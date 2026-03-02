package event

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
)

func TestInjectEventScreenToFlatMap(t *testing.T) {
	testcases := []struct {
		name     string
		input    Screen
		expected map[string]string
	}{
		{
			name:     "empty struct",
			input:    Screen{},
			expected: map[string]string{},
		},
		{
			name: "all fields populated",
			input: Screen{
				Density: 2,
				Width:   12,
				Height:  20,
			},
			expected: map[string]string{
				"event.screen.density": "2",
				"event.screen.width":   "12",
				"event.screen.height":  "20",
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var actual = make(map[string]string)
			maps.Copy(actual, tc.expected)

			injectEventScreenToFlatMap(tc.input, tc.expected)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestApplyEventScreenFromBaggageMember(t *testing.T) {
	testcases := []struct {
		name     string
		input    func() baggage.Member
		expected *Event
	}{
		{
			name: "unknown field",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.screen.unknown", "anything")
				return m
			},
			expected: &Event{
				Screen: Screen{},
			},
		},
		{
			name: "density",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.screen.density", "2")
				return m
			},
			expected: &Event{
				Screen: Screen{
					Density: 2,
				},
			},
		},
		{
			name: "width",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.screen.width", "12")
				return m
			},
			expected: &Event{
				Screen: Screen{
					Width: 12,
				},
			},
		},
		{
			name: "height",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.screen.height", "20")
				return m
			},
			expected: &Event{
				Screen: Screen{
					Height: 20,
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := new(Event)
			applyEventScreenFromBaggageMember(tc.input(), actual)

			assert.Equal(t, tc.expected, actual)
		})
	}
}
