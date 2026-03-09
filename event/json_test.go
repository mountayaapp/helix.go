package event

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEventFromJSON(t *testing.T) {
	testcases := []struct {
		name     string
		input    json.RawMessage
		expected Event
		success  bool
	}{
		{
			name:     "invalid JSON input",
			input:    []byte(`not a valid JSON input`),
			expected: Event{},
			success:  false,
		},
		{
			name:     "missing event key",
			input:    []byte(`{ "_no_event_key": {} }`),
			expected: Event{},
			success:  false,
		},
		{
			name:     "empty JSON",
			input:    []byte(`{}`),
			expected: Event{},
			success:  false,
		},
		{
			name:     "null event",
			input:    []byte(`{"event": null}`),
			expected: Event{},
			success:  true,
		},
		{
			name:     "event value is array",
			input:    []byte(`{"event": [1, 2, 3]}`),
			expected: Event{},
			success:  false,
		},
		{
			name:     "event value is string",
			input:    []byte(`{"event": "not_an_object"}`),
			expected: Event{},
			success:  false,
		},
		{
			name: "event with invalid key only",
			input: []byte(`{ "event": {
        "invalid_key": true
      } }`),
			expected: Event{},
			success:  true,
		},
		{
			name: "event with name and meta",
			input: []byte(`{ "event": {
        "name": "testing",
        "meta": {
          "source": "web"
        }
      } }`),
			expected: Event{
				Name: "testing",
				Meta: map[string]string{
					"source": "web",
				},
			},
			success: true,
		},
		{
			name: "event with other sibling keys",
			input: []byte(`{
				"some_other_key": "value",
				"event": {"name": "test"},
				"another_key": 42
			}`),
			expected: Event{Name: "test"},
			success:  true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual, ok := EventFromJSON(tc.input)

			assert.Equal(t, tc.expected, actual)
			assert.Equal(t, tc.success, ok)
		})
	}
}

