package event

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
)

func contextWithEventBaggage(ctx context.Context, e Event) context.Context {
	flat := ToFlatMap(e)
	var members []baggage.Member
	for k, v := range flat {
		m, err := baggage.NewMember(k, v)
		if err == nil {
			members = append(members, m)
		}
	}

	b, _ := baggage.New(members...)
	return baggage.ContextWithBaggage(ctx, b)
}

func TestEventPropagation_ViaBaggageHTTPHeaders(t *testing.T) {
	input := Event{
		Name:   "subscribed",
		UserID: "user_abc",
		Meta:   map[string]string{"source": "web"},
		Subscriptions: []Subscription{
			{ID: "sub_001", CustomerID: "cus_001"},
		},
	}

	propagator := propagation.Baggage{}
	headers := http.Header{}
	propagator.Inject(contextWithEventBaggage(t.Context(), input), propagation.HeaderCarrier(headers))

	assert.NotEmpty(t, headers.Get("Baggage"))

	ctxReceived := propagator.Extract(t.Context(), propagation.HeaderCarrier(headers))
	output, ok := EventFromContext(ctxReceived)

	assert.True(t, ok)
	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.UserID, output.UserID)
	assert.Equal(t, input.Meta, output.Meta)
	assert.Len(t, output.Subscriptions, 1)
	assert.Equal(t, "sub_001", output.Subscriptions[0].ID)
	assert.Equal(t, "cus_001", output.Subscriptions[0].CustomerID)
}

func TestEventPropagation_FullEventViaBaggageHTTPHeaders(t *testing.T) {
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	input := Event{
		ID:             "evt_full",
		Name:           "checkout",
		UserID:         "user_xyz",
		OrganizationID: "org_xyz",
		TenantID:       "tenant_xyz",
		IP:             "10.0.0.1",
		UserAgent:      "TestAgent/1.0",
		Locale:         "fr-FR",
		Timezone:       "Europe/Paris",
		Timestamp:      ts,
		IsAnonymous:    BoolPtr(true),
		Meta:           map[string]string{"env": "production", "region": "eu-west-1"},
		App:            App{Name: "my-app", Version: "3.0.0", BuildID: "abc123"},
		Campaign:       Campaign{Name: "summer", Source: "google", Medium: "cpc", Term: "shoes", Content: "banner"},
		Device:         Device{ID: "dev_1", Manufacturer: "Apple", Model: "iPhone", Type: "mobile"},
		OS:             OS{Name: "iOS", Arch: "arm64", Version: "17.0"},
		Page:           Page{Path: "/checkout", Title: "Checkout", URL: "https://example.com/checkout"},
		Referrer:       Referrer{Type: "organic", Name: "Google", URL: "https://google.com"},
		Screen:         Screen{Density: 3, Width: 1170, Height: 2532},
		Subscriptions: []Subscription{
			{ID: "sub_001", CustomerID: "cus_001", ProductID: "prod_001", PriceID: "price_001"},
			{ID: "sub_002", CustomerID: "cus_002", ProductID: "prod_002", PriceID: "price_002"},
		},
	}

	propagator := propagation.Baggage{}
	headers := http.Header{}
	propagator.Inject(contextWithEventBaggage(t.Context(), input), propagation.HeaderCarrier(headers))

	ctxReceived := propagator.Extract(t.Context(), propagation.HeaderCarrier(headers))
	output, ok := EventFromContext(ctxReceived)

	assert.True(t, ok)
	assert.Equal(t, input.ID, output.ID)
	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.UserID, output.UserID)
	assert.Equal(t, input.OrganizationID, output.OrganizationID)
	assert.Equal(t, input.TenantID, output.TenantID)
	assert.Equal(t, input.IP, output.IP)
	assert.Equal(t, input.UserAgent, output.UserAgent)
	assert.Equal(t, input.Locale, output.Locale)
	assert.Equal(t, input.Timezone, output.Timezone)
	assert.True(t, input.Timestamp.Equal(output.Timestamp))
	assert.NotNil(t, output.IsAnonymous)
	assert.Equal(t, *input.IsAnonymous, *output.IsAnonymous)
	assert.Equal(t, input.Meta, output.Meta)
	assert.Equal(t, input.App, output.App)
	assert.Equal(t, input.Campaign, output.Campaign)
	assert.Equal(t, input.Device.ID, output.Device.ID)
	assert.Equal(t, input.Device.Manufacturer, output.Device.Manufacturer)
	assert.Equal(t, input.Device.Model, output.Device.Model)
	assert.Equal(t, input.Device.Type, output.Device.Type)
	assert.Equal(t, input.OS, output.OS)
	assert.Equal(t, input.Page, output.Page)
	assert.Equal(t, input.Referrer, output.Referrer)
	assert.Equal(t, input.Screen, output.Screen)
	assert.Len(t, output.Subscriptions, 2)
	assert.Equal(t, "sub_001", output.Subscriptions[0].ID)
	assert.Equal(t, "cus_001", output.Subscriptions[0].CustomerID)
	assert.Equal(t, "prod_001", output.Subscriptions[0].ProductID)
	assert.Equal(t, "price_001", output.Subscriptions[0].PriceID)
	assert.Equal(t, "sub_002", output.Subscriptions[1].ID)
}

func TestEventPropagation_CompositeTextMapPropagator(t *testing.T) {
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.Baggage{},
		propagation.TraceContext{},
	)

	input := Event{
		Name:   "test_composite",
		UserID: "user_composite",
		App:    App{Name: "helix", Version: "1.0.0"},
	}

	headers := http.Header{}
	propagator.Inject(contextWithEventBaggage(t.Context(), input), propagation.HeaderCarrier(headers))

	assert.NotEmpty(t, headers.Get("Baggage"))

	ctxReceived := propagator.Extract(t.Context(), propagation.HeaderCarrier(headers))
	output, ok := EventFromContext(ctxReceived)

	assert.True(t, ok)
	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.UserID, output.UserID)
	assert.Equal(t, input.App, output.App)
}

func TestEventPropagation_EmptyEventNotPropagated(t *testing.T) {
	input := Event{UserID: "user_no_name"}

	propagator := propagation.Baggage{}
	headers := http.Header{}
	propagator.Inject(contextWithEventBaggage(t.Context(), input), propagation.HeaderCarrier(headers))

	ctxReceived := propagator.Extract(t.Context(), propagation.HeaderCarrier(headers))
	_, ok := EventFromContext(ctxReceived)

	assert.False(t, ok)
}

func TestEventPropagation_MultiHopPropagation(t *testing.T) {
	input := Event{
		Name:   "multi_hop",
		UserID: "user_hop",
		Meta:   map[string]string{"trace": "multi"},
	}

	propagator := propagation.Baggage{}

	// Hop A → B.
	headersAB := http.Header{}
	propagator.Inject(contextWithEventBaggage(t.Context(), input), propagation.HeaderCarrier(headersAB))

	// Hop B → C.
	ctxB := propagator.Extract(t.Context(), propagation.HeaderCarrier(headersAB))
	headersBC := http.Header{}
	propagator.Inject(ctxB, propagation.HeaderCarrier(headersBC))

	ctxC := propagator.Extract(t.Context(), propagation.HeaderCarrier(headersBC))
	output, ok := EventFromContext(ctxC)

	assert.True(t, ok)
	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.UserID, output.UserID)
	assert.Equal(t, input.Meta, output.Meta)
}
