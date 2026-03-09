package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestToFlatMap_EmptyEvent(t *testing.T) {
	m := ToFlatMap(Event{})

	assert.NotNil(t, m)
	assert.Empty(t, m)
}

func TestToFlatMap_TopLevelStringFields(t *testing.T) {
	e := Event{
		ID:             "evt_123",
		Name:           "subscribed",
		UserID:         "user_456",
		OrganizationID: "org_789",
		TenantID:       "tenant_abc",
		IP:             "192.168.1.1",
		UserAgent:      "Mozilla/5.0",
		Locale:         "en-US",
		Timezone:       "America/New_York",
	}

	m := ToFlatMap(e)

	assert.Equal(t, "evt_123", m["event.id"])
	assert.Equal(t, "subscribed", m["event.name"])
	assert.Equal(t, "user_456", m["event.user_id"])
	assert.Equal(t, "org_789", m["event.organization_id"])
	assert.Equal(t, "tenant_abc", m["event.tenant_id"])
	assert.Equal(t, "192.168.1.1", m["event.ip"])
	assert.Equal(t, "Mozilla/5.0", m["event.user_agent"])
	assert.Equal(t, "en-US", m["event.locale"])
	assert.Equal(t, "America/New_York", m["event.timezone"])
}

func TestToFlatMap_IsAnonymous(t *testing.T) {
	t.Run("nil pointer omitted", func(t *testing.T) {
		m := ToFlatMap(Event{Name: "test"})

		assert.NotContains(t, m, "event.is_anonymous")
	})

	t.Run("true", func(t *testing.T) {
		m := ToFlatMap(Event{Name: "test", IsAnonymous: BoolPtr(true)})

		assert.Equal(t, "true", m["event.is_anonymous"])
	})

	t.Run("false", func(t *testing.T) {
		m := ToFlatMap(Event{Name: "test", IsAnonymous: BoolPtr(false)})

		assert.Equal(t, "false", m["event.is_anonymous"])
	})
}

func TestToFlatMap_Timestamp(t *testing.T) {
	t.Run("zero value omitted", func(t *testing.T) {
		m := ToFlatMap(Event{Name: "test"})

		assert.NotContains(t, m, "event.timestamp")
	})

	t.Run("set value", func(t *testing.T) {
		ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		m := ToFlatMap(Event{Name: "test", Timestamp: ts})

		assert.Equal(t, ts.Format(time.RFC3339Nano), m["event.timestamp"])
	})
}

func TestToFlatMap_Meta(t *testing.T) {
	e := Event{
		Name: "test",
		Meta: map[string]string{
			"hello": "world",
			"this":  "works",
		},
	}

	m := ToFlatMap(e)

	assert.Equal(t, "world", m["event.meta.hello"])
	assert.Equal(t, "works", m["event.meta.this"])
}

