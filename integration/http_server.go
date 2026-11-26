package integration

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/mountaya/helix.go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// HTTPServer instruments the inbound HTTP requests of one integration: it opens
// the entry span of each trace, records what the response turned out to be, and
// keeps the duration and size instruments.
//
// It is what replaced otelhttp on the three HTTP server integrations. The reason
// is not that otelhttp is poor — it is that it emits the OpenTelemetry semantic
// conventions, at whichever version it happens to be pinned to, which is not the
// vocabulary the other six integrations speak. See the package overview of
// service for the trade that represents.
//
// Build one per server, at construction, and hold it: it registers its
// instruments once.
type HTTPServer struct {
	inst   *Instrument
	public bool

	keyMethod     attribute.Key
	keyPath       attribute.Key
	keyRoute      attribute.Key
	keyStatusCode attribute.Key
	keyRequest    attribute.Key
	keyResponse   attribute.Key

	duration     otelmetric.Float64Histogram
	requestSize  otelmetric.Int64Histogram
	responseSize otelmetric.Int64Histogram
}

// HTTPServerOptions configures the inbound-HTTP instrumentation of a server.
type HTTPServerOptions struct {

	// PublicEndpoint marks a server reachable from outside the trust boundary of
	// the services it belongs to — from the internet, typically.
	//
	// Such a server does not continue the caller's trace: it starts a new one,
	// linked to the caller's span so that the two can still be navigated between.
	// And it ignores the caller's baggage, so that neither an Event nor anything
	// else a client put in its headers is propagated onward as though a trusted
	// upstream service had set it. A caller that could set the trace context could
	// otherwise also decide whether this service's spans are sampled.
	//
	// Leave it unset for a server only other services of yours call: continuing
	// their trace and their baggage is what makes a request crossing several
	// services one trace.
	PublicEndpoint bool
}

// NewHTTPServer returns the inbound-HTTP instrumentation for this integration.
//
// Instrument construction cannot fail in a way worth surfacing — a provider that
// rejects an instrument hands back a working no-op — so the errors are dropped
// rather than turned into a startup failure over telemetry.
func (i *Instrument) NewHTTPServer(opts HTTPServerOptions) *HTTPServer {
	s := &HTTPServer{
		inst:          i,
		public:        opts.PublicEndpoint,
		keyMethod:     i.Key(suffixMethod),
		keyPath:       i.Key(suffixPath),
		keyRoute:      i.Key(suffixRoute),
		keyStatusCode: i.Key(suffixStatusCode),
		keyRequest:    i.Key(suffixRequestSize),
		keyResponse:   i.Key(suffixResponseSize),
	}

	s.duration, _ = i.meter.Float64Histogram(i.keyPrefix+suffixDuration,
		otelmetric.WithUnit("s"),
		otelmetric.WithDescription("Duration of inbound HTTP requests."),
		otelmetric.WithExplicitBucketBoundaries(durationBuckets...),
	)
	s.requestSize, _ = i.meter.Int64Histogram(i.keyPrefix+suffixRequestSize,
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Size of inbound HTTP request bodies."),
		otelmetric.WithExplicitBucketBoundaries(sizeBuckets...),
	)
	s.responseSize, _ = i.meter.Int64Histogram(i.keyPrefix+suffixResponseSize,
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Size of outbound HTTP response bodies."),
		otelmetric.WithExplicitBucketBoundaries(sizeBuckets...),
	)

	return s
}

// Middleware wraps next so that every request it serves is traced and measured.
//
// It is meant to be outermost, around the application's own middleware, so that a
// response that middleware writes itself — a 401 from authentication, a 429 from a
// rate limiter — is traced and measured like any other. A router that matches a
// route further in names the span after it with SetRoute.
//
// A handler that panics is recorded as a 500 with the panic as its error, and the
// panic is then re-raised for net/http to handle as it always does.
func (s *HTTPServer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw, req, end := s.Observe(rw, req, "")

		defer func() {
			if v := recover(); v != nil {
				end(panicError{value: v})
				panic(v)
			}
		}()

		next.ServeHTTP(rw, req)
		end(nil)
	})
}

// StampEventMiddleware wraps next so that the Event carried by the request
// context is stamped onto the span already open, and the event's propagated
// identifiers are merged into the request's baggage.
//
// Apply it at the point where the Event is first in the context: innermost,
// closest to the handlers, because the application's middleware is what puts the
// Event there and the entry span is opened around that middleware.
func StampEventMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		next.ServeHTTP(rw, req.WithContext(trace.StampEvent(req.Context())))
	})
}

// observationKey is the context key the request's observation is stored under,
// for SetRoute to find.
type observationKey struct{}

// observation is what SetRoute can still change about a request after its span
// was opened.
type observation struct {
	route string
}

// SetRoute names the request's span after the route a router matched, and
// records the route on the span and on the request's measurements.
//
// It is for a router running inside Middleware: the span is opened before
// routing, when only the method is known, and a route template is the one thing
// worth having in a span name that cannot be recovered afterwards. Pass the
// template ("/things/:id"), never the concrete path: a request for a path nobody
// routes must not be able to mint a new span name or time series.
//
// It does nothing for a request that was not observed.
func (s *HTTPServer) SetRoute(req *http.Request, route string) {
	obs, ok := req.Context().Value(observationKey{}).(*observation)
	if !ok || route == "" {
		return
	}

	obs.route = route
	oteltrace.SpanFromContext(req.Context()).SetName(s.inst.SpanName(NormalizeMethod(req.Method) + " " + route))
}

