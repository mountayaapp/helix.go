package trace

import (
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/event"
	internaltrace "github.com/mountayaapp/helix.go/internal/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
)

func TestStart_PopulatesBaggageFromEvent(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	e := event.Event{
		Name:   "subscribed",
		UserID: "user_123",
		Meta: map[string]string{
			"source": "web",
		},
	}

	ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
	ctx, s := Start(ctx, SpanKindInternal, "test-operation")
	defer s.End()

	b := baggage.FromContext(ctx)
	assert.Equal(t, "subscribed", b.Member("event.name").Value())
	assert.Equal(t, "user_123", b.Member("event.user_id").Value())
	assert.Equal(t, "web", b.Member("event.meta.source").Value())
}

func TestStart_BaggageContainsAllTopLevelFields(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	e := event.Event{
		ID:             "evt_001",
		Name:           "page_view",
		UserID:         "user_456",
		OrganizationID: "org_789",
		TenantID:       "tenant_abc",
		IP:             "192.168.1.1",
		UserAgent:      "Mozilla/5.0",
		Locale:         "en-US",
		Timezone:       "America/New_York",
		Timestamp:      ts,
		IsAnonymous:    event.BoolPtr(true),
	}

	ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
	ctx, s := Start(ctx, SpanKindServer, "http-request")
	defer s.End()

	b := baggage.FromContext(ctx)
	assert.Equal(t, "evt_001", b.Member("event.id").Value())
	assert.Equal(t, "page_view", b.Member("event.name").Value())
	assert.Equal(t, "user_456", b.Member("event.user_id").Value())
	assert.Equal(t, "org_789", b.Member("event.organization_id").Value())
	assert.Equal(t, "tenant_abc", b.Member("event.tenant_id").Value())
	assert.Equal(t, "192.168.1.1", b.Member("event.ip").Value())
	assert.Equal(t, "Mozilla/5.0", b.Member("event.user_agent").Value())
	assert.Equal(t, "en-US", b.Member("event.locale").Value())
	assert.Equal(t, "America/New_York", b.Member("event.timezone").Value())
	assert.Equal(t, ts.Format(time.RFC3339Nano), b.Member("event.timestamp").Value())
	assert.Equal(t, "true", b.Member("event.is_anonymous").Value())
}

func TestStart_BaggageContainsNestedObjects(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	e := event.Event{
		Name: "checkout",
		App:  event.App{Name: "my-app", Version: "2.0.0"},
		OS:   event.OS{Name: "iOS", Arch: "arm64", Version: "17.0"},
		Page: event.Page{Path: "/checkout", Title: "Checkout"},
	}

	ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
	ctx, s := Start(ctx, SpanKindInternal, "checkout-flow")
	defer s.End()

	b := baggage.FromContext(ctx)
	assert.Equal(t, "my-app", b.Member("event.app.name").Value())
	assert.Equal(t, "2.0.0", b.Member("event.app.version").Value())
	assert.Equal(t, "iOS", b.Member("event.os.name").Value())
	assert.Equal(t, "arm64", b.Member("event.os.arch").Value())
	assert.Equal(t, "/checkout", b.Member("event.page.path").Value())
}

func TestStart_BaggageContainsSubscriptions(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	e := event.Event{
		Name: "subscribed",
		Subscriptions: []event.Subscription{
			{
				ID:         "sub_001",
				CustomerID: "cus_001",
				ProductID:  "prod_001",
			},
		},
	}

	ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
	ctx, s := Start(ctx, SpanKindInternal, "subscribe")
	defer s.End()

	b := baggage.FromContext(ctx)
	assert.Equal(t, "sub_001", b.Member("event.subscriptions.0.id").Value())
	assert.Equal(t, "cus_001", b.Member("event.subscriptions.0.customer_id").Value())
	assert.Equal(t, "prod_001", b.Member("event.subscriptions.0.product_id").Value())
}

func TestStart_NoEventInContext(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	ctx := internaltrace.ContextWithTracer(t.Context(), tr)
	ctx, s := Start(ctx, SpanKindInternal, "no-event")
	defer s.End()

	b := baggage.FromContext(ctx)
	assert.Empty(t, b.Members())
}

