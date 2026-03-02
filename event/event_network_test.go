package event

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
)

func TestInjectEventNetworkToFlatMap(t *testing.T) {
	testcases := []struct {
		name     string
		input    Network
		expected map[string]string
	}{
		{
			name:     "empty struct",
			input:    Network{},
			expected: map[string]string{},
		},
		{
			name: "all fields populated",
			input: Network{
				Bluetooth: true,
				Cellular:  false,
				WIFI:      true,
				Carrier:   "network_carrier_test",
			},
			expected: map[string]string{
				"event.network.bluetooth": "true",
				"event.network.wifi":      "true",
				"event.network.carrier":   "network_carrier_test",
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var actual = make(map[string]string)
			maps.Copy(actual, tc.expected)

			injectEventNetworkToFlatMap(tc.input, tc.expected)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestApplyEventNetworkFromBaggageMember(t *testing.T) {
	testcases := []struct {
		name     string
		input    func() baggage.Member
		expected *Event
	}{
		{
			name: "unknown field",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.network.unknown", "anything")
				return m
			},
			expected: &Event{
				Network: Network{},
			},
		},
		{
			name: "bluetooth",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.network.bluetooth", "true")
				return m
			},
			expected: &Event{
				Network: Network{
					Bluetooth: true,
				},
			},
		},
		{
			name: "cellular",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.network.cellular", "true")
				return m
			},
			expected: &Event{
				Network: Network{
					Cellular: true,
				},
			},
		},
		{
			name: "wifi",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.network.wifi", "true")
				return m
			},
			expected: &Event{
				Network: Network{
					WIFI: true,
				},
			},
		},
		{
			name: "carrier",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.network.carrier", "network_carrier_test")
				return m
			},
			expected: &Event{
				Network: Network{
					Carrier: "network_carrier_test",
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := new(Event)
			applyEventNetworkFromBaggageMember(tc.input(), actual)

			assert.Equal(t, tc.expected, actual)
		})
	}
}
