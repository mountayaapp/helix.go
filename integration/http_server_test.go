package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// recordingHTTPServer is an HTTPServer whose spans and metrics are recorded in
// memory.
type recordingHTTPServer struct {
	*HTTPServer

	spans  *tracetest.SpanRecorder
	reader *sdkmetric.ManualReader
}

func newRecordingHTTPServer(t *testing.T, opts HTTPServerOptions) recordingHTTPServer {
	t.Helper()

	spans := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()

	inst, err := NewInstrument(InstrumentConfig{
		Identifier:     "rest",
		Humanized:      "REST",
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)),
		MeterProvider:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		Propagator:     propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}),
	})
	require.NoError(t, err)

	return recordingHTTPServer{HTTPServer: inst.NewHTTPServer(opts), spans: spans, reader: reader}
}

// metric returns the metric named name, failing the test when there is none.
func (r recordingHTTPServer) metric(t *testing.T, name string) metricdata.Metrics {
	t.Helper()

	var rm metricdata.ResourceMetrics
	require.NoError(t, r.reader.Collect(t.Context(), &rm))

	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == name {
				return m
			}
		}
	}

	t.Fatalf("metric %q was not recorded", name)
	return metricdata.Metrics{}
}

func serve(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	return rw
}

func TestHTTPServer_DurationUsesSecondsBuckets(t *testing.T) {
	s := newRecordingHTTPServer(t, HTTPServerOptions{})

	serve(s.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})),
		httptest.NewRequest(http.MethodGet, "/", nil))

	histogram, ok := s.metric(t, "rest.request.duration").Data.(metricdata.Histogram[float64])
	require.True(t, ok)
	require.Len(t, histogram.DataPoints, 1)
	assert.Equal(t, durationBuckets, histogram.DataPoints[0].Bounds)
}

func TestHTTPServer_UnknownMethodIsNotRecordedVerbatim(t *testing.T) {
	s := newRecordingHTTPServer(t, HTTPServerOptions{})

	serve(s.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})),
		httptest.NewRequest("FOOBAR123", "/", nil))

	spans := s.spans.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "REST: _OTHER", spans[0].Name())

	histogram := s.metric(t, "rest.request.duration").Data.(metricdata.Histogram[float64])
	method, _ := histogram.DataPoints[0].Attributes.Value("rest.method")
	assert.Equal(t, "_OTHER", method.AsString())
}

func TestHTTPServer_PanicIsRecordedAsA500(t *testing.T) {
	s := newRecordingHTTPServer(t, HTTPServerOptions{})

	handler := s.Middleware(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		panic("boom")
	}))

	assert.PanicsWithValue(t, "boom", func() {
		serve(handler, httptest.NewRequest(http.MethodGet, "/", nil))
	})

	spans := s.spans.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Error, spans[0].Status().Code)
	assert.Contains(t, spans[0].Attributes(), attribute.Int("rest.status_code", http.StatusInternalServerError))
}

func TestHTTPServer_SetRouteNamesTheSpan(t *testing.T) {
	s := newRecordingHTTPServer(t, HTTPServerOptions{})

	serve(s.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		s.SetRoute(req, "/things/:id")
	})), httptest.NewRequest(http.MethodGet, "/things/42", nil))

	spans := s.spans.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "REST: GET /things/:id", spans[0].Name())
	assert.Contains(t, spans[0].Attributes(), attribute.String("rest.route", "/things/:id"))

	histogram := s.metric(t, "rest.request.duration").Data.(metricdata.Histogram[float64])
	route, _ := histogram.DataPoints[0].Attributes.Value("rest.route")
	assert.Equal(t, "/things/:id", route.AsString())
}

func TestHTTPServer_RedactPathRecordsTheTemplate(t *testing.T) {
	s := newRecordingHTTPServer(t, HTTPServerOptions{})

	serve(s.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		s.SetRoute(req, "/things/:id")
		s.RedactPath(req, "/things/:id")
	})), httptest.NewRequest(http.MethodGet, "/things/secret-value", nil))

	spans := s.spans.Ended()
	require.Len(t, spans, 1)
	assert.Contains(t, spans[0].Attributes(), attribute.String("rest.path", "/things/:id"))
	for _, attr := range spans[0].Attributes() {
		assert.NotContains(t, attr.Value.String(), "secret-value", "attribute %s", attr.Key)
	}
	assert.NotContains(t, spans[0].Name(), "secret-value")
}

