package event

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
)

func TestInjectEventDeviceToFlatMap(t *testing.T) {
	testcases := []struct {
		name     string
		input    Device
		expected map[string]string
	}{
		{
			name:     "empty struct",
			input:    Device{},
			expected: map[string]string{},
		},
		{
			name: "all fields populated",
			input: Device{
				Id:            "device_id_test",
				Manufacturer:  "device_manufacturer_test",
				Model:         "device_model_test",
				Name:          "device_name_test",
				Type:          "device_type_test",
				Version:       "device_version_test",
				AdvertisingId: "device_advertisingid_test",
			},
			expected: map[string]string{
				"event.device.id":             "device_id_test",
				"event.device.manufacturer":   "device_manufacturer_test",
				"event.device.model":          "device_model_test",
				"event.device.name":           "device_name_test",
				"event.device.type":           "device_type_test",
				"event.device.version":        "device_version_test",
				"event.device.advertising_id": "device_advertisingid_test",
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var actual = make(map[string]string)
			maps.Copy(actual, tc.expected)

			injectEventDeviceToFlatMap(tc.input, tc.expected)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestApplyEventDeviceFromBaggageMember(t *testing.T) {
	testcases := []struct {
		name     string
		input    func() baggage.Member
		expected *Event
	}{
		{
			name: "unknown field",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.device.unknown", "anything")
				return m
			},
			expected: &Event{
				Device: Device{},
			},
		},
		{
			name: "id",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.device.id", "device_id_test")
				return m
			},
			expected: &Event{
				Device: Device{
					Id: "device_id_test",
				},
			},
		},
		{
			name: "manufacturer",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.device.manufacturer", "device_manufacturer_test")
				return m
			},
			expected: &Event{
				Device: Device{
					Manufacturer: "device_manufacturer_test",
				},
			},
		},
		{
			name: "model",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.device.model", "device_model_test")
				return m
			},
			expected: &Event{
				Device: Device{
					Model: "device_model_test",
				},
			},
		},
		{
			name: "name",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.device.name", "device_name_test")
				return m
			},
			expected: &Event{
				Device: Device{
					Name: "device_name_test",
				},
			},
		},
		{
			name: "type",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.device.type", "device_type_test")
				return m
			},
			expected: &Event{
				Device: Device{
					Type: "device_type_test",
				},
			},
		},
		{
			name: "version",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.device.version", "device_version_test")
				return m
			},
			expected: &Event{
				Device: Device{
					Version: "device_version_test",
				},
			},
		},
		{
			name: "advertising_id",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.device.advertising_id", "device_advertisingid_test")
				return m
			},
			expected: &Event{
				Device: Device{
					AdvertisingId: "device_advertisingid_test",
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := new(Event)
			applyEventDeviceFromBaggageMember(tc.input(), actual)

			assert.Equal(t, tc.expected, actual)
		})
	}
}
