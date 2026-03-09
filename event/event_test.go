package event

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBoolPtr(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		p := BoolPtr(true)
		assert.NotNil(t, p)
		assert.True(t, *p)
	})

	t.Run("false", func(t *testing.T) {
		p := BoolPtr(false)
		assert.NotNil(t, p)
		assert.False(t, *p)
	})

	t.Run("returns different pointers", func(t *testing.T) {
		p1 := BoolPtr(true)
		p2 := BoolPtr(true)
		assert.NotSame(t, p1, p2)
		assert.Equal(t, *p1, *p2)
	})
}

func TestEvent_ZeroValue(t *testing.T) {
	var e Event

	assert.Empty(t, e.ID)
	assert.Empty(t, e.Name)
	assert.Nil(t, e.Meta)
	assert.Nil(t, e.IsAnonymous)
	assert.Empty(t, e.UserID)
	assert.Empty(t, e.OrganizationID)
	assert.Empty(t, e.TenantID)
	assert.Empty(t, e.IP)
	assert.Empty(t, e.UserAgent)
	assert.Empty(t, e.Locale)
	assert.Empty(t, e.Timezone)
	assert.True(t, e.Timestamp.IsZero())
	assert.Equal(t, App{}, e.App)
	assert.Equal(t, Campaign{}, e.Campaign)
	assert.Equal(t, Device{}, e.Device)
	assert.Equal(t, Location{}, e.Location)
	assert.Equal(t, Network{}, e.Network)
	assert.Equal(t, OS{}, e.OS)
	assert.Equal(t, Page{}, e.Page)
	assert.Equal(t, Referrer{}, e.Referrer)
	assert.Equal(t, Screen{}, e.Screen)
	assert.Nil(t, e.Subscriptions)
}

func TestEvent_JSONMarshal_EmptyEvent(t *testing.T) {
	b, err := json.Marshal(Event{})

	assert.NoError(t, err)
	assert.JSONEq(t, `{}`, string(b))
}

func TestEvent_JSONMarshal_OmitsZeroValues(t *testing.T) {
	b, err := json.Marshal(Event{Name: "minimal"})

	assert.NoError(t, err)

	var unmarshaled map[string]any
	err = json.Unmarshal(b, &unmarshaled)
	assert.NoError(t, err)

	assert.Equal(t, "minimal", unmarshaled["name"])

	omitted := []string{
		"id", "meta", "is_anonymous", "user_id", "organization_id",
		"tenant_id", "ip", "user_agent", "locale", "timezone", "timestamp",
		"app", "campaign", "device", "location", "network", "os",
		"page", "referrer", "screen", "subscriptions",
	}
	for _, key := range omitted {
		assert.NotContains(t, unmarshaled, key)
	}
}

func TestEvent_JSONRoundTrip(t *testing.T) {
	original := Event{
		ID:             "evt_rt",
		Name:           "round_trip",
		Meta:           map[string]string{"key": "value"},
		IsAnonymous:    BoolPtr(true),
		UserID:         "user_rt",
		OrganizationID: "org_rt",
		TenantID:       "tenant_rt",
		IP:             "10.0.0.1",
		UserAgent:      "RoundTrip/1.0",
		Locale:         "ja-JP",
		Timezone:       "Asia/Tokyo",
		Timestamp:      time.Date(2025, 12, 25, 0, 0, 0, 0, time.UTC),
		App:            App{Name: "rt-app", Version: "1.0.0", BuildID: "build_rt"},
		Campaign:       Campaign{Name: "winter", Source: "email"},
		Device:         Device{ID: "dev_rt", Type: "tablet"},
		Location:       Location{City: "Tokyo", Country: "Japan", Latitude: 35.6762, Longitude: 139.6503},
		Network:        Network{WIFI: true, Carrier: "SoftBank"},
		OS:             OS{Name: "iPadOS", Version: "17.0"},
		Page:           Page{Path: "/home", Title: "Home", URL: "https://example.jp/home"},
		Referrer:       Referrer{Type: "direct"},
		Screen:         Screen{Density: 2, Width: 2048, Height: 2732},
		Subscriptions: []Subscription{
			{
				ID:          "sub_rt",
				CustomerID:  "cus_rt",
				ProductID:   "prod_rt",
				IncrementBy: 3.14,
				Metadata:    map[string]string{"env": "test"},
			},
		},
	}

	b, err := json.Marshal(original)
	assert.NoError(t, err)

	var restored Event
	err = json.Unmarshal(b, &restored)
	assert.NoError(t, err)

	assert.Equal(t, original.ID, restored.ID)
	assert.Equal(t, original.Name, restored.Name)
	assert.Equal(t, original.Meta, restored.Meta)
	assert.NotNil(t, restored.IsAnonymous)
	assert.Equal(t, *original.IsAnonymous, *restored.IsAnonymous)
	assert.Equal(t, original.UserID, restored.UserID)
	assert.Equal(t, original.OrganizationID, restored.OrganizationID)
	assert.Equal(t, original.TenantID, restored.TenantID)
	assert.Equal(t, original.IP, restored.IP)
	assert.Equal(t, original.UserAgent, restored.UserAgent)
	assert.Equal(t, original.Locale, restored.Locale)
	assert.Equal(t, original.Timezone, restored.Timezone)
	assert.True(t, original.Timestamp.Equal(restored.Timestamp))
	assert.Equal(t, original.App, restored.App)
	assert.Equal(t, original.Campaign, restored.Campaign)
	assert.Equal(t, original.Device, restored.Device)
	assert.Equal(t, original.Location, restored.Location)
	assert.Equal(t, original.Network, restored.Network)
	assert.Equal(t, original.OS, restored.OS)
	assert.Equal(t, original.Page, restored.Page)
	assert.Equal(t, original.Referrer, restored.Referrer)
	assert.Equal(t, original.Screen, restored.Screen)
	assert.Len(t, restored.Subscriptions, 1)
	assert.Equal(t, original.Subscriptions[0].ID, restored.Subscriptions[0].ID)
	assert.Equal(t, original.Subscriptions[0].CustomerID, restored.Subscriptions[0].CustomerID)
	assert.Equal(t, original.Subscriptions[0].ProductID, restored.Subscriptions[0].ProductID)
	assert.InDelta(t, original.Subscriptions[0].IncrementBy, restored.Subscriptions[0].IncrementBy, 0.001)
	assert.Equal(t, original.Subscriptions[0].Metadata, restored.Subscriptions[0].Metadata)
}

