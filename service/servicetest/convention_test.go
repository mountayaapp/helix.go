package servicetest

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// recordingTB captures what AssertConvention reports instead of failing the test
// running it. Embedding testing.TB satisfies the interface's unexported method,
// which is what makes a stand-in possible at all.
type recordingTB struct {
	testing.TB

	errors []string
}

func (tb *recordingTB) Helper() {}

func (tb *recordingTB) Errorf(format string, args ...any) {
	tb.errors = append(tb.errors, fmt.Sprintf(format, args...))
}

// emit records one span under the given scope, name and attributes, and returns
// it in the shape AssertConvention reads.
func emit(tb testing.TB, scope, name string, attrs ...attribute.KeyValue) []sdktrace.ReadOnlySpan {
	tb.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	_, span := provider.Tracer(scope).Start(tb.Context(), name, oteltrace.WithAttributes(attrs...))
	span.End()

	return recorder.Ended()
}

func TestAssertConvention(t *testing.T) {
	testcases := []struct {
		name  string
		span  []sdktrace.ReadOnlySpan
		fails int
	}{
		{
			name: "conforming span passes",
			span: emit(t, "github.com/mountaya/helix.go", "PostgreSQL: Exec",
				attribute.String("postgres.name", "primary"),
				attribute.String("postgres.query", "SELECT 1"),
			),
		},
		{
			name: "event attributes are allowed alongside an integration's own",
			span: emit(t, "github.com/mountaya/helix.go", "REST: Request",
				attribute.String("rest.route", "/things/:id"),
				attribute.String("event.id", "evt_1"),
				attribute.String("enduser.id", "usr_1"),
				attribute.String("user_agent.original", "curl/8"),
				attribute.String("client.address", "127.0.0.1"),
			),
		},
		{
			name: "a third-party scope is not itself a violation",
			span: emit(t, "example.com/thirdparty/helix-integration", "Stripe: Charge",
				attribute.String("stripe.customer", "cus_1"),
			),
		},
		{
			name: "span with no attributes passes",
			span: emit(t, "github.com/mountaya/helix.go", "Bucket: Write"),
		},
		{
			name:  "otelhttp's span name does not follow the convention",
			span:  emit(t, "otelhttp", "GET /thing"),
			fails: 1,
		},
		{
			name:  "a name with no operation does not follow the convention",
			span:  emit(t, "github.com/mountaya/helix.go", "PostgreSQL: "),
			fails: 1,
		},
		{
			name:  "gocloud's un-namespaced key is rejected",
			span:  emit(t, "gocloud.dev/blob", "Bucket: Write", attribute.String("gocdk_method", "NewWriter")),
			fails: 1,
		},
		{
			name: "a foreign vocabulary is rejected even when it is internally consistent",
			span: emit(t, "github.com/mountaya/helix.go", "REST: Request",
				attribute.String("http.route", "/thing"),
				attribute.String("http.target", "/thing"),
			),
			fails: 2,
		},
		{
			name: "mixing two namespaces is rejected",
			span: emit(t, "github.com/mountaya/helix.go", "REST: Request",
				attribute.String("rest.route", "/thing"),
				attribute.String("graphql.operation", "Query"),
			),
			fails: 1,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tb := &recordingTB{TB: t}
			AssertConvention(tb, tc.span)

			assert.Len(t, tb.errors, tc.fails, "reported: %v", tb.errors)
		})
	}
}

func TestScopes(t *testing.T) {
	var spans []sdktrace.ReadOnlySpan
	spans = append(spans, emit(t, "gocloud.dev/blob", "Bucket: Write")...)
	spans = append(spans, emit(t, "github.com/mountaya/helix.go", "Bucket: Write")...)
	spans = append(spans, emit(t, "github.com/mountaya/helix.go", "Bucket: Read")...)

	assert.Equal(t, []string{"github.com/mountaya/helix.go", "gocloud.dev/blob"}, Scopes(spans))
	assert.Empty(t, Scopes(nil))
}

// TestAssertConventionAcrossEveryEmittedSpan is the shape every integration's
// golden test uses: whatever the Service produced, all of it conforms.
func TestAssertConventionAcrossEveryEmittedSpan(t *testing.T) {
	var spans []sdktrace.ReadOnlySpan
	spans = append(spans, emit(t, "github.com/mountaya/helix.go", "Valkey: Get", attribute.String("valkey.key", "k"))...)
	spans = append(spans, emit(t, "github.com/mountaya/helix.go", "ClickHouse: Select", attribute.String("clickhouse.database", "default"))...)
	require.Len(t, spans, 2)

	AssertConvention(t, spans)
}
