package event

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
)

func TestInjectEventAppToFlatMap(t *testing.T) {
	testcases := []struct {
		name     string
		input    App
		expected map[string]string
	}{
		{
			name:     "empty struct",
			input:    App{},
			expected: map[string]string{},
		},
		{
			name: "all fields populated",
			input: App{
				Name:    "app_name_test",
				Version: "app_version_test",
				BuildId: "app_buildid_test",
			},
			expected: map[string]string{
				"event.app.name":     "app_name_test",
				"event.app.version":  "app_version_test",
				"event.app.build_id": "app_buildid_test",
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var actual = make(map[string]string)
			maps.Copy(actual, tc.expected)

			injectEventAppToFlatMap(tc.input, tc.expected)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestApplyEventAppFromBaggageMember(t *testing.T) {
	testcases := []struct {
		name     string
		input    func() baggage.Member
		expected *Event
	}{
		{
			name: "unknown field",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.app.unknown", "anything")
				return m
			},
			expected: &Event{
				App: App{},
			},
		},
		{
			name: "name",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.app.name", "app_name_test")
				return m
			},
			expected: &Event{
				App: App{
					Name: "app_name_test",
				},
			},
		},
		{
			name: "version",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.app.version", "app_version_test")
				return m
			},
			expected: &Event{
				App: App{
					Version: "app_version_test",
				},
			},
		},
		{
			name: "build_id",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.app.build_id", "app_buildid_test")
				return m
			},
			expected: &Event{
				App: App{
					BuildId: "app_buildid_test",
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := new(Event)
			applyEventAppFromBaggageMember(tc.input(), actual)

			assert.Equal(t, tc.expected, actual)
		})
	}
}