func TestToFlatMap_NestedStructs(t *testing.T) {
	e := Event{
		Name:     "full_event",
		App:      App{Name: "my-app", Version: "1.0.0", BuildID: "abc"},
		Campaign: Campaign{Name: "summer_sale", Source: "google", Medium: "cpc", Term: "shoes", Content: "banner"},
		Device:   Device{ID: "dev_1", Manufacturer: "Apple", Model: "iPhone", Name: "iPhone 15", Type: "mobile", Version: "17.0", AdvertisingID: "ad_123"},
		Location: Location{City: "Paris", Country: "FR", Region: "IDF", Latitude: 48.8566, Longitude: 2.3522},
		Network:  Network{Bluetooth: true, WIFI: true, Carrier: "Orange"},
		OS:       OS{Name: "iOS", Arch: "arm64", Version: "17.0"},
		Page:     Page{Path: "/home", Title: "Home", URL: "https://example.com"},
		Referrer: Referrer{Type: "organic", Name: "Google", URL: "https://google.com"},
		Screen:   Screen{Density: 3, Width: 1170, Height: 2532},
	}

	m := ToFlatMap(e)

	assert.Equal(t, "my-app", m["event.app.name"])
	assert.Equal(t, "1.0.0", m["event.app.version"])
	assert.Equal(t, "abc", m["event.app.build_id"])
	assert.Equal(t, "summer_sale", m["event.campaign.name"])
	assert.Equal(t, "google", m["event.campaign.source"])
	assert.Equal(t, "dev_1", m["event.device.id"])
	assert.Equal(t, "Apple", m["event.device.manufacturer"])
	assert.Equal(t, "Paris", m["event.location.city"])
	assert.Equal(t, "48.8566", m["event.location.latitude"])
	assert.Equal(t, "true", m["event.network.bluetooth"])
	assert.Equal(t, "Orange", m["event.network.carrier"])
	assert.Equal(t, "iOS", m["event.os.name"])
	assert.Equal(t, "/home", m["event.page.path"])
	assert.Equal(t, "organic", m["event.referrer.type"])
	assert.Equal(t, "3", m["event.screen.density"])
	assert.Equal(t, "1170", m["event.screen.width"])
	assert.Equal(t, "2532", m["event.screen.height"])
}

func TestToFlatMap_Subscriptions(t *testing.T) {
	e := Event{
		Name: "test",
		Subscriptions: []Subscription{
			{
				ID:          "sub_0",
				CustomerID:  "cus_0",
				ProductID:   "prod_0",
				PriceID:     "price_0",
				Usage:       "api_calls",
				IncrementBy: 1,
				Metadata:    map[string]string{"version": "a"},
			},
			{
				ID:          "sub_1",
				CustomerID:  "cus_1",
				IncrementBy: 1.25,
			},
		},
	}

	m := ToFlatMap(e)

	assert.Equal(t, "sub_0", m["event.subscriptions.0.id"])
	assert.Equal(t, "cus_0", m["event.subscriptions.0.customer_id"])
	assert.Equal(t, "prod_0", m["event.subscriptions.0.product_id"])
	assert.Equal(t, "price_0", m["event.subscriptions.0.price_id"])
	assert.Equal(t, "api_calls", m["event.subscriptions.0.usage"])
	assert.Equal(t, "1", m["event.subscriptions.0.increment_by"])
	assert.Equal(t, "a", m["event.subscriptions.0.metadata.version"])
	assert.Equal(t, "sub_1", m["event.subscriptions.1.id"])
	assert.Equal(t, "cus_1", m["event.subscriptions.1.customer_id"])
	assert.Equal(t, "1.25", m["event.subscriptions.1.increment_by"])
}

func TestToFlatMap_ZeroValuesOmitted(t *testing.T) {
	e := Event{
		Name: "test",
		Location: Location{
			Latitude: 0,
		},
		Network: Network{
			Bluetooth: false,
		},
		Screen: Screen{
			Density: 0,
		},
	}

	m := ToFlatMap(e)

	assert.NotContains(t, m, "event.location.latitude")
	assert.NotContains(t, m, "event.network.bluetooth")
	assert.NotContains(t, m, "event.screen.density")
}

func TestFromFlatMap_TopLevelFields(t *testing.T) {
	m := map[string]string{
		"event.id":              "evt_123",
		"event.name":            "subscribed",
		"event.user_id":         "user_456",
		"event.organization_id": "org_789",
		"event.tenant_id":       "tenant_abc",
		"event.ip":              "10.0.0.1",
		"event.user_agent":      "TestAgent",
		"event.locale":          "en-US",
		"event.timezone":        "UTC",
	}

	e := FromFlatMap(m)

	assert.Equal(t, "evt_123", e.ID)
	assert.Equal(t, "subscribed", e.Name)
	assert.Equal(t, "user_456", e.UserID)
	assert.Equal(t, "org_789", e.OrganizationID)
	assert.Equal(t, "tenant_abc", e.TenantID)
	assert.Equal(t, "10.0.0.1", e.IP)
	assert.Equal(t, "TestAgent", e.UserAgent)
	assert.Equal(t, "en-US", e.Locale)
	assert.Equal(t, "UTC", e.Timezone)
}

