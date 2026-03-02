package event

import (
	"context"
	"net/url"
	"testing"

	"github.com/mountayaapp/helix.go/internal/contextkey"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/baggage"
)

func TestEventFromContext(t *testing.T) {
	testcases := []struct {
		name     string
		ctx      context.Context
		baggage  func() baggage.Baggage
		expected Event
		success  bool
	}{
		{
			name:     "context value is not an Event",
			ctx:      context.WithValue(t.Context(), contextkey.Event, "not an Event"),
			expected: Event{},
			success:  false,
		},
		{
			name:     "context value is an empty Event",
			ctx:      context.WithValue(t.Context(), contextkey.Event, Event{}),
			expected: Event{},
			success:  true,
		},
		{
			name: "context value is an Event with name",
			ctx: context.WithValue(t.Context(), contextkey.Event, Event{
				Name: "testing",
			}),
			expected: Event{
				Name: "testing",
			},
			success: true,
		},
		{
			name: "event extracted from baggage",
			ctx:  t.Context(),
			baggage: func() baggage.Baggage {
				b, _ := baggage.New()
				memberName, _ := baggage.NewMember("event.name", "testing")
				memberParamsFilters0, _ := baggage.NewMember("event.params.filters.0", "a")
				memberParamsFilters1, _ := baggage.NewMember("event.params.filters.1", "b")
				memberParamsQuery0, _ := baggage.NewMember("event.params.query.0", "search_query")

				b, _ = b.SetMember(memberName)
				b, _ = b.SetMember(memberParamsFilters0)
				b, _ = b.SetMember(memberParamsFilters1)
				b, _ = b.SetMember(memberParamsQuery0)
				return b
			},
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
			if tc.baggage != nil {
				tc.ctx = baggage.ContextWithBaggage(tc.ctx, tc.baggage())
			}

			actual, ok := EventFromContext(tc.ctx)

			assert.Equal(t, tc.expected, actual)
			assert.Equal(t, tc.success, ok)
		})
	}
}

func TestEventFromContext_PrefersContextOverBaggage(t *testing.T) {

	// When both context value and baggage are present, context value wins.
	e := Event{Name: "from_context"}
	ctx := ContextWithEvent(t.Context(), e)

	memberName, _ := baggage.NewMember("event.name", "from_baggage")
	b, _ := baggage.New(memberName)
	ctx = baggage.ContextWithBaggage(ctx, b)

	actual, ok := EventFromContext(ctx)

	assert.True(t, ok)
	assert.Equal(t, "from_context", actual.Name)
}

func TestEventFromContext_EmptyBaggageReturnsFalse(t *testing.T) {

	// Baggage without event.name should return false.
	m, _ := baggage.NewMember("event.user_id", "user_123")
	b, _ := baggage.New(m)
	ctx := baggage.ContextWithBaggage(t.Context(), b)

	_, ok := EventFromContext(ctx)

	assert.False(t, ok)
}

func TestEventFromContext_BaggageWithMeta(t *testing.T) {
	name, _ := baggage.NewMember("event.name", "click")
	meta, _ := baggage.NewMember("event.meta.source", "web")
	b, _ := baggage.New(name, meta)
	ctx := baggage.ContextWithBaggage(t.Context(), b)

	actual, ok := EventFromContext(ctx)

	assert.True(t, ok)
	assert.Equal(t, "click", actual.Name)
	assert.Equal(t, "web", actual.Meta["source"])
}

func TestContextWithEvent(t *testing.T) {
	testcases := []struct {
		name     string
		ctx      context.Context
		input    Event
		expected context.Context
		success  bool
	}{
		{
			name: "stores event in context",
			ctx:  t.Context(),
			input: Event{
				Name: "testing",
			},
			expected: context.WithValue(t.Context(), contextkey.Event, Event{
				Name: "testing",
			}),
			success: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := ContextWithEvent(tc.ctx, tc.input)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestContextWithEvent_RoundTrip(t *testing.T) {
	input := Event{
		Name:   "test_event",
		UserId: "user_123",
		Meta: map[string]string{
			"key": "value",
		},
	}

	ctx := ContextWithEvent(t.Context(), input)
	output, ok := EventFromContext(ctx)

	assert.True(t, ok)
	assert.Equal(t, input, output)
}

func TestContextWithEvent_OverwritesPrevious(t *testing.T) {
	first := Event{Name: "first"}
	second := Event{Name: "second"}

	ctx := ContextWithEvent(t.Context(), first)
	ctx = ContextWithEvent(ctx, second)

	output, ok := EventFromContext(ctx)

	assert.True(t, ok)
	assert.Equal(t, "second", output.Name)
}

func TestEventFromBaggage_EmptyName(t *testing.T) {

	// eventFromBaggage returns false when Name is empty.
	m, _ := baggage.NewMember("event.user_id", "user_123")
	b, _ := baggage.New(m)

	e, ok := eventFromBaggage(b)

	assert.False(t, ok)
	assert.Equal(t, "", e.Name)
}

func TestEventFromBaggage_WithName(t *testing.T) {
	m, _ := baggage.NewMember("event.name", "test")
	b, _ := baggage.New(m)

	e, ok := eventFromBaggage(b)

	assert.True(t, ok)
	assert.Equal(t, "test", e.Name)
}
