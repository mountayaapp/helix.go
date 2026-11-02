package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/event"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/service/servicetest"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// operationOf returns the span of the logical request among spans: the one the
// attempts are children of.
func operationOf(t *testing.T, spans []sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range spans {
		if !strings.HasSuffix(span.Name(), " / Attempt") {
			return span
		}
	}

	t.Fatalf("no operation span among %d spans", len(spans))
	return nil
}

// attributeOf returns the value of key on span, and whether it is set.
func attributeOf(span sdktrace.ReadOnlySpan, key attribute.Key) (attribute.Value, bool) {
	for _, attr := range span.Attributes() {
		if attr.Key == key {
			return attr.Value, true
		}
	}

	return attribute.Value{}, false
}

// outcomes returns the outcome of every httpclient.operation.duration data point
// recorded.
func outcomes(t *testing.T, rec *servicetest.Recorder) []string {
	t.Helper()

	var out []string
	for _, m := range rec.Metrics() {
		if m.Name != "httpclient.operation.duration" {
			continue
		}

		histogram, ok := m.Data.(metricdata.Histogram[float64])
		require.True(t, ok)

		for _, point := range histogram.DataPoints {
			outcome, _ := point.Attributes.Value("httpclient.outcome")
			out = append(out, outcome.AsString())
		}
	}

	return out
}

// TestDo_Final5xxIsRecordedAsFailure pins that a request ending in a 5xx is a
// failed operation: it used to return without recording one, so the operation
// metric counted it as a success while the attempt beneath it recorded an error.
func TestDo_Final5xxIsRecordedAsFailure(t *testing.T) {
	testcases := []struct {
		name      string
		endpoints int
	}{
		{name: "single endpoint", endpoints: 1},
		{name: "every endpoint", endpoints: 2},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var endpoints []string
			for range tc.endpoints {
				url, _ := newStatusServer(t, http.StatusServiceUnavailable)
				endpoints = append(endpoints, url)
			}

			svc, rec := servicetest.New(t)
			client, err := Connect(t.Context(), svc, Config{Endpoints: endpoints})
			require.NoError(t, err)

			resp, err := client.Get(t.Context(), "/")
			require.NoError(t, err)
			drainClose(resp)
			assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

			op := operationOf(t, rec.Spans())
			assert.Equal(t, codes.Error, op.Status().Code)

			attempts, _ := attributeOf(op, attrKeyAttempts)
			assert.Equal(t, int64(tc.endpoints), attempts.AsInt64())

			assert.Equal(t, []string{"error"}, outcomes(t, rec))
		})
	}
}

// TestDo_SpansCarryNoQueryString pins the redaction on the telemetry side: the
// operation recorded the transport error verbatim, query string included.
func TestDo_SpansCarryNoQueryString(t *testing.T) {
	url := deadURL(t)

	svc, rec := servicetest.New(t)
	client, err := Connect(t.Context(), svc, Config{Endpoints: []string{url}})
	require.NoError(t, err)

	resp, err := client.Get(t.Context(), "/things?api_key=secret")
	drainClose(resp)
	require.Error(t, err)

	spans := rec.Spans()
	require.NotEmpty(t, spans)

	for _, span := range spans {
		assert.NotContains(t, span.Status().Description, "secret")

		for _, attr := range span.Attributes() {
			assert.NotContains(t, attr.Value.String(), "secret", "attribute %s of %q", attr.Key, span.Name())
		}

		for _, ev := range span.Events() {
			for _, attr := range ev.Attributes {
				assert.NotContains(t, attr.Value.String(), "secret", "event attribute %s of %q", attr.Key, span.Name())
			}
		}
	}
}

// TestDo_ContextEndingDuringFailoverKeepsAttributes pins that a request cut
// short by its context still says how far it got.
func TestDo_ContextEndingDuringFailoverKeepsAttributes(t *testing.T) {
	slow, _ := newServer(t, func(rw http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		rw.WriteHeader(http.StatusOK)
	})
	up, _ := newStatusServer(t, http.StatusOK)

	svc, rec := servicetest.New(t)
	client, err := Connect(t.Context(), svc, Config{Endpoints: []string{slow, up}})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	resp, err := client.Get(ctx, "/")
	drainClose(resp)
	require.Error(t, err)

	op := operationOf(t, rec.Spans())
	assert.Equal(t, codes.Error, op.Status().Code)

	attempts, ok := attributeOf(op, attrKeyAttempts)
	require.True(t, ok)
	assert.Equal(t, int64(1), attempts.AsInt64())

	endpoint, ok := attributeOf(op, attrKeyEndpoint)
	require.True(t, ok)
	assert.Equal(t, slow, endpoint.AsString())
}