func TestStart_EventSurvivesRoundTripViaBaggage(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	input := event.Event{
		Name:   "subscribed",
		UserID: "user_abc",
		Meta: map[string]string{
			"source": "api",
		},
		Subscriptions: []event.Subscription{
			{ID: "sub_001", CustomerID: "cus_001"},
		},
	}

	ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), input), tr)
	ctx, s := Start(ctx, SpanKindClient, "outgoing-call")
	defer s.End()

	b := baggage.FromContext(ctx)
	ctxB := baggage.ContextWithBaggage(t.Context(), b)
	output, ok := event.EventFromContext(ctxB)

	assert.True(t, ok)
	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.UserID, output.UserID)
	assert.Equal(t, input.Meta, output.Meta)
	assert.Len(t, output.Subscriptions, 1)
	assert.Equal(t, "sub_001", output.Subscriptions[0].ID)
	assert.Equal(t, "cus_001", output.Subscriptions[0].CustomerID)
}

func TestStart_FullEventRoundTrip(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	input := event.Event{
		ID:             "evt_full",
		Name:           "full_event",
		UserID:         "user_full",
		OrganizationID: "org_full",
		TenantID:       "tenant_full",
		IP:             "10.0.0.1",
		UserAgent:      "TestAgent/2.0",
		Locale:         "fr-FR",
		Timezone:       "Europe/Paris",
		Timestamp:      ts,
		IsAnonymous:    event.BoolPtr(true),
		Meta: map[string]string{
			"env": "test",
		},
		App:      event.App{Name: "helix", Version: "1.0.0", BuildID: "build_123"},
		Campaign: event.Campaign{Name: "launch", Source: "email", Medium: "newsletter"},
		Device:   event.Device{ID: "dev_1", Type: "mobile", Manufacturer: "Apple"},
		Location: event.Location{City: "Paris", Country: "FR", Latitude: 48.8566, Longitude: 2.3522},
		Network:  event.Network{WIFI: true, Carrier: "Orange"},
		OS:       event.OS{Name: "iOS", Arch: "arm64", Version: "17.0"},
		Page:     event.Page{Path: "/home", Title: "Home", URL: "https://example.com"},
		Referrer: event.Referrer{Type: "organic", Name: "Google"},
		Screen:   event.Screen{Density: 3, Width: 1170, Height: 2532},
		Subscriptions: []event.Subscription{
			{ID: "sub_001", CustomerID: "cus_001", ProductID: "prod_001"},
			{ID: "sub_002", CustomerID: "cus_002", ProductID: "prod_002"},
		},
	}

	ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), input), tr)
	ctx, s := Start(ctx, SpanKindClient, "service-a")
	defer s.End()

	b := baggage.FromContext(ctx)
	ctxB := baggage.ContextWithBaggage(t.Context(), b)
	output, ok := event.EventFromContext(ctxB)

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
	assert.Equal(t, input.Device.Type, output.Device.Type)
	assert.Equal(t, input.Device.Manufacturer, output.Device.Manufacturer)
	assert.Equal(t, input.Location.City, output.Location.City)
	assert.Equal(t, input.Location.Country, output.Location.Country)
	assert.Equal(t, input.Network.WIFI, output.Network.WIFI)
	assert.Equal(t, input.Network.Carrier, output.Network.Carrier)
	assert.Equal(t, input.OS, output.OS)
	assert.Equal(t, input.Page, output.Page)
	assert.Equal(t, input.Referrer.Type, output.Referrer.Type)
	assert.Equal(t, input.Referrer.Name, output.Referrer.Name)
	assert.Equal(t, input.Screen, output.Screen)
	assert.Len(t, output.Subscriptions, 2)
	assert.Equal(t, "sub_001", output.Subscriptions[0].ID)
	assert.Equal(t, "sub_002", output.Subscriptions[1].ID)
}

func TestStart_NoTracerInContext(t *testing.T) {
	ctx, s := Start(t.Context(), SpanKindInternal, "no-tracer")
	assert.NotNil(t, s)
	assert.NotNil(t, ctx)

	assert.NotPanics(t, func() {
		s.End()
	})
}

