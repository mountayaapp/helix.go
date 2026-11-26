package event

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
)

// contextWithPropagatedEvent puts the Event's propagated members in the context's
// baggage, the way the server integrations do before an outbound call.
func contextWithPropagatedEvent(t *testing.T, ctx context.Context, e Event) context.Context {
	t.Helper()

	members, err := e.Propagated()
	require.NoError(t, err)

	b, err := baggage.New(members...)
	require.NoError(t, err)

	return baggage.ContextWithBaggage(ctx, b)
}

// contextWithFullFlatMap puts every tagged field of the Event in the context's
// baggage — what a client that does not follow the allow-list could send.
func contextWithFullFlatMap(t *testing.T, ctx context.Context, e Event) context.Context {
	t.Helper()

	var members []baggage.Member
	for k, v := range ToFlatMap(e) {
		m, err := baggage.NewMemberRaw(k, v)
		require.NoError(t, err)
		members = append(members, m)
	}

	b, err := baggage.New(members...)
	require.NoError(t, err)

	return baggage.ContextWithBaggage(ctx, b)
}

func TestEventPropagation_ViaBaggageHTTPHeaders(t *testing.T) {
	input := Event{
		ID:             "evt_1",
		Name:           "subscribed",
		OrganizationID: "org_1",
		TenantID:       "tenant_1",
		App:            App{Name: "my-app", Version: "3.0.0"},
	}

	propagator := propagation.Baggage{}
	headers := http.Header{}
	propagator.Inject(contextWithPropagatedEvent(t, t.Context(), input), propagation.HeaderCarrier(headers))

	assert.NotEmpty(t, headers.Get("Baggage"))

	output, ok := FromContext(propagator.Extract(t.Context(), propagation.HeaderCarrier(headers)))

	assert.True(t, ok)
	assert.Equal(t, input, output)
}

func TestEventPropagation_ValuesNeedingEncodingRoundTrip(t *testing.T) {
	input := Event{
		Name: "user signed up, again",
		App:  App{Name: "My App ✓", Version: "100%25 ready"},
	}

	propagator := propagation.Baggage{}
	headers := http.Header{}
	propagator.Inject(contextWithPropagatedEvent(t, t.Context(), input), propagation.HeaderCarrier(headers))

	output, ok := FromContext(propagator.Extract(t.Context(), propagation.HeaderCarrier(headers)))

	assert.True(t, ok)
	assert.Equal(t, input, output)
}

func TestEventPropagation_InboundDecodesAllowListOnly(t *testing.T) {
	input := Event{
		ID:             "evt_full",
		Name:           "checkout",
		UserID:         "user_xyz",
		OrganizationID: "org_xyz",
		TenantID:       "tenant_xyz",
		IP:             "10.0.0.1",
		UserAgent:      "TestAgent/1.0",
		Locale:         "fr-FR",
		IsAnonymous:    new(true),
		Meta:           map[string]string{"env": "production"},
		App:            App{Name: "my-app", Version: "3.0.0", BuildID: "abc123"},
		Device:         Device{ID: "dev_1"},
		Subscriptions:  []Subscription{{ID: "sub_001", CustomerID: "cus_001"}},
	}

	propagator := propagation.Baggage{}
	headers := http.Header{}
	propagator.Inject(contextWithFullFlatMap(t, t.Context(), input), propagation.HeaderCarrier(headers))

	output, ok := FromContext(propagator.Extract(t.Context(), propagation.HeaderCarrier(headers)))

	assert.True(t, ok)
	assert.Equal(t, Event{
		ID:             "evt_full",
		Name:           "checkout",
		OrganizationID: "org_xyz",
		TenantID:       "tenant_xyz",
		App:            App{Name: "my-app", Version: "3.0.0"},
	}, output)
}

func TestEventPropagation_HugeIndexDoesNotAllocate(t *testing.T) {
	headers := http.Header{}
	headers.Set("Baggage", "event.name=x,event.subscriptions.999999999.id=y")

	output, ok := FromContext(propagation.Baggage{}.Extract(t.Context(), propagation.HeaderCarrier(headers)))

	assert.True(t, ok)
	assert.Equal(t, "x", output.Name)
	assert.Nil(t, output.Subscriptions)
}

func TestEventPropagation_CompositeTextMapPropagator(t *testing.T) {
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.Baggage{},
		propagation.TraceContext{},
	)

	input := Event{
		Name: "test_composite",
		App:  App{Name: "helix", Version: "1.0.0"},
	}

	headers := http.Header{}
	propagator.Inject(contextWithPropagatedEvent(t, t.Context(), input), propagation.HeaderCarrier(headers))

	assert.NotEmpty(t, headers.Get("Baggage"))

	output, ok := FromContext(propagator.Extract(t.Context(), propagation.HeaderCarrier(headers)))

	assert.True(t, ok)
	assert.Equal(t, input, output)
}

func TestEventPropagation_EmptyEventNotPropagated(t *testing.T) {
	input := Event{TenantID: "tenant_no_name"}

	propagator := propagation.Baggage{}
	headers := http.Header{}
	propagator.Inject(contextWithPropagatedEvent(t, t.Context(), input), propagation.HeaderCarrier(headers))

	_, ok := FromContext(propagator.Extract(t.Context(), propagation.HeaderCarrier(headers)))

	assert.False(t, ok)
}

func TestEventPropagation_MultiHopPropagation(t *testing.T) {
	input := Event{
		Name:     "multi_hop",
		TenantID: "tenant_hop",
	}

	propagator := propagation.Baggage{}

	// Hop A → B.
	headersAB := http.Header{}
	propagator.Inject(contextWithPropagatedEvent(t, t.Context(), input), propagation.HeaderCarrier(headersAB))

	// Hop B → C.
	ctxB := propagator.Extract(t.Context(), propagation.HeaderCarrier(headersAB))
	headersBC := http.Header{}
	propagator.Inject(ctxB, propagation.HeaderCarrier(headersBC))

	output, ok := FromContext(propagator.Extract(t.Context(), propagation.HeaderCarrier(headersBC)))

	assert.True(t, ok)
	assert.Equal(t, input, output)
}