func TestFromFlatMap_IsAnonymous(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		e := FromFlatMap(map[string]string{"event.is_anonymous": "true"})

		assert.NotNil(t, e.IsAnonymous)
		assert.True(t, *e.IsAnonymous)
	})

	t.Run("false", func(t *testing.T) {
		e := FromFlatMap(map[string]string{"event.is_anonymous": "false"})

		assert.NotNil(t, e.IsAnonymous)
		assert.False(t, *e.IsAnonymous)
	})

	t.Run("absent", func(t *testing.T) {
		e := FromFlatMap(map[string]string{"event.name": "test"})

		assert.Nil(t, e.IsAnonymous)
	})
}

func TestFromFlatMap_Timestamp(t *testing.T) {
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	e := FromFlatMap(map[string]string{
		"event.timestamp": ts.Format(time.RFC3339Nano),
	})

	assert.True(t, ts.Equal(e.Timestamp))
}

func TestFromFlatMap_NestedStructs(t *testing.T) {
	m := map[string]string{
		"event.app.name":       "my-app",
		"event.app.version":    "2.0.0",
		"event.os.name":        "macOS",
		"event.os.arch":        "arm64",
		"event.os.version":     "14.0",
		"event.page.path":      "/about",
		"event.page.title":     "About",
		"event.screen.density": "3",
		"event.screen.width":   "1170",
		"event.screen.height":  "2532",
	}

	e := FromFlatMap(m)

	assert.Equal(t, App{Name: "my-app", Version: "2.0.0"}, e.App)
	assert.Equal(t, OS{Name: "macOS", Arch: "arm64", Version: "14.0"}, e.OS)
	assert.Equal(t, Page{Path: "/about", Title: "About"}, e.Page)
	assert.Equal(t, Screen{Density: 3, Width: 1170, Height: 2532}, e.Screen)
}

func TestFromFlatMap_Subscriptions(t *testing.T) {
	m := map[string]string{
		"event.subscriptions.0.id":          "sub_0",
		"event.subscriptions.0.customer_id": "cus_0",
		"event.subscriptions.1.id":          "sub_1",
		"event.subscriptions.1.customer_id": "cus_1",
	}

	e := FromFlatMap(m)

	assert.Len(t, e.Subscriptions, 2)
	assert.Equal(t, "sub_0", e.Subscriptions[0].ID)
	assert.Equal(t, "cus_0", e.Subscriptions[0].CustomerID)
	assert.Equal(t, "sub_1", e.Subscriptions[1].ID)
	assert.Equal(t, "cus_1", e.Subscriptions[1].CustomerID)
}

func TestFromFlatMap_Meta(t *testing.T) {
	m := map[string]string{
		"event.meta.source": "web",
		"event.meta.env":    "production",
	}

	e := FromFlatMap(m)

	assert.Equal(t, map[string]string{
		"source": "web",
		"env":    "production",
	}, e.Meta)
}

