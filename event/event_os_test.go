package event

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
)

func TestInjectEventOSToFlatMap(t *testing.T) {
	testcases := []struct {
		name     string
		input    OS
		expected map[string]string
	}{
		{
			name:     "empty struct",
			input:    OS{},
			expected: map[string]string{},
		},
		{
			name: "all fields populated",
			input: OS{
				Name:    "app_name_test",
				Arch:    "app_arch_test",
				Version: "app_version_test",
			},
			expected: map[string]string{
				"event.os.name":    "app_name_test",
				"event.os.arch":    "app_arch_test",
				"event.os.version": "app_version_test",
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var actual = make(map[string]string)
			maps.Copy(actual, tc.expected)

			injectEventOSToFlatMap(tc.input, tc.expected)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestApplyEventOSFromBaggageMember(t *testing.T) {
	testcases := []struct {
		name     string
		input    func() baggage.Member
		expected *Event
	}{
		{
			name: "unknown field",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.os.unknown", "anything")
				return m
			},
			expected: &Event{
				OS: OS{},
			},
		},
		{
			name: "name",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.os.name", "app_name_test")
				return m
			},
			expected: &Event{
				OS: OS{
					Name: "app_name_test",
				},
			},
		},
		{
			name: "arch",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.os.arch", "app_arch_test")
				return m
			},
			expected: &Event{
				OS: OS{
					Arch: "app_arch_test",
				},
			},
		},
		{
			name: "version",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.os.version", "app_version_test")
				return m
			},
			expected: &Event{
				OS: OS{
					Version: "app_version_test",
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := new(Event)
			applyEventOSFromBaggageMember(tc.input(), actual)

			assert.Equal(t, tc.expected, actual)
		})
	}
}
