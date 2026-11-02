package integration

import (
	"net/http"
	"time"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
)

// HTTPTransport instruments the outbound HTTP requests of one integration: it
// opens a span per transport attempt, propagates the trace to whoever is being
// called, and keeps the duration and size instruments.
//
// Build one per client, at construction, and hold it: it registers its
// instruments once.
type HTTPTransport struct {
	inst *Instrument

	keyMethod     attribute.Key
	keyEndpoint   attribute.Key
	keyPath       attribute.Key
	keyStatusCode attribute.Key
	keyRequest    attribute.Key
	keyResponse   attribute.Key

	duration     otelmetric.Float64Histogram
	requestSize  otelmetric.Int64Histogram
	responseSize otelmetric.Int64Histogram
}

// NewHTTPTransport returns the outbound-HTTP instrumentation for this
// integration.
//
// Instrument construction cannot fail in a way worth surfacing — a provider that
// rejects an instrument hands back a working no-op — so the errors are dropped
// rather than turned into a startup failure over telemetry.
func (i *Instrument) NewHTTPTransport() *HTTPTransport {
	t := &HTTPTransport{
		inst:          i,
		keyMethod:     i.Key(suffixMethod),
		keyEndpoint:   i.Key(suffixEndpoint),
		keyPath:       i.Key(suffixPath),
		keyStatusCode: i.Key(suffixStatusCode),
		keyRequest:    i.Key(suffixRequestSize),
		keyResponse:   i.Key(suffixResponseSize),
	}

	t.duration, _ = i.meter.Float64Histogram(i.keyPrefix+suffixDuration,
		otelmetric.WithUnit("s"),
		otelmetric.WithDescription("Duration of outbound HTTP requests."),
		otelmetric.WithExplicitBucketBoundaries(durationBuckets...),
	)
	t.requestSize, _ = i.meter.Int64Histogram(i.keyPrefix+suffixRequestSize,
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Size of outbound HTTP request bodies."),
		otelmetric.WithExplicitBucketBoundaries(sizeBuckets...),
	)
	t.responseSize, _ = i.meter.Int64Histogram(i.keyPrefix+suffixResponseSize,
		otelmetric.WithUnit("By"),
		otelmetric.WithDescription("Size of inbound HTTP response bodies."),
		otelmetric.WithExplicitBucketBoundaries(sizeBuckets...),
	)

	return t
}

// RoundTripper wraps base so that every attempt it makes is traced, measured, and
// carries the trace context to the service being called.
//
// A nil base means http.DefaultTransport, matching http.Client.
func (t *HTTPTransport) RoundTripper(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}

	return roundTripper{transport: t, base: base}
}

// roundTripper is the instrumented http.RoundTripper HTTPTransport hands back.
type roundTripper struct {
	transport *HTTPTransport
	base      http.RoundTripper
}

// CloseIdleConnections forwards to the wrapped transport when it can close idle
// connections. http.Client.CloseIdleConnections finds it by type assertion on the
// client's transport, so without it a wrapped transport's pool would never be
// released.
func (r roundTripper) CloseIdleConnections() {
	if closer, ok := r.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// RoundTrip traces one attempt.
//
// The span ends when the round trip returns, not when the response body is
// closed. Ending on body close would make the span cover the read as well, at the
// cost that a caller who leaks the body leaks the span with it — silently, since
// a span that never ends is never exported. The size of the body is taken from
// Content-Length instead, which is known without holding anything open.
func (r roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	t := r.transport

	// Named for what the attempt is, not for the fact that there was one: a trace
	// making twenty outbound calls used to show twenty spans all called "Attempt".
	// The method is the part worth having in the name — a read and a write read
	// differently at a glance — while the endpoint and the path, which carry real
	// cardinality, stay attributes.
	method := NormalizeMethod(req.Method)
	ctx, span := t.inst.Start(req.Context(), trace.SpanKindClient, method+" / Attempt")
	defer span.End()

	// Hand the trace to whoever is being called, so that a request crossing two
	// helix services is one trace rather than two. The header is set on a clone:
	// the RoundTripper contract forbids modifying the request it was given.
	req = req.Clone(ctx)
	t.inst.propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))

	endpoint := req.URL.Scheme + "://" + req.URL.Host

	started := time.Now()
	resp, err := r.base.RoundTrip(req)
	elapsed := time.Since(started)

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}

	if span.IsRecording() {
		attrs := make([]attribute.KeyValue, 0, 6)
		attrs = append(attrs,
			t.keyMethod.String(method),
			t.keyEndpoint.String(endpoint),
			t.keyPath.String(req.URL.Path),
		)
		if req.ContentLength >= 0 {
			attrs = append(attrs, t.keyRequest.Int64(req.ContentLength))
		}
		if resp != nil {
			attrs = append(attrs, t.keyStatusCode.Int(status))
			if resp.ContentLength >= 0 {
				attrs = append(attrs, t.keyResponse.Int64(resp.ContentLength))
			}
		}

		span.SetAttributes(attrs...)
	}

	// A 5xx is the far side saying it failed, and is recorded as a failure here
	// for the same reason the server side records one: it is the server's problem,
	// not the caller's. A 4xx is the caller's and leaves the span unset. The two
	// halves of this package now agree on that.
	switch {
	case err != nil:
		span.RecordError("failed to execute request", err)
	case status >= http.StatusInternalServerError:
		span.RecordError("failed to execute request", errStatus(status))
	}

	measuredAttrs := make([]attribute.KeyValue, 0, len(t.inst.identity)+3)
	measuredAttrs = t.inst.AppendIdentity(measuredAttrs)
	measuredAttrs = append(measuredAttrs,
		t.keyMethod.String(method),
		t.keyEndpoint.String(endpoint),
		t.keyStatusCode.Int(status),
	)

	measured := otelmetric.WithAttributes(measuredAttrs...)

	t.duration.Record(ctx, elapsed.Seconds(), measured)
	if req.ContentLength >= 0 {
		t.requestSize.Record(ctx, req.ContentLength, measured)
	}
	if resp != nil && resp.ContentLength >= 0 {
		t.responseSize.Record(ctx, resp.ContentLength, measured)
	}

	return resp, err
}
