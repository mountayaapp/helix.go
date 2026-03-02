package event

import (
	"encoding/json"
	"net/url"
	"testing"

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
			name: "event with invalid key only",
			input: []byte(`{ "event": {
        "invalid_key": true
      } }`),
			expected: Event{},
			success:  true,
		},
		{
			name: "event with name and params",
			input: []byte(`{ "event": {
        "name": "testing",
        "params": {
          "filters": ["a", "b"],
          "query": ["search_query"]
        }
      } }`),
			expected: Event{
				Name: "testing",
				Params: url.Values{
					"filters": []string{"a", "b"},
					"query":   []string{"search_query"},
				},
			},
			success: true,
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

func TestEventFromJSON_WithMeta(t *testing.T) {
	input := []byte(`{
		"event": {
			"name": "click",
			"meta": {
				"source": "web",
				"campaign": "summer"
			}
		}
	}`)

	actual, ok := EventFromJSON(input)

	assert.True(t, ok)
	assert.Equal(t, "click", actual.Name)
	assert.Equal(t, map[string]string{
		"source":   "web",
		"campaign": "summer",
	}, actual.Meta)
}

func TestEventFromJSON_WithSubscriptions(t *testing.T) {
	input := []byte(`{
		"event": {
			"name": "subscribed",
			"subscriptions": [
				{
					"id": "sub_001",
					"customer_id": "cus_001",
					"product_id": "prod_001"
				}
			]
		}
	}`)

	actual, ok := EventFromJSON(input)

	assert.True(t, ok)
	assert.Equal(t, "subscribed", actual.Name)
	assert.Len(t, actual.Subscriptions, 1)
	assert.Equal(t, "sub_001", actual.Subscriptions[0].Id)
	assert.Equal(t, "cus_001", actual.Subscriptions[0].CustomerId)
	assert.Equal(t, "prod_001", actual.Subscriptions[0].ProductId)
}

func TestEventFromJSON_WithTopLevelFields(t *testing.T) {
	input := []byte(`{
		"event": {
			"name": "page_view",
			"user_id": "user_123",
			"organization_id": "org_456",
			"is_anonymous": true,
			"user_agent": "TestAgent/1.0",
			"locale": "en-US",
			"timezone": "UTC"
		}
	}`)

	actual, ok := EventFromJSON(input)

	assert.True(t, ok)
	assert.Equal(t, "page_view", actual.Name)
	assert.Equal(t, "user_123", actual.UserId)
	assert.Equal(t, "org_456", actual.OrganizationId)
	assert.True(t, actual.IsAnonymous)
	assert.Equal(t, "TestAgent/1.0", actual.UserAgent)
	assert.Equal(t, "en-US", actual.Locale)
	assert.Equal(t, "UTC", actual.Timezone)
}

func TestEventFromJSON_WithNestedApp(t *testing.T) {
	input := []byte(`{
		"event": {
			"name": "startup",
			"app": {
				"name": "my-app",
				"version": "1.0.0",
				"build_id": "abc123"
			}
		}
	}`)

	actual, ok := EventFromJSON(input)

	assert.True(t, ok)
	assert.Equal(t, "my-app", actual.App.Name)
	assert.Equal(t, "1.0.0", actual.App.Version)
	assert.Equal(t, "abc123", actual.App.BuildId)
}

func TestEventFromJSON_EmptyJSON(t *testing.T) {
	input := []byte(`{}`)

	actual, ok := EventFromJSON(input)

	assert.False(t, ok)
	assert.Equal(t, Event{}, actual)
}

func TestEventFromJSON_NullEvent(t *testing.T) {
	input := []byte(`{"event": null}`)

	actual, ok := EventFromJSON(input)

	assert.True(t, ok)
	assert.Equal(t, Event{}, actual)
}

func TestEventFromJSON_WithOtherKeys(t *testing.T) {
	input := []byte(`{
		"some_other_key": "value",
		"event": {
			"name": "test"
		},
		"another_key": 42
	}`)

	actual, ok := EventFromJSON(input)

	assert.True(t, ok)
	assert.Equal(t, "test", actual.Name)
}
