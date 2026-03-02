package event

import (
	"maps"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
)

func TestInjectEventToFlatMap(t *testing.T) {
	testcases := []struct {
		name     string
		input    Event
		expected map[string]string
	}{
		{
			name:     "empty struct",
			input:    Event{},
			expected: map[string]string{},
		},
		{
			name: "all fields populated",
			input: Event{
				Name: "name_test",
				Meta: map[string]string{
					"hello": "world",
					"this":  "works",
				},
				Params: url.Values{
					"query": []string{"a", "b"},
				},
				IsAnonymous: false,
				Subscriptions: []Subscription{
					{
						Id:          "subscription_0_id_test",
						CustomerId:  "subscription_0_customerid_test",
						ProductId:   "subscription_0_productid_test",
						PriceId:     "subscription_0_priceid_test",
						Usage:       "subscription_0_usage_test",
						IncrementBy: 1,
						Metadata: map[string]string{
							"version": "a",
						},
					},
				},
			},
			expected: map[string]string{
				"event.name":                             "name_test",
				"event.meta.hello":                       "world",
				"event.meta.this":                        "works",
				"event.params.query.0":                   "a",
				"event.params.query.1":                   "b",
				"event.subscriptions.0.id":               "subscription_0_id_test",
				"event.subscriptions.0.customer_id":      "subscription_0_customerid_test",
				"event.subscriptions.0.product_id":       "subscription_0_productid_test",
				"event.subscriptions.0.price_id":         "subscription_0_priceid_test",
				"event.subscriptions.0.usage":            "subscription_0_usage_test",
				"event.subscriptions.0.increment_by":     "1.000000",
				"event.subscriptions.0.metadata.version": "a",
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var actual = make(map[string]string)
			maps.Copy(actual, tc.expected)

			injectEventToFlatMap(tc.input, tc.expected)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestInjectEventToFlatMap_TopLevelFields(t *testing.T) {
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	e := Event{
		Id:             "evt_123",
		Name:           "subscribed",
		UserId:         "user_456",
		OrganizationId: "org_789",
		TenantId:       "tenant_abc",
		IP:             net.ParseIP("192.168.1.1"),
		UserAgent:      "Mozilla/5.0",
		Locale:         "en-US",
		Timezone:       "America/New_York",
		Timestamp:      ts,
		IsAnonymous:    true,
	}

	flatten := make(map[string]string)
	injectEventToFlatMap(e, flatten)

	assert.Equal(t, "evt_123", flatten["event.id"])
	assert.Equal(t, "subscribed", flatten["event.name"])
	assert.Equal(t, "user_456", flatten["event.user_id"])
	assert.Equal(t, "org_789", flatten["event.organization_id"])
	assert.Equal(t, "tenant_abc", flatten["event.tenant_id"])
	assert.Equal(t, "192.168.1.1", flatten["event.ip"])
	assert.Equal(t, "Mozilla/5.0", flatten["event.user_agent"])
	assert.Equal(t, "en-US", flatten["event.locale"])
	assert.Equal(t, "America/New_York", flatten["event.timezone"])
	assert.Equal(t, ts.Format(time.RFC3339Nano), flatten["event.timestamp"])
	assert.Equal(t, "true", flatten["event.is_anonymous"])
}

func TestInjectEventToFlatMap_CleansEmptyValues(t *testing.T) {
	e := Event{
		Name:        "",
		IsAnonymous: false,
	}

	flatten := make(map[string]string)
	injectEventToFlatMap(e, flatten)

	// Empty string values, "false", "0", "0E+00", "0.000000", and "<nil>" should be removed.
	assert.NotContains(t, flatten, "event.name")
	assert.NotContains(t, flatten, "event.id")
	assert.NotContains(t, flatten, "event.is_anonymous")
	assert.NotContains(t, flatten, "event.user_id")
	assert.NotContains(t, flatten, "event.ip")
}

func TestInjectEventToFlatMap_TimestampOmittedWhenZero(t *testing.T) {
	e := Event{
		Name: "test",
	}

	flatten := make(map[string]string)
	injectEventToFlatMap(e, flatten)

	assert.NotContains(t, flatten, "event.timestamp")
}

func TestInjectEventToFlatMap_NilFlatten(t *testing.T) {

	// injectEventToFlatMap handles nil flatten map by creating a new one,
	// but changes won't be visible to caller. This tests it doesn't panic.
	assert.NotPanics(t, func() {
		injectEventToFlatMap(Event{Name: "test"}, nil)
	})
}

func TestInjectEventToFlatMap_MultipleParams(t *testing.T) {
	e := Event{
		Name: "search",
		Params: url.Values{
			"filters": []string{"a", "b", "c"},
			"query":   []string{"hello"},
			"sort":    []string{"asc", "desc"},
		},
	}

	flatten := make(map[string]string)
	injectEventToFlatMap(e, flatten)

	assert.Equal(t, "a", flatten["event.params.filters.0"])
	assert.Equal(t, "b", flatten["event.params.filters.1"])
	assert.Equal(t, "c", flatten["event.params.filters.2"])
	assert.Equal(t, "hello", flatten["event.params.query.0"])
	assert.Equal(t, "asc", flatten["event.params.sort.0"])
	assert.Equal(t, "desc", flatten["event.params.sort.1"])
}

func TestInjectEventToFlatMap_IPv6(t *testing.T) {
	e := Event{
		Name: "test",
		IP:   net.ParseIP("::1"),
	}

	flatten := make(map[string]string)
	injectEventToFlatMap(e, flatten)

	assert.Equal(t, "::1", flatten["event.ip"])
}

func TestInjectEventToFlatMap_AllNestedObjects(t *testing.T) {
	e := Event{
		Name: "full_event",
		App:  App{Name: "my-app", Version: "1.0.0", BuildId: "abc"},
		Campaign: Campaign{
			Name: "summer_sale", Source: "google", Medium: "cpc",
			Term: "shoes", Content: "banner",
		},
		Device: Device{
			Id: "dev_1", Manufacturer: "Apple", Model: "iPhone",
			Name: "iPhone 15", Type: "mobile", Version: "17.0",
			AdvertisingId: "ad_123",
		},
		Location: Location{
			City: "Paris", Country: "FR", Region: "IDF",
			Latitude: 48.8566, Longitude: 2.3522, Speed: 0,
		},
		Network: Network{
			Bluetooth: true, Cellular: false, WIFI: true,
			Carrier: "Orange",
		},
		OS:       OS{Name: "iOS", Arch: "arm64", Version: "17.0"},
		Page:     Page{Path: "/home", Title: "Home", URL: "https://example.com"},
		Referrer: Referrer{Type: "organic", Name: "Google", URL: "https://google.com"},
		Screen:   Screen{Density: 3, Width: 1170, Height: 2532},
	}

	flatten := make(map[string]string)
	injectEventToFlatMap(e, flatten)

	assert.Equal(t, "my-app", flatten["event.app.name"])
	assert.Equal(t, "1.0.0", flatten["event.app.version"])
	assert.Equal(t, "abc", flatten["event.app.build_id"])
	assert.Equal(t, "summer_sale", flatten["event.campaign.name"])
	assert.Equal(t, "google", flatten["event.campaign.source"])
	assert.Equal(t, "dev_1", flatten["event.device.id"])
	assert.Equal(t, "Apple", flatten["event.device.manufacturer"])
	assert.Equal(t, "Paris", flatten["event.location.city"])
	assert.Equal(t, "48.856600", flatten["event.location.latitude"])
	assert.Equal(t, "true", flatten["event.network.bluetooth"])
	assert.Equal(t, "Orange", flatten["event.network.carrier"])
	assert.Equal(t, "iOS", flatten["event.os.name"])
	assert.Equal(t, "/home", flatten["event.page.path"])
	assert.Equal(t, "organic", flatten["event.referrer.type"])
	assert.Equal(t, "3", flatten["event.screen.density"])
	assert.Equal(t, "1170", flatten["event.screen.width"])
	assert.Equal(t, "2532", flatten["event.screen.height"])
}

func TestInjectExtractRoundTrip(t *testing.T) {
	input := Event{
		Name: "subscribed",
		Meta: map[string]string{
			"key": "value",
		},
		Params: url.Values{
			"filters": []string{"a", "b"},
			"query":   []string{"search"},
		},
		Subscriptions: []Subscription{
			{
				Id:         "sub_001",
				CustomerId: "cus_001",
			},
		},
	}

	flatten := make(map[string]string)
	injectEventToFlatMap(input, flatten)

	var members []baggage.Member
	for k, v := range flatten {
		m, _ := baggage.NewMember(k, v)
		members = append(members, m)
	}

	b, _ := baggage.New(members...)
	output := extractEventFromBaggage(b)

	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.Meta, output.Meta)
	assert.Equal(t, input.Params, output.Params)
	assert.Len(t, output.Subscriptions, 1)
	assert.Equal(t, "sub_001", output.Subscriptions[0].Id)
	assert.Equal(t, "cus_001", output.Subscriptions[0].CustomerId)
}

func TestInjectExtractRoundTrip_TopLevelFields(t *testing.T) {
	ts := time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC)
	input := Event{
		Name:           "page_view",
		UserId:         "user_abc",
		OrganizationId: "org_def",
		TenantId:       "tenant_ghi",
		IP:             net.ParseIP("10.0.0.1"),
		UserAgent:      "TestAgent/1.0",
		Locale:         "fr-FR",
		Timezone:       "Europe/Paris",
		Timestamp:      ts,
		IsAnonymous:    true,
	}

	flatten := make(map[string]string)
	injectEventToFlatMap(input, flatten)

	var members []baggage.Member
	for k, v := range flatten {
		m, _ := baggage.NewMember(k, v)
		members = append(members, m)
	}

	b, _ := baggage.New(members...)
	output := extractEventFromBaggage(b)

	assert.Equal(t, input.Name, output.Name)
	assert.Equal(t, input.UserId, output.UserId)
	assert.Equal(t, input.OrganizationId, output.OrganizationId)
	assert.Equal(t, input.TenantId, output.TenantId)
	assert.True(t, input.IP.Equal(output.IP))
	assert.Equal(t, input.UserAgent, output.UserAgent)
	assert.Equal(t, input.Locale, output.Locale)
	assert.Equal(t, input.Timezone, output.Timezone)
	assert.True(t, input.Timestamp.Equal(output.Timestamp))
	assert.Equal(t, input.IsAnonymous, output.IsAnonymous)
}

func TestInjectExtractRoundTrip_NestedObjects(t *testing.T) {
	input := Event{
		Name: "full_test",
		App:  App{Name: "my-app", Version: "2.0.0"},
		OS:   OS{Name: "macOS", Arch: "arm64", Version: "14.0"},
		Page: Page{Path: "about", Title: "AboutUs"},
	}

	flatten := make(map[string]string)
	injectEventToFlatMap(input, flatten)

	var members []baggage.Member
	for k, v := range flatten {
		m, _ := baggage.NewMember(k, v)
		members = append(members, m)
	}

	b, _ := baggage.New(members...)
	output := extractEventFromBaggage(b)

	assert.Equal(t, input.App, output.App)
	assert.Equal(t, input.OS, output.OS)
	assert.Equal(t, input.Page, output.Page)
}

func TestInjectExtractRoundTrip_MultipleSubscriptions(t *testing.T) {
	input := Event{
		Name: "multi_sub",
		Subscriptions: []Subscription{
			{Id: "sub_0", CustomerId: "cus_0", ProductId: "prod_0"},
			{Id: "sub_1", CustomerId: "cus_1", ProductId: "prod_1"},
		},
	}

	flatten := make(map[string]string)
	injectEventToFlatMap(input, flatten)

	var members []baggage.Member
	for k, v := range flatten {
		m, _ := baggage.NewMember(k, v)
		members = append(members, m)
	}

	b, _ := baggage.New(members...)
	output := extractEventFromBaggage(b)

	assert.Len(t, output.Subscriptions, 2)
	assert.Equal(t, "sub_0", output.Subscriptions[0].Id)
	assert.Equal(t, "cus_0", output.Subscriptions[0].CustomerId)
	assert.Equal(t, "prod_0", output.Subscriptions[0].ProductId)
	assert.Equal(t, "sub_1", output.Subscriptions[1].Id)
	assert.Equal(t, "cus_1", output.Subscriptions[1].CustomerId)
	assert.Equal(t, "prod_1", output.Subscriptions[1].ProductId)
}

func TestExtractEventFromBaggage(t *testing.T) {
	testcases := []struct {
		name     string
		input    func() baggage.Baggage
		expected Event
	}{
		{
			name: "name and meta from baggage",
			input: func() baggage.Baggage {
				name, _ := baggage.NewMember("event.name", "name_test")
				metaHelloWorld, _ := baggage.NewMember("event.meta.hello", "world")
				metaThisWorks, _ := baggage.NewMember("event.meta.this", "works")

				b, _ := baggage.New(name, metaHelloWorld, metaThisWorks)
				return b
			},
			expected: Event{
				Name: "name_test",
				Meta: map[string]string{
					"hello": "world",
					"this":  "works",
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := extractEventFromBaggage(tc.input())

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestExtractEventFromBaggage_EmptyBaggage(t *testing.T) {
	b, _ := baggage.New()

	actual := extractEventFromBaggage(b)

	assert.Equal(t, Event{}, actual)
}

func TestExtractEventFromBaggage_NonEventMembers(t *testing.T) {
	m1, _ := baggage.NewMember("other.key", "value")
	m2, _ := baggage.NewMember("something.else", "data")
	b, _ := baggage.New(m1, m2)

	actual := extractEventFromBaggage(b)

	assert.Equal(t, Event{}, actual)
}

func TestExtractEventFromBaggage_TopLevelFields(t *testing.T) {
	name, _ := baggage.NewMember("event.name", "test_event")
	userId, _ := baggage.NewMember("event.user_id", "user_123")
	orgId, _ := baggage.NewMember("event.organization_id", "org_456")
	tenantId, _ := baggage.NewMember("event.tenant_id", "tenant_789")
	ip, _ := baggage.NewMember("event.ip", "10.0.0.1")
	ua, _ := baggage.NewMember("event.user_agent", "TestAgent")
	locale, _ := baggage.NewMember("event.locale", "en-US")
	tz, _ := baggage.NewMember("event.timezone", "UTC")
	anon, _ := baggage.NewMember("event.is_anonymous", "true")

	b, _ := baggage.New(name, userId, orgId, tenantId, ip, ua, locale, tz, anon)
	actual := extractEventFromBaggage(b)

	assert.Equal(t, "test_event", actual.Name)
	assert.Equal(t, "user_123", actual.UserId)
	assert.Equal(t, "org_456", actual.OrganizationId)
	assert.Equal(t, "tenant_789", actual.TenantId)
	assert.True(t, net.ParseIP("10.0.0.1").Equal(actual.IP))
	assert.Equal(t, "TestAgent", actual.UserAgent)
	assert.Equal(t, "en-US", actual.Locale)
	assert.Equal(t, "UTC", actual.Timezone)
	assert.True(t, actual.IsAnonymous)
}

func TestExtractEventFromBaggage_Timestamp(t *testing.T) {
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	name, _ := baggage.NewMember("event.name", "test")
	timestamp, _ := baggage.NewMember("event.timestamp", ts.Format(time.RFC3339Nano))

	b, _ := baggage.New(name, timestamp)
	actual := extractEventFromBaggage(b)

	assert.True(t, ts.Equal(actual.Timestamp))
}

func TestExtractEventFromBaggage_Params(t *testing.T) {
	name, _ := baggage.NewMember("event.name", "search")
	p0, _ := baggage.NewMember("event.params.query.0", "hello")
	p1, _ := baggage.NewMember("event.params.filters.0", "a")
	p2, _ := baggage.NewMember("event.params.filters.1", "b")

	b, _ := baggage.New(name, p0, p1, p2)
	actual := extractEventFromBaggage(b)

	assert.Equal(t, url.Values{
		"query":   []string{"hello"},
		"filters": []string{"a", "b"},
	}, actual.Params)
}

func TestToFlatMap(t *testing.T) {
	e := Event{
		Name:   "test_event",
		UserId: "user_abc",
		Meta: map[string]string{
			"key": "value",
		},
	}

	flatten := ToFlatMap(e)

	assert.NotNil(t, flatten)
	assert.Equal(t, "test_event", flatten["event.name"])
	assert.Equal(t, "user_abc", flatten["event.user_id"])
	assert.Equal(t, "value", flatten["event.meta.key"])
}

func TestToFlatMap_EmptyEvent(t *testing.T) {
	e := Event{}

	flatten := ToFlatMap(e)

	assert.NotNil(t, flatten)
	assert.Empty(t, flatten)
}

func TestToFlatMap_WithParams(t *testing.T) {
	e := Event{
		Name: "search",
		Params: url.Values{
			"q": []string{"test"},
		},
	}

	flatten := ToFlatMap(e)

	assert.Equal(t, "test", flatten["event.params.q.0"])
}

func TestKey(t *testing.T) {
	assert.Equal(t, "event", Key)
}
