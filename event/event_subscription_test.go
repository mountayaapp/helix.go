package event

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
)

func TestInjectEventSubscriptionsToFlatMap(t *testing.T) {
	testcases := []struct {
		name     string
		input    []Subscription
		expected map[string]string
	}{
		{
			name:     "empty slice",
			input:    []Subscription{},
			expected: map[string]string{},
		},
		{
			name: "all fields populated for two subscriptions",
			input: []Subscription{
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
				{
					Id:          "subscription_1_id_test",
					CustomerId:  "subscription_1_customerid_test",
					ProductId:   "subscription_1_productid_test",
					PriceId:     "subscription_1_priceid_test",
					Usage:       "subscription_1_usage_test",
					IncrementBy: 1.25,
					Metadata: map[string]string{
						"version": "b",
					},
				},
			},
			expected: map[string]string{
				"event.subscriptions.0.id":               "subscription_0_id_test",
				"event.subscriptions.0.customer_id":      "subscription_0_customerid_test",
				"event.subscriptions.0.product_id":       "subscription_0_productid_test",
				"event.subscriptions.0.price_id":         "subscription_0_priceid_test",
				"event.subscriptions.0.usage":            "subscription_0_usage_test",
				"event.subscriptions.0.increment_by":     "1.000000",
				"event.subscriptions.0.metadata.version": "a",
				"event.subscriptions.1.id":               "subscription_1_id_test",
				"event.subscriptions.1.customer_id":      "subscription_1_customerid_test",
				"event.subscriptions.1.product_id":       "subscription_1_productid_test",
				"event.subscriptions.1.price_id":         "subscription_1_priceid_test",
				"event.subscriptions.1.usage":            "subscription_1_usage_test",
				"event.subscriptions.1.increment_by":     "1.250000",
				"event.subscriptions.1.metadata.version": "b",
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var actual = make(map[string]string)
			maps.Copy(actual, tc.expected)

			injectEventSubscriptionsToFlatMap(tc.input, tc.expected)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestApplyEventSubscriptionsFromBaggageMember(t *testing.T) {
	testcases := []struct {
		name     string
		input    func() baggage.Member
		expected *Event
	}{
		{
			name: "unknown field at index 0",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.0.unknown", "anything")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{},
				},
			},
		},
		{
			name: "id at index 0",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.0.id", "subscription_0_id_test")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{
						Id: "subscription_0_id_test",
					},
				},
			},
		},
		{
			name: "customer_id at index 0",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.0.customer_id", "subscription_0_customerid_test")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{
						CustomerId: "subscription_0_customerid_test",
					},
				},
			},
		},
		{
			name: "product_id at index 0",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.0.product_id", "subscription_0_productid_test")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{
						ProductId: "subscription_0_productid_test",
					},
				},
			},
		},
		{
			name: "usage at index 0",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.0.usage", "subscription_0_usage_test")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{
						Usage: "subscription_0_usage_test",
					},
				},
			},
		},
		{
			name: "increment_by at index 0",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.0.increment_by", "1.000000")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{
						IncrementBy: 1,
					},
				},
			},
		},
		{
			name: "metadata at index 0",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.0.metadata.version", "a")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{
						Metadata: map[string]string{
							"version": "a",
						},
					},
				},
			},
		},
		{
			name: "unknown field at index 1",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.1.unknown", "anything")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{},
					{},
				},
			},
		},
		{
			name: "id at index 1",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.1.id", "subscription_1_id_test")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{},
					{
						Id: "subscription_1_id_test",
					},
				},
			},
		},
		{
			name: "customer_id at index 1",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.1.customer_id", "subscription_1_customerid_test")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{},
					{
						CustomerId: "subscription_1_customerid_test",
					},
				},
			},
		},
		{
			name: "product_id at index 1",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.1.product_id", "subscription_1_productid_test")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{},
					{
						ProductId: "subscription_1_productid_test",
					},
				},
			},
		},
		{
			name: "usage at index 1",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.1.usage", "subscription_1_usage_test")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{},
					{
						Usage: "subscription_1_usage_test",
					},
				},
			},
		},
		{
			name: "increment_by at index 1",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.1.increment_by", "1.250000")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{},
					{
						IncrementBy: 1.25,
					},
				},
			},
		},
		{
			name: "metadata at index 1",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.subscriptions.1.metadata.version", "b")
				return m
			},
			expected: &Event{
				Subscriptions: []Subscription{
					{},
					{
						Metadata: map[string]string{
							"version": "b",
						},
					},
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := new(Event)
			applyEventSubscriptionsFromBaggageMember(tc.input(), actual)

			assert.Equal(t, tc.expected, actual)
		})
	}
}
