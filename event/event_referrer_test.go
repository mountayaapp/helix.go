package event

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
)

func TestInjectEventReferrerToFlatMap(t *testing.T) {
	testcases := []struct {
		name     string
		input    Referrer
		expected map[string]string
	}{
		{
			name:     "empty struct",
			input:    Referrer{},
			expected: map[string]string{},
		},
		{
			name: "all fields populated",
			input: Referrer{
				Type: "referrer_type_test",
				Name: "referrer_name_test",
				URL:  "referrer_url_test",
				Link: "referrer_link_test",
			},
			expected: map[string]string{
				"event.referrer.type": "referrer_type_test",
				"event.referrer.name": "referrer_name_test",
				"event.referrer.url":  "referrer_url_test",
				"event.referrer.link": "referrer_link_test",
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var actual = make(map[string]string)
			maps.Copy(actual, tc.expected)

			injectEventReferrerToFlatMap(tc.input, tc.expected)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestApplyEventReferrerFromBaggageMember(t *testing.T) {
	testcases := []struct {
		name     string
		input    func() baggage.Member
		expected *Event
	}{
		{
			name: "unknown field",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.referrer.unknown", "anything")
				return m
			},
			expected: &Event{
				Referrer: Referrer{},
			},
		},
		{
			name: "type",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.referrer.type", "referrer_type_test")
				return m
			},
			expected: &Event{
				Referrer: Referrer{
					Type: "referrer_type_test",
				},
			},
		},
		{
			name: "name",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.referrer.name", "referrer_name_test")
				return m
			},
			expected: &Event{
				Referrer: Referrer{
					Name: "referrer_name_test",
				},
			},
		},
		{
			name: "url",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.referrer.url", "referrer_url_test")
				return m
			},
			expected: &Event{
				Referrer: Referrer{
					URL: "referrer_url_test",
				},
			},
		},
		{
			name: "link",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.referrer.link", "referrer_link_test")
				return m
			},
			expected: &Event{
				Referrer: Referrer{
					Link: "referrer_link_test",
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := new(Event)
			applyEventReferrerFromBaggageMember(tc.input(), actual)

			assert.Equal(t, tc.expected, actual)
		})
	}
}