func TestEventFromJSON_WithFullEvent(t *testing.T) {
	input := []byte(`{
		"event": {
			"id": "evt_full_001",
			"name": "full_event",
			"meta": {"env": "production"},
			"is_anonymous": false,
			"user_id": "user_full",
			"organization_id": "org_full",
			"tenant_id": "tenant_full",
			"ip": "192.168.1.1",
			"user_agent": "FullAgent/2.0",
			"locale": "fr-FR",
			"timezone": "Europe/Paris",
			"timestamp": "2025-06-15T10:30:00Z",
			"app": {
				"name": "full-app",
				"version": "2.0.0",
				"build_id": "build_full"
			},
			"campaign": {
				"name": "full_campaign",
				"source": "newsletter",
				"medium": "email",
				"term": "promo",
				"content": "header"
			},
			"device": {
				"id": "dev_full",
				"manufacturer": "Samsung",
				"model": "Galaxy S24",
				"name": "My Galaxy",
				"type": "mobile",
				"version": "S24",
				"advertising_id": "adid_full"
			},
			"location": {
				"city": "Lyon",
				"country": "France",
				"region": "Auvergne-Rhone-Alpes",
				"latitude": 45.764,
				"longitude": 4.8357,
				"speed": 5.0
			},
			"network": {
				"bluetooth": false,
				"cellular": true,
				"wifi": false,
				"carrier": "Orange"
			},
			"os": {
				"name": "Android",
				"arch": "arm64",
				"version": "14"
			},
			"page": {
				"path": "/dashboard",
				"referrer": "https://example.com",
				"search": "?tab=overview",
				"title": "Dashboard",
				"url": "https://app.example.com/dashboard?tab=overview"
			},
			"referrer": {
				"type": "organic",
				"name": "Google",
				"url": "https://google.com/search?q=example",
				"link": "https://google.com"
			},
			"screen": {
				"density": 3,
				"width": 2560,
				"height": 1440
			},
			"subscriptions": [
				{
					"id": "sub_full_001",
					"tenant_id": "tenant_sub",
					"customer_id": "cus_full",
					"product_id": "prod_full",
					"price_id": "price_full",
					"usage": "api_calls",
					"increment_by": 10.5,
					"metadata": {"plan": "enterprise"}
				},
				{
					"id": "sub_full_002",
					"customer_id": "cus_full_2",
					"product_id": "prod_storage",
					"price_id": "price_002",
					"usage": "storage_gb",
					"increment_by": 0.5,
					"metadata": {"tier": "pro", "region": "us-east"}
				}
			]
		}
	}`)

	actual, ok := EventFromJSON(input)

	assert.True(t, ok)
	assert.Equal(t, "evt_full_001", actual.ID)
	assert.Equal(t, "full_event", actual.Name)
	assert.Equal(t, map[string]string{"env": "production"}, actual.Meta)
	assert.NotNil(t, actual.IsAnonymous)
	assert.False(t, *actual.IsAnonymous)
	assert.Equal(t, "user_full", actual.UserID)
	assert.Equal(t, "org_full", actual.OrganizationID)
	assert.Equal(t, "tenant_full", actual.TenantID)
	assert.Equal(t, "192.168.1.1", actual.IP)
	assert.Equal(t, "FullAgent/2.0", actual.UserAgent)
	assert.Equal(t, "fr-FR", actual.Locale)
	assert.Equal(t, "Europe/Paris", actual.Timezone)
	assert.Equal(t, time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC), actual.Timestamp)

	assert.Equal(t, App{Name: "full-app", Version: "2.0.0", BuildID: "build_full"}, actual.App)
	assert.Equal(t, Campaign{Name: "full_campaign", Source: "newsletter", Medium: "email", Term: "promo", Content: "header"}, actual.Campaign)
	assert.Equal(t, Device{ID: "dev_full", Manufacturer: "Samsung", Model: "Galaxy S24", Name: "My Galaxy", Type: "mobile", Version: "S24", AdvertisingID: "adid_full"}, actual.Device)

	assert.Equal(t, "Lyon", actual.Location.City)
	assert.Equal(t, "France", actual.Location.Country)
	assert.Equal(t, "Auvergne-Rhone-Alpes", actual.Location.Region)
	assert.InDelta(t, 45.764, actual.Location.Latitude, 0.001)
	assert.InDelta(t, 4.8357, actual.Location.Longitude, 0.001)
	assert.InDelta(t, 5.0, actual.Location.Speed, 0.001)

	assert.False(t, actual.Network.Bluetooth)
	assert.True(t, actual.Network.Cellular)
	assert.False(t, actual.Network.WIFI)
	assert.Equal(t, "Orange", actual.Network.Carrier)

	assert.Equal(t, OS{Name: "Android", Arch: "arm64", Version: "14"}, actual.OS)
	assert.Equal(t, Page{Path: "/dashboard", Referrer: "https://example.com", Search: "?tab=overview", Title: "Dashboard", URL: "https://app.example.com/dashboard?tab=overview"}, actual.Page)
	assert.Equal(t, Referrer{Type: "organic", Name: "Google", URL: "https://google.com/search?q=example", Link: "https://google.com"}, actual.Referrer)
	assert.Equal(t, Screen{Density: 3, Width: 2560, Height: 1440}, actual.Screen)

	assert.Len(t, actual.Subscriptions, 2)
	assert.Equal(t, "sub_full_001", actual.Subscriptions[0].ID)
	assert.Equal(t, "tenant_sub", actual.Subscriptions[0].TenantID)
	assert.Equal(t, "cus_full", actual.Subscriptions[0].CustomerID)
	assert.Equal(t, "prod_full", actual.Subscriptions[0].ProductID)
	assert.Equal(t, "price_full", actual.Subscriptions[0].PriceID)
	assert.Equal(t, "api_calls", actual.Subscriptions[0].Usage)
	assert.InDelta(t, 10.5, actual.Subscriptions[0].IncrementBy, 0.001)
	assert.Equal(t, map[string]string{"plan": "enterprise"}, actual.Subscriptions[0].Metadata)

	assert.Equal(t, "sub_full_002", actual.Subscriptions[1].ID)
	assert.Equal(t, "cus_full_2", actual.Subscriptions[1].CustomerID)
	assert.Equal(t, "prod_storage", actual.Subscriptions[1].ProductID)
	assert.Equal(t, "price_002", actual.Subscriptions[1].PriceID)
	assert.Equal(t, "storage_gb", actual.Subscriptions[1].Usage)
	assert.InDelta(t, 0.5, actual.Subscriptions[1].IncrementBy, 0.001)
	assert.Equal(t, map[string]string{"tier": "pro", "region": "us-east"}, actual.Subscriptions[1].Metadata)
}

func TestKey(t *testing.T) {
	assert.Equal(t, "event", Key)
}
