package trace

import (
	"testing"

	"github.com/mountaya/helix.go/event"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestStampEvent_StampsEntrySpanAndMergesBaggage is the end-to-end check for the
// replacement of the per-span reflective walk: the Event's attributes land on the
// span that is already current — the one otelhttp created — and only the
// allow-listed identifiers reach baggage.
func TestStampEvent_StampsEntrySpanAndMergesBaggage(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })

	upstream, err := baggage.NewMember("upstream.key", "kept")
	require.NoError(t, err)
	b, err := baggage.New(upstream)
	require.NoError(t, err)

	e := event.Event{
		ID:             "evt_1",
		Name:           "checkout",
		UserID:         "user_1",
		IP:             "203.0.113.7",
		OrganizationID: "org_1",
	}

	ctx := baggage.ContextWithBaggage(t.Context(), b)
	ctx = event.NewContext(ctx, e)
	ctx, span := provider.Tracer("test").Start(ctx, "GET /checkout")

	ctx = StampEvent(ctx)
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	attrs := map[string]string{}
	for _, kv := range spans[0].Attributes {
		attrs[string(kv.Key)] = kv.Value.String()
	}

	// The full Event lands on the span, including the personal data, because a
	// span stays inside the trace.
	assert.Equal(t, "checkout", attrs["event.name"])
	assert.Equal(t, "user_1", attrs["enduser.id"], "semconv key, not a bespoke one")
	assert.Equal(t, "203.0.113.7", attrs["client.address"])

	// Baggage crosses process boundaries, so it carries the allow-list only.
	got := baggage.FromContext(ctx)
	assert.Equal(t, "kept", got.Member("upstream.key").Value(), "upstream baggage must survive")
	assert.Equal(t, "org_1", got.Member("event.organization_id").Value())
	assert.Empty(t, got.Member("enduser.id").Value())
	assert.Empty(t, got.Member("event.user_id").Value())
	assert.Empty(t, got.Member("event.ip").Value())
}

func TestStampEvent_NoEventIsANoOp(t *testing.T) {
	ctx := StampEvent(t.Context())

	assert.Empty(t, baggage.FromContext(ctx).Members())
}