func TestEvent_JSONUnmarshal_FullEvent(t *testing.T) {
	input := `{
		"id": "evt_unmarshal",
		"name": "signup",
		"meta": {"channel": "organic"},
		"is_anonymous": true,
		"user_id": "user_u001",
		"organization_id": "org_u001",
		"tenant_id": "tenant_u001",
		"ip": "172.16.0.1",
		"user_agent": "UnmarshalAgent/1.0",
		"locale": "de-DE",
		"timezone": "Europe/Berlin",
		"timestamp": "2025-09-01T14:00:00Z",
		"app": {"name": "signup-app", "version": "3.0.0", "build_id": "build_u"},
		"campaign": {"name": "fall", "source": "facebook", "medium": "social", "term": "signup", "content": "cta"},
		"device": {"id": "dev_u", "manufacturer": "Google", "model": "Pixel 8", "name": "My Pixel", "type": "mobile", "version": "8", "advertising_id": "adid_u"},
		"location": {"city": "Berlin", "country": "Germany", "region": "Berlin", "latitude": 52.52, "longitude": 13.405, "speed": 0},
		"network": {"bluetooth": false, "cellular": true, "wifi": false, "carrier": "T-Mobile"},
		"os": {"name": "Android", "arch": "arm64", "version": "14"},
		"page": {"path": "/signup", "referrer": "https://facebook.com", "search": "?ref=fb", "title": "Sign Up", "url": "https://app.com/signup?ref=fb"},
		"referrer": {"type": "social", "name": "Facebook", "url": "https://facebook.com/ad/123", "link": "https://fb.me/abc"},
		"screen": {"density": 3, "width": 1080, "height": 2400},
		"subscriptions": [
			{"id": "sub_u001", "customer_id": "cus_u001", "product_id": "prod_u001", "increment_by": 1}
		]
	}`

	var actual Event
	err := json.Unmarshal([]byte(input), &actual)

	assert.NoError(t, err)
	assert.Equal(t, "evt_unmarshal", actual.ID)
	assert.Equal(t, "signup", actual.Name)
	assert.Equal(t, map[string]string{"channel": "organic"}, actual.Meta)
	assert.NotNil(t, actual.IsAnonymous)
	assert.True(t, *actual.IsAnonymous)
	assert.Equal(t, "user_u001", actual.UserID)
	assert.Equal(t, "org_u001", actual.OrganizationID)
	assert.Equal(t, "tenant_u001", actual.TenantID)
	assert.Equal(t, "172.16.0.1", actual.IP)
	assert.Equal(t, "UnmarshalAgent/1.0", actual.UserAgent)
	assert.Equal(t, "de-DE", actual.Locale)
	assert.Equal(t, "Europe/Berlin", actual.Timezone)
	assert.Equal(t, time.Date(2025, 9, 1, 14, 0, 0, 0, time.UTC), actual.Timestamp)

	assert.Equal(t, App{Name: "signup-app", Version: "3.0.0", BuildID: "build_u"}, actual.App)
	assert.Equal(t, Campaign{Name: "fall", Source: "facebook", Medium: "social", Term: "signup", Content: "cta"}, actual.Campaign)
	assert.Equal(t, Device{ID: "dev_u", Manufacturer: "Google", Model: "Pixel 8", Name: "My Pixel", Type: "mobile", Version: "8", AdvertisingID: "adid_u"}, actual.Device)

	assert.Equal(t, "Berlin", actual.Location.City)
	assert.Equal(t, "Germany", actual.Location.Country)
	assert.InDelta(t, 52.52, actual.Location.Latitude, 0.001)
	assert.InDelta(t, 13.405, actual.Location.Longitude, 0.001)

	assert.False(t, actual.Network.Bluetooth)
	assert.True(t, actual.Network.Cellular)
	assert.False(t, actual.Network.WIFI)
	assert.Equal(t, "T-Mobile", actual.Network.Carrier)

	assert.Equal(t, OS{Name: "Android", Arch: "arm64", Version: "14"}, actual.OS)
	assert.Equal(t, "/signup", actual.Page.Path)
	assert.Equal(t, "Sign Up", actual.Page.Title)
	assert.Equal(t, "social", actual.Referrer.Type)
	assert.Equal(t, "Facebook", actual.Referrer.Name)
	assert.Equal(t, Screen{Density: 3, Width: 1080, Height: 2400}, actual.Screen)

	assert.Len(t, actual.Subscriptions, 1)
	assert.Equal(t, "sub_u001", actual.Subscriptions[0].ID)
	assert.Equal(t, "cus_u001", actual.Subscriptions[0].CustomerID)
	assert.InDelta(t, 1.0, actual.Subscriptions[0].IncrementBy, 0.001)
}