func TestStart_BaggageContainsNestedFields(t *testing.T) {
	tr := internaltrace.NewNopTracer()

	t.Run("Campaign", func(t *testing.T) {
		e := event.Event{
			Name:     "click",
			Campaign: event.Campaign{Name: "launch", Source: "email", Medium: "newsletter", Term: "signup", Content: "banner"},
		}

		ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
		ctx, s := Start(ctx, SpanKindInternal, "campaign-test")
		defer s.End()

		b := baggage.FromContext(ctx)
		assert.Equal(t, "launch", b.Member("event.campaign.name").Value())
		assert.Equal(t, "email", b.Member("event.campaign.source").Value())
		assert.Equal(t, "newsletter", b.Member("event.campaign.medium").Value())
		assert.Equal(t, "signup", b.Member("event.campaign.term").Value())
		assert.Equal(t, "banner", b.Member("event.campaign.content").Value())
	})

	t.Run("Device", func(t *testing.T) {
		e := event.Event{
			Name:   "view",
			Device: event.Device{ID: "dev_001", Manufacturer: "Apple", Model: "iPhone15", Name: "MyiPhone", Type: "mobile", Version: "1.0"},
		}

		ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
		ctx, s := Start(ctx, SpanKindInternal, "device-test")
		defer s.End()

		b := baggage.FromContext(ctx)
		assert.Equal(t, "dev_001", b.Member("event.device.id").Value())
		assert.Equal(t, "Apple", b.Member("event.device.manufacturer").Value())
		assert.Equal(t, "iPhone15", b.Member("event.device.model").Value())
		assert.Equal(t, "MyiPhone", b.Member("event.device.name").Value())
		assert.Equal(t, "mobile", b.Member("event.device.type").Value())
		assert.Equal(t, "1.0", b.Member("event.device.version").Value())
	})

	t.Run("Referrer", func(t *testing.T) {
		e := event.Event{
			Name:     "visit",
			Referrer: event.Referrer{Type: "organic", Name: "Google", URL: "https://google.com", Link: "https://google.com/search"},
		}

		ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
		ctx, s := Start(ctx, SpanKindInternal, "referrer-test")
		defer s.End()

		b := baggage.FromContext(ctx)
		assert.Equal(t, "organic", b.Member("event.referrer.type").Value())
		assert.Equal(t, "Google", b.Member("event.referrer.name").Value())
		assert.Equal(t, "https://google.com", b.Member("event.referrer.url").Value())
		assert.Equal(t, "https://google.com/search", b.Member("event.referrer.link").Value())
	})

	t.Run("Location", func(t *testing.T) {
		e := event.Event{
			Name:     "checkin",
			Location: event.Location{City: "Paris", Country: "FR", Region: "IDF", Latitude: 48.8566, Longitude: 2.3522, Speed: 1.5},
		}

		ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
		ctx, s := Start(ctx, SpanKindInternal, "location-test")
		defer s.End()

		b := baggage.FromContext(ctx)
		assert.Equal(t, "Paris", b.Member("event.location.city").Value())
		assert.Equal(t, "FR", b.Member("event.location.country").Value())
		assert.Equal(t, "IDF", b.Member("event.location.region").Value())
		assert.Equal(t, "48.8566", b.Member("event.location.latitude").Value())
		assert.Equal(t, "2.3522", b.Member("event.location.longitude").Value())
		assert.Equal(t, "1.5", b.Member("event.location.speed").Value())
	})

	t.Run("Network", func(t *testing.T) {
		e := event.Event{
			Name:    "sync",
			Network: event.Network{Bluetooth: true, Cellular: true, WIFI: true, Carrier: "Orange"},
		}

		ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
		ctx, s := Start(ctx, SpanKindInternal, "network-test")
		defer s.End()

		b := baggage.FromContext(ctx)
		assert.Equal(t, "true", b.Member("event.network.bluetooth").Value())
		assert.Equal(t, "true", b.Member("event.network.cellular").Value())
		assert.Equal(t, "true", b.Member("event.network.wifi").Value())
		assert.Equal(t, "Orange", b.Member("event.network.carrier").Value())
	})

	t.Run("Meta", func(t *testing.T) {
		e := event.Event{
			Name: "action",
			Meta: map[string]string{
				"source":  "web",
				"version": "2.0",
				"env":     "production",
			},
		}

		ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
		ctx, s := Start(ctx, SpanKindInternal, "meta-test")
		defer s.End()

		b := baggage.FromContext(ctx)
		assert.Equal(t, "web", b.Member("event.meta.source").Value())
		assert.Equal(t, "2.0", b.Member("event.meta.version").Value())
		assert.Equal(t, "production", b.Member("event.meta.env").Value())
	})

	t.Run("IsAnonymous/true", func(t *testing.T) {
		e := event.Event{
			Name:        "view",
			IsAnonymous: event.BoolPtr(true),
		}

		ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
		ctx, s := Start(ctx, SpanKindInternal, "anon-true")
		defer s.End()

		b := baggage.FromContext(ctx)
		assert.Equal(t, "true", b.Member("event.is_anonymous").Value())
	})

	t.Run("IsAnonymous/false", func(t *testing.T) {
		e := event.Event{
			Name:        "view",
			IsAnonymous: event.BoolPtr(false),
		}

		ctx := internaltrace.ContextWithTracer(event.ContextWithEvent(t.Context(), e), tr)
		ctx, s := Start(ctx, SpanKindInternal, "anon-false")
		defer s.End()

		b := baggage.FromContext(ctx)
		assert.Equal(t, "false", b.Member("event.is_anonymous").Value())
	})
}
