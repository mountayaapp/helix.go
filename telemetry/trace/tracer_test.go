package trace

import (
	"testing"

	"github.com/mountaya/helix.go/event"
	internaltrace "github.com/mountaya/helix.go/internal/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/baggage"
)

// richEvent is an Event populated across the personal and non-personal fields,
// used to assert that only the allow-listed subset is propagated.
func richEvent() event.Event {
	return event.Event{
		ID:             "evt_123",
		Name:           "subscribed",
		UserID:         "user_abc",
		OrganizationID: "org_1",
		TenantID:       "tenant_1",
		IP:             "203.0.113.7",
		UserAgent:      "Mozilla/5.0",
		Meta:           map[string]string{"source": "api"},
		App:            event.App{Name: "checkout", Version: "1.4.0"},
		Device:         event.Device{ID: "device_9", Model: "Pixel"},
		Location:       event.Location{City: "Paris", Country: "FR"},
		Subscriptions:  []event.Subscription{{ID: "sub_001", CustomerID: "cus_001"}},
	}
}

func TestStart_DoesNotTouchBaggage(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	ctx := internaltrace.NewContext(event.NewContext(t.Context(), richEvent()), tr)

	ctx, s := Start(ctx, SpanKindClient, "outgoing-call")
	defer s.End()

	// Event attributes belong on the entry span. A child span neither re-stamps
	// them nor writes baggage.
	assert.Empty(t, baggage.FromContext(ctx).Members())
}

func TestStartEntry_PropagatesAllowListedFieldsOnly(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	ctx := internaltrace.NewContext(event.NewContext(t.Context(), richEvent()), tr)

	ctx, s := StartEntry(ctx, SpanKindServer, "incoming-request")
	defer s.End()

	b := baggage.FromContext(ctx)

	assert.Equal(t, "evt_123", b.Member("event.id").Value())
	assert.Equal(t, "subscribed", b.Member("event.name").Value())
	assert.Equal(t, "org_1", b.Member("event.organization_id").Value())
	assert.Equal(t, "tenant_1", b.Member("event.tenant_id").Value())
	assert.Equal(t, "checkout", b.Member("event.app.name").Value())
	assert.Equal(t, "1.4.0", b.Member("event.app.version").Value())
}

func TestStartEntry_WithholdsPersonalData(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	ctx := internaltrace.NewContext(event.NewContext(t.Context(), richEvent()), tr)

	ctx, s := StartEntry(ctx, SpanKindServer, "incoming-request")
	defer s.End()

	b := baggage.FromContext(ctx)

	// Baggage rides outbound headers to third parties. None of these may travel.
	for _, key := range []string{
		"event.user_id",
		"event.ip",
		"event.user_agent",
		"event.meta.source",
		"event.device.id",
		"event.device.model",
		"event.location.city",
		"event.location.country",
		"event.subscriptions.0.id",
		"event.subscriptions.0.customer_id",
	} {
		assert.Empty(t, b.Member(key).Value(), "%s must not be propagated", key)
	}
}

func TestStartEntry_MergesRatherThanReplacesUpstreamBaggage(t *testing.T) {
	tr := internaltrace.NewNopTracer()

	upstream, err := baggage.NewMember("upstream.key", "upstream-value")
	require.NoError(t, err)
	b, err := baggage.New(upstream)
	require.NoError(t, err)

	ctx := baggage.ContextWithBaggage(t.Context(), b)
	ctx = internaltrace.NewContext(event.NewContext(ctx, richEvent()), tr)

	ctx, s := StartEntry(ctx, SpanKindServer, "incoming-request")
	defer s.End()

	got := baggage.FromContext(ctx)

	assert.Equal(t, "upstream-value", got.Member("upstream.key").Value(),
		"baggage set by an upstream service must survive")
	assert.Equal(t, "subscribed", got.Member("event.name").Value())
}

func TestStartEntry_NoEventInContext(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	ctx := internaltrace.NewContext(t.Context(), tr)

	ctx, s := StartEntry(ctx, SpanKindServer, "no-event")
	defer s.End()

	assert.Empty(t, baggage.FromContext(ctx).Members())
}

func TestStartEntry_RoundTripRecoversAllowListedSubset(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	input := richEvent()

	ctx := internaltrace.NewContext(event.NewContext(t.Context(), input), tr)
	ctx, s := StartEntry(ctx, SpanKindServer, "incoming-request")
	defer s.End()

	// Simulate the far side of a process boundary: only the baggage survives.
	downstream := baggage.ContextWithBaggage(t.Context(), baggage.FromContext(ctx))
	output, ok := event.FromContext(downstream)

	require.True(t, ok)
	assert.Equal(t, input.ID, output.ID)
	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.OrganizationID, output.OrganizationID)
	assert.Equal(t, input.TenantID, output.TenantID)
	assert.Equal(t, input.App.Name, output.App.Name)
	assert.Equal(t, input.App.Version, output.App.Version)

	// Personal data does not cross the boundary.
	assert.Empty(t, output.UserID)
	assert.Empty(t, output.IP)
	assert.Empty(t, output.UserAgent)
	assert.Empty(t, output.Meta)
	assert.Empty(t, output.Subscriptions)
}

func TestStart_NoTracerInContext(t *testing.T) {
	ctx, s := Start(t.Context(), SpanKindInternal, "no-tracer")
	defer s.End()

	assert.NotNil(t, s)
	assert.NotNil(t, ctx)
}

func TestStartEntry_NoTracerInContext(t *testing.T) {
	ctx := event.NewContext(t.Context(), richEvent())

	ctx, s := StartEntry(ctx, SpanKindServer, "no-tracer")
	defer s.End()

	assert.NotNil(t, s)
	assert.NotNil(t, ctx)
}