func TestHTTPServer_RedactPathLeavesAnUnobservedRequestAlone(t *testing.T) {
	s := newRecordingHTTPServer(t, HTTPServerOptions{})

	assert.NotPanics(t, func() {
		s.RedactPath(httptest.NewRequest(http.MethodGet, "/things/42", nil), "/things/:id")
	})
	assert.Empty(t, s.spans.Ended())
}

func TestHTTPServer_RedactPathWithoutARouteKeepsThePath(t *testing.T) {
	s := newRecordingHTTPServer(t, HTTPServerOptions{})

	serve(s.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		s.RedactPath(req, "")
	})), httptest.NewRequest(http.MethodGet, "/things/42", nil))

	spans := s.spans.Ended()
	require.Len(t, spans, 1)
	assert.Contains(t, spans[0].Attributes(), attribute.String("rest.path", "/things/42"))
}

func TestHTTPServer_InformationalStatusIsNotTheResponse(t *testing.T) {
	s := newRecordingHTTPServer(t, HTTPServerOptions{})

	serve(s.Middleware(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusEarlyHints)
		rw.WriteHeader(http.StatusAccepted)
	})), httptest.NewRequest(http.MethodGet, "/", nil))

	spans := s.spans.Ended()
	require.Len(t, spans, 1)
	assert.Contains(t, spans[0].Attributes(), attribute.Int("rest.status_code", http.StatusAccepted))
}

// inboundRequest returns a request carrying a caller's trace context and baggage.
func inboundRequest(t *testing.T) (*http.Request, oteltrace.SpanContext) {
	t.Helper()

	caller := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    oteltrace.TraceID{1},
		SpanID:     oteltrace.SpanID{2},
		TraceFlags: oteltrace.FlagsSampled,
		Remote:     true,
	})

	member, err := baggage.NewMemberRaw("event.name", "from-the-caller")
	require.NoError(t, err)
	b, err := baggage.New(member)
	require.NoError(t, err)

	ctx := baggage.ContextWithBaggage(oteltrace.ContextWithSpanContext(context.Background(), caller), b)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}).
		Inject(ctx, propagation.HeaderCarrier(req.Header))

	return req, caller
}

func TestHTTPServer_ContinuesTheCallersTrace(t *testing.T) {
	s := newRecordingHTTPServer(t, HTTPServerOptions{})
	req, caller := inboundRequest(t)

	var seen baggage.Baggage
	serve(s.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		seen = baggage.FromContext(req.Context())
	})), req)

	spans := s.spans.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, caller.TraceID(), spans[0].SpanContext().TraceID())
	assert.Equal(t, "from-the-caller", seen.Member("event.name").Value())
}

func TestHTTPServer_PublicEndpointStartsItsOwnTrace(t *testing.T) {
	s := newRecordingHTTPServer(t, HTTPServerOptions{PublicEndpoint: true})
	req, caller := inboundRequest(t)

	var seen baggage.Baggage
	serve(s.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		seen = baggage.FromContext(req.Context())
	})), req)

	spans := s.spans.Ended()
	require.Len(t, spans, 1)
	assert.NotEqual(t, caller.TraceID(), spans[0].SpanContext().TraceID())
	require.Len(t, spans[0].Links(), 1)
	assert.Equal(t, caller.TraceID(), spans[0].Links()[0].SpanContext.TraceID())
	assert.Zero(t, seen.Len(), "a public endpoint must not take its caller's baggage")
}

func TestNormalizeMethod(t *testing.T) {
	assert.Equal(t, http.MethodGet, NormalizeMethod(""))
	assert.Equal(t, http.MethodPost, NormalizeMethod(http.MethodPost))
	assert.Equal(t, MethodOther, NormalizeMethod("get"))
	assert.Equal(t, MethodOther, NormalizeMethod("PURGE"))
}
