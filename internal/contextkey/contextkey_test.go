package contextkey

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEventKey_SetAndRetrieve(t *testing.T) {
	ctx := context.WithValue(t.Context(), Event, "test-event-value")

	actual, ok := ctx.Value(Event).(string)

	assert.True(t, ok)
	assert.Equal(t, "test-event-value", actual)
}

func TestSpanKey_SetAndRetrieve(t *testing.T) {
	ctx := context.WithValue(t.Context(), Span, "test-span-value")

	actual, ok := ctx.Value(Span).(string)

	assert.True(t, ok)
	assert.Equal(t, "test-span-value", actual)
}

func TestKeys_AreDistinct(t *testing.T) {
	ctx := context.WithValue(t.Context(), Event, "event-value")
	ctx = context.WithValue(ctx, Span, "span-value")

	eventVal, eventOk := ctx.Value(Event).(string)
	spanVal, spanOk := ctx.Value(Span).(string)

	assert.True(t, eventOk)
	assert.True(t, spanOk)
	assert.Equal(t, "event-value", eventVal)
	assert.Equal(t, "span-value", spanVal)
}

func TestEventKey_MissingFromContext(t *testing.T) {
	ctx := t.Context()

	actual := ctx.Value(Event)

	assert.Nil(t, actual)
}

func TestSpanKey_MissingFromContext(t *testing.T) {
	ctx := t.Context()

	actual := ctx.Value(Span)

	assert.Nil(t, actual)
}

func TestEventKey_TypeSafety(t *testing.T) {

	// Setting a value with a different key type should not be retrievable through
	// the Event key.
	type otherKey struct{}
	ctx := context.WithValue(t.Context(), otherKey{}, "some-value")

	actual := ctx.Value(Event)

	assert.Nil(t, actual)
}

func TestSpanKey_TypeSafety(t *testing.T) {
	type otherKey struct{}
	ctx := context.WithValue(t.Context(), otherKey{}, "some-value")

	actual := ctx.Value(Span)

	assert.Nil(t, actual)
}

func TestEventKey_OverwriteValue(t *testing.T) {
	ctx := context.WithValue(t.Context(), Event, "first")
	ctx = context.WithValue(ctx, Event, "second")

	actual, ok := ctx.Value(Event).(string)

	assert.True(t, ok)
	assert.Equal(t, "second", actual)
}

func TestKeys_StoreStructValues(t *testing.T) {
	type eventData struct {
		Name string
	}
	type spanData struct {
		TraceID string
	}

	ctx := context.WithValue(t.Context(), Event, eventData{Name: "test"})
	ctx = context.WithValue(ctx, Span, spanData{TraceID: "abc123"})

	eventVal, eventOk := ctx.Value(Event).(eventData)
	spanVal, spanOk := ctx.Value(Span).(spanData)

	assert.True(t, eventOk)
	assert.True(t, spanOk)
	assert.Equal(t, "test", eventVal.Name)
	assert.Equal(t, "abc123", spanVal.TraceID)
}