// RedactPath records the route template a router matched as the request's path,
// in place of the concrete path its span was opened with.
//
// It is for a route whose path carries a value a trace backend must not keep, a
// credential or a one-time token, where the template ("/things/:id") is all the
// span may say. The concrete path is replaced before the span ends, so it is never
// exported, and it is recorded nowhere else: neither the span name nor the
// measurements ever carry it.
//
// It does nothing for a request that was not observed, or for an empty route.
func (s *HTTPServer) RedactPath(req *http.Request, route string) {
	if _, ok := req.Context().Value(observationKey{}).(*observation); !ok || route == "" {
		return
	}

	oteltrace.SpanFromContext(req.Context()).SetAttributes(s.keyPath.String(route))
}

// Observe opens the entry span for an inbound request and returns a
// ResponseWriter that records what the handler writes, the request carrying the
// span, and the function that closes the observation.
//
// route is the matched route template when the caller is a router that has
// matched one ("/things/:id"), and empty otherwise; SetRoute can supply it later.
// The raw path goes on the span but never into the span name or onto an
// instrument.
//
// attrs are additional attributes for the span, for whatever the caller knows
// that this cannot see.
//
// The returned function takes the error a handler returned, for the routers whose
// handlers return one. Pass nil when there is none; the status code is what
// classifies the response either way. A caller that does not go through
// Middleware is responsible for reporting a panic through it.
func (s *HTTPServer) Observe(
	rw http.ResponseWriter, req *http.Request, route string, attrs ...attribute.KeyValue,
) (http.ResponseWriter, *http.Request, func(error)) {
	method := NormalizeMethod(req.Method)
	ctx, span := s.startEntry(req, method, route)

	if span.IsRecording() {
		opened := make([]attribute.KeyValue, 0, len(attrs)+2)
		opened = append(opened, s.keyMethod.String(method), s.keyPath.String(req.URL.Path))
		span.SetAttributes(append(opened, attrs...)...)
	}

	obs := &observation{route: route}
	ctx = context.WithValue(ctx, observationKey{}, obs)

	started := time.Now()
	recorder := newRecorder(rw)
	req = req.WithContext(ctx)

	return recorder, req, func(err error) {
		status := recorder.status()

		var panicked panicError
		switch {
		case errors.As(err, &panicked):
			status = http.StatusInternalServerError
		case status == 0:
			status = http.StatusOK
		}

		if span.IsRecording() {
			attrs := make([]attribute.KeyValue, 0, 4)
			attrs = append(attrs, s.keyStatusCode.Int(status), s.keyResponse.Int64(recorder.written()))
			if obs.route != "" {
				attrs = append(attrs, s.keyRoute.String(obs.route))
			}
			if req.ContentLength >= 0 {
				attrs = append(attrs, s.keyRequest.Int64(req.ContentLength))
			}

			span.SetAttributes(attrs...)
		}

		// A 5xx is the server saying it failed, whether or not the handler returned
		// an error to say so. A 4xx is the client's problem and leaves the span
		// unset, which is what lets a backend separate the two.
		span.RecordError("failed to serve request", err)
		if err == nil && status >= http.StatusInternalServerError {
			span.RecordError("failed to serve request", errStatus(status))
		}

		span.End()

		// Identity first: without it two servers of the same kind — a public API
		// and an internal one on one Service — write this integration's metrics
		// under series nothing can tell apart.
		measuredAttrs := make([]attribute.KeyValue, 0, len(s.inst.identity)+3)
		measuredAttrs = s.inst.AppendIdentity(measuredAttrs)
		measuredAttrs = append(measuredAttrs, s.keyMethod.String(method), s.keyStatusCode.Int(status))
		if obs.route != "" {
			measuredAttrs = append(measuredAttrs, s.keyRoute.String(obs.route))
		}

		measured := otelmetric.WithAttributes(measuredAttrs...)

		s.duration.Record(ctx, time.Since(started).Seconds(), measured)
		s.responseSize.Record(ctx, recorder.written(), measured)
		if req.ContentLength >= 0 {
			s.requestSize.Record(ctx, req.ContentLength, measured)
		}
	}
}

// startEntry opens the entry span of an inbound request, continuing the caller's
// trace or not depending on whether the server is public.
func (s *HTTPServer) startEntry(req *http.Request, method, route string) (context.Context, *trace.Span) {
	operation := method
	if route != "" {
		operation += " " + route
	}

	carrier := propagation.HeaderCarrier(req.Header)

	// Continue the caller's trace when it sent one, and pick up its baggage. This
	// is what makes a request crossing two helix services one trace rather than
	// two. The propagator is the Service's own, so this keeps working when the
	// Service was told to leave the global registry alone.
	if !s.public {
		ctx := s.inst.propagator.Extract(req.Context(), carrier)
		return s.inst.StartEntry(ctx, trace.SpanKindServer, operation)
	}

	// A public server reads the caller's span context only to link to it, and
	// starts a trace of its own. Its baggage is never extracted.
	var opts []oteltrace.SpanStartOption
	opts = append(opts, oteltrace.WithNewRoot())
	if remote := oteltrace.SpanContextFromContext(s.inst.propagator.Extract(context.Background(), carrier)); remote.IsValid() {
		opts = append(opts, oteltrace.WithLinks(oteltrace.Link{SpanContext: remote}))
	}

	ctx, span := s.inst.start(req.Context(), trace.SpanKindServer, operation, opts...)
	return trace.StampEvent(ctx), span
}

// errStatus is the error a 5xx response is recorded as when the handler did not
// return one of its own.
type errStatus int

// Error renders the status the way net/http names it, falling back to the number
// for a status net/http has no name for — an exception event whose message is the
// empty string says nothing at all.
func (e errStatus) Error() string {
	if text := http.StatusText(int(e)); text != "" {
		return text
	}

	return "HTTP " + strconv.Itoa(int(e))
}