// TestCheckHealth_EmitsNoTelemetry pins that readiness polls are not traffic:
// every poll used to open a span per endpoint and record into the request
// metrics, a custom HealthCheck's requests included.
func TestCheckHealth_EmitsNoTelemetry(t *testing.T) {
	url, hits := newStatusServer(t, http.StatusOK)

	svc, rec := servicetest.New(t)

	_, err := Connect(t.Context(), svc, Config{Name: "probed", Endpoints: []string{url}, HealthPath: "/health"})
	require.NoError(t, err)

	_, err = Connect(t.Context(), svc, Config{
		Name:      "custom",
		Endpoints: []string{url},
		HealthCheck: func(ctx context.Context, client HTTPClient) error {
			resp, err := client.Get(ctx, "/custom")
			drainClose(resp)
			return err
		},
	})
	require.NoError(t, err)

	require.NoError(t, svc.CheckHealth(t.Context()))
	assert.Equal(t, int64(2), hits.Load())

	assert.Empty(t, rec.Spans())
	for _, m := range rec.Metrics() {
		assert.False(t, strings.HasPrefix(m.Name, identifier+"."), "metric %s was recorded", m.Name)
	}
}

// newTracingService builds a Service that leaves the OpenTelemetry globals alone
// and records spans, so that what reaches the wire comes from the Service's own
// propagator and not from the global one.
func newTracingService(t *testing.T) *service.Service {
	t.Helper()

	t.Setenv("OTEL_SDK_DISABLED", "true")

	svc, err := service.New(t.Context(), service.Config{
		SkipGlobals:    true,
		TracerProvider: sdktrace.NewTracerProvider(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, svc.Stop(context.Background()))
	})

	return svc
}

// echoPropagation starts a server reporting the propagation headers it received.
func echoPropagation(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Echo-Traceparent", req.Header.Get("Traceparent"))
		rw.Header().Set("X-Echo-Baggage", req.Header.Get("Baggage"))
		rw.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

// eventContext returns a context carrying an Event, stamped into the baggage the
// way an entry span stamps it.
func eventContext(ctx context.Context) context.Context {
	ctx = event.NewContext(ctx, event.Event{
		ID:             "evt-1",
		Name:           "signup",
		OrganizationID: "org-1",
		UserID:         "user-1",
		IP:             "203.0.113.7",
	})

	return trace.StampEvent(ctx)
}

func TestDo_PropagatesTraceAndAllowListedBaggage(t *testing.T) {
	url := echoPropagation(t)

	client, err := Connect(t.Context(), newTracingService(t), Config{Endpoints: []string{url}})
	require.NoError(t, err)

	resp, err := client.Get(eventContext(t.Context()), "/")
	require.NoError(t, err)
	drainClose(resp)

	assert.NotEmpty(t, resp.Header.Get("X-Echo-Traceparent"))

	baggage := resp.Header.Get("X-Echo-Baggage")
	assert.Contains(t, baggage, "event.id=evt-1")
	assert.Contains(t, baggage, "event.organization_id=org-1")
	assert.NotContains(t, baggage, "user-1")
	assert.NotContains(t, baggage, "203.0.113.7")
}

func TestDo_CustomPropagatorSendsNoBaggage(t *testing.T) {
	url := echoPropagation(t)

	client, err := Connect(t.Context(), newTracingService(t), Config{
		Endpoints:  []string{url},
		Propagator: propagation.TraceContext{},
	})
	require.NoError(t, err)

	resp, err := client.Get(eventContext(t.Context()), "/")
	require.NoError(t, err)
	drainClose(resp)

	assert.NotEmpty(t, resp.Header.Get("X-Echo-Traceparent"))
	assert.Empty(t, resp.Header.Get("X-Echo-Baggage"))
}