func TestFlatMapRoundTrip(t *testing.T) {
	ts := time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC)
	input := Event{
		ID:             "evt_rt",
		Name:           "page_view",
		UserID:         "user_abc",
		OrganizationID: "org_def",
		TenantID:       "tenant_ghi",
		IP:             "10.0.0.1",
		UserAgent:      "TestAgent/1.0",
		Locale:         "fr-FR",
		Timezone:       "Europe/Paris",
		Timestamp:      ts,
		IsAnonymous:    BoolPtr(true),
		Meta:           map[string]string{"key": "value"},
		App:            App{Name: "my-app", Version: "2.0.0", BuildID: "abc"},
		Campaign:       Campaign{Name: "summer", Source: "google"},
		Device:         Device{ID: "dev_1", Manufacturer: "Apple", Model: "iPhone"},
		Location:       Location{City: "Paris", Country: "FR", Latitude: 48.8566},
		Network:        Network{Bluetooth: true, WIFI: true, Carrier: "Orange"},
		OS:             OS{Name: "macOS", Arch: "arm64", Version: "14.0"},
		Page:           Page{Path: "/about", Title: "About"},
		Referrer:       Referrer{Type: "organic", Name: "Google"},
		Screen:         Screen{Density: 3, Width: 1170, Height: 2532},
		Subscriptions: []Subscription{
			{ID: "sub_0", CustomerID: "cus_0", ProductID: "prod_0"},
			{ID: "sub_1", CustomerID: "cus_1"},
		},
	}

	m := ToFlatMap(input)
	output := FromFlatMap(m)

	assert.Equal(t, input, output)
}

func TestFlatMapRoundTrip_EmptyEvent(t *testing.T) {
	m := ToFlatMap(Event{})
	output := FromFlatMap(m)

	assert.Equal(t, Event{}, output)
}

func TestFromFlatMap_IgnoresNonEventKeys(t *testing.T) {
	m := map[string]string{
		"other.key":      "value",
		"something.else": "data",
		"event.name":     "test",
	}

	e := FromFlatMap(m)

	assert.Equal(t, "test", e.Name)
}

func TestFromFlatMap_SubscriptionMetadata(t *testing.T) {
	m := map[string]string{
		"event.subscriptions.0.id":               "sub_0",
		"event.subscriptions.0.metadata.version": "a",
		"event.subscriptions.0.metadata.env":     "prod",
	}

	e := FromFlatMap(m)

	assert.Len(t, e.Subscriptions, 1)
	assert.Equal(t, "sub_0", e.Subscriptions[0].ID)
	assert.Equal(t, map[string]string{
		"version": "a",
		"env":     "prod",
	}, e.Subscriptions[0].Metadata)
}

func TestFromFlatMap_LocationFloats(t *testing.T) {
	m := map[string]string{
		"event.location.latitude":  "48.8566",
		"event.location.longitude": "2.3522",
		"event.location.speed":     "50",
	}

	e := FromFlatMap(m)

	assert.Equal(t, 48.8566, e.Location.Latitude)
	assert.Equal(t, 2.3522, e.Location.Longitude)
	assert.Equal(t, float64(50), e.Location.Speed)
}

func TestFromFlatMap_NetworkBools(t *testing.T) {
	m := map[string]string{
		"event.network.bluetooth": "true",
		"event.network.cellular":  "false",
		"event.network.wifi":      "true",
	}

	e := FromFlatMap(m)

	assert.True(t, e.Network.Bluetooth)
	assert.False(t, e.Network.Cellular)
	assert.True(t, e.Network.WIFI)
}

func TestFromFlatMap_SparseSubscriptionIndices(t *testing.T) {
	m := map[string]string{
		"event.subscriptions.0.id": "sub_0",
		"event.subscriptions.2.id": "sub_2",
	}

	e := FromFlatMap(m)

	// Slice length is maxIdx+1, so index 1 is a zero-value Subscription.
	assert.Len(t, e.Subscriptions, 3)
	assert.Equal(t, "sub_0", e.Subscriptions[0].ID)
	assert.Equal(t, "", e.Subscriptions[1].ID)
	assert.Equal(t, "sub_2", e.Subscriptions[2].ID)
}

func TestFromFlatMap_InvalidSubscriptionIndex(t *testing.T) {
	m := map[string]string{
		"event.subscriptions.abc.id": "sub_bad",
		"event.subscriptions.0.id":   "sub_0",
	}

	e := FromFlatMap(m)

	// Non-numeric index is silently skipped; only index 0 is populated.
	assert.Len(t, e.Subscriptions, 1)
	assert.Equal(t, "sub_0", e.Subscriptions[0].ID)
}
