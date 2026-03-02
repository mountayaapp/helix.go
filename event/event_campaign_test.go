package event

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
)

func TestInjectEventCampaignToFlatMap(t *testing.T) {
	testcases := []struct {
		name     string
		input    Campaign
		expected map[string]string
	}{
		{
			name:     "empty struct",
			input:    Campaign{},
			expected: map[string]string{},
		},
		{
			name: "all fields populated",
			input: Campaign{
				Name:    "campaign_name_test",
				Source:  "campaign_source_test",
				Medium:  "campaign_medium_test",
				Term:    "campaign_term_test",
				Content: "campaign_content_test",
			},
			expected: map[string]string{
				"event.campaign.name":    "campaign_name_test",
				"event.campaign.source":  "campaign_source_test",
				"event.campaign.medium":  "campaign_medium_test",
				"event.campaign.term":    "campaign_term_test",
				"event.campaign.content": "campaign_content_test",
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var actual = make(map[string]string)
			maps.Copy(actual, tc.expected)

			injectEventCampaignToFlatMap(tc.input, tc.expected)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestApplyEventCampaignFromBaggageMember(t *testing.T) {
	testcases := []struct {
		name     string
		input    func() baggage.Member
		expected *Event
	}{
		{
			name: "unknown field",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.campaign.unknown", "anything")
				return m
			},
			expected: &Event{
				Campaign: Campaign{},
			},
		},
		{
			name: "name",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.campaign.name", "campaign_name_test")
				return m
			},
			expected: &Event{
				Campaign: Campaign{
					Name: "campaign_name_test",
				},
			},
		},
		{
			name: "source",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.campaign.source", "campaign_source_test")
				return m
			},
			expected: &Event{
				Campaign: Campaign{
					Source: "campaign_source_test",
				},
			},
		},
		{
			name: "medium",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.campaign.medium", "campaign_medium_test")
				return m
			},
			expected: &Event{
				Campaign: Campaign{
					Medium: "campaign_medium_test",
				},
			},
		},
		{
			name: "term",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.campaign.term", "campaign_term_test")
				return m
			},
			expected: &Event{
				Campaign: Campaign{
					Term: "campaign_term_test",
				},
			},
		},
		{
			name: "content",
			input: func() baggage.Member {
				m, _ := baggage.NewMember("event.campaign.content", "campaign_content_test")
				return m
			},
			expected: &Event{
				Campaign: Campaign{
					Content: "campaign_content_test",
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := new(Event)
			applyEventCampaignFromBaggageMember(tc.input(), actual)

			assert.Equal(t, tc.expected, actual)
		})
	}
}
