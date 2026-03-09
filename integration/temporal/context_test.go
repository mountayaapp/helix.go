package temporal

import (
	"context"
	"testing"

	"github.com/mountayaapp/helix.go/event"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestEventFromActivity_WithEventAndSpan(t *testing.T) {
	e := event.Event{
		Name:   "activity_started",
		UserID: "user_act",
	}

	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(t.Context(), "test")

	ctx := context.WithValue(t.Context(), eventCtxKey, e)
	ctx = context.WithValue(ctx, spanCtxKey, span)

	output, ok := EventFromActivity(ctx)

	assert.True(t, ok)
	assert.Equal(t, "activity_started", output.Name)
	assert.Equal(t, "user_act", output.UserID)
}

func TestEventFromActivity_NoEvent(t *testing.T) {
	output, ok := EventFromActivity(t.Context())

	assert.False(t, ok)
	assert.Equal(t, event.Event{}, output)
}

func TestEventFromActivity_EventButNoSpan(t *testing.T) {
	e := event.Event{
		Name:   "no_span",
		UserID: "user_no_span",
	}

	ctx := context.WithValue(t.Context(), eventCtxKey, e)

	output, ok := EventFromActivity(ctx)

	assert.True(t, ok)
	assert.Equal(t, "no_span", output.Name)
}

func TestEventFromActivity_InvalidEventType(t *testing.T) {
	ctx := context.WithValue(t.Context(), eventCtxKey, "not an event")

	_, ok := EventFromActivity(ctx)

	assert.False(t, ok)
}

func TestEventFromActivity_InvalidSpanType(t *testing.T) {
	e := event.Event{
		Name: "invalid_span",
	}

	ctx := context.WithValue(t.Context(), eventCtxKey, e)
	ctx = context.WithValue(ctx, spanCtxKey, "not a span")

	output, ok := EventFromActivity(ctx)

	assert.True(t, ok)
	assert.Equal(t, "invalid_span", output.Name)
}

func TestEventFromActivity_FullEvent(t *testing.T) {
	e := event.Event{
		Name:   "process_order",
		UserID: "user_full",
		Meta:   map[string]string{"workflow": "order"},
		App:    event.App{Name: "worker", Version: "1.0.0"},
		Subscriptions: []event.Subscription{
			{ID: "sub_001", CustomerID: "cus_001"},
		},
	}

	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(t.Context(), "test")

	ctx := context.WithValue(t.Context(), eventCtxKey, e)
	ctx = context.WithValue(ctx, spanCtxKey, span)

	output, ok := EventFromActivity(ctx)

	assert.True(t, ok)
	assert.Equal(t, e.Name, output.Name)
	assert.Equal(t, e.UserID, output.UserID)
	assert.Equal(t, e.Meta, output.Meta)
	assert.Equal(t, e.App, output.App)
	assert.Len(t, output.Subscriptions, 1)
	assert.Equal(t, "sub_001", output.Subscriptions[0].ID)
}

func TestEventFromActivity_SpanReceivesAttributes(t *testing.T) {
	e := event.Event{
		Name:   "with_attributes",
		UserID: "user_attr",
		Meta:   map[string]string{"key": "value"},
	}

	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(t.Context(), "test")

	ctx := context.WithValue(t.Context(), eventCtxKey, e)
	ctx = context.WithValue(ctx, spanCtxKey, span)

	assert.NotPanics(t, func() {
		_, _ = EventFromActivity(ctx)
	})
}

func TestEventFromActivity_NilSpanValue(t *testing.T) {
	e := event.Event{
		Name: "nil_span",
	}

	ctx := context.WithValue(t.Context(), eventCtxKey, e)
	ctx = context.WithValue(ctx, spanCtxKey, (trace.Span)(nil))

	output, ok := EventFromActivity(ctx)

	assert.True(t, ok)
	assert.Equal(t, "nil_span", output.Name)
}

func TestSetEventSpanAttributes_BatchesAttributes(t *testing.T) {
	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(t.Context(), "test")

	mapped := map[string]string{
		"event.name":    "test_event",
		"event.user_id": "user_batch",
		"event.ip":      "10.0.0.1",
	}

	// Verify batched SetAttributes does not panic with multiple entries.
	assert.NotPanics(t, func() {
		setEventSpanAttributes(span, mapped)
	})
}

func TestSetEventSpanAttributes_EmptyMap(t *testing.T) {
	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(t.Context(), "test")

	assert.NotPanics(t, func() {
		setEventSpanAttributes(span, map[string]string{})
	})
}

func TestSetEventSpanAttributes_NilMap(t *testing.T) {
	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(t.Context(), "test")

	assert.NotPanics(t, func() {
		setEventSpanAttributes(span, nil)
	})
}

// mockValueGetter satisfies the valueGetter interface, allowing us to test
// eventFromContext without needing a real workflow.Context.
type mockValueGetter struct {
	values map[any]any
}

func (m *mockValueGetter) Value(key any) any {
	return m.values[key]
}

func TestEventFromContext_ViaValueGetter_WithEvent(t *testing.T) {
	e := event.Event{
		Name:   "workflow_event",
		UserID: "user_wf",
	}

	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(t.Context(), "test")

	ctx := &mockValueGetter{
		values: map[any]any{
			eventCtxKey: e,
			spanCtxKey:  span,
		},
	}

	output, ok := eventFromContext(ctx)

	assert.True(t, ok)
	assert.Equal(t, "workflow_event", output.Name)
	assert.Equal(t, "user_wf", output.UserID)
}

func TestEventFromContext_ViaValueGetter_NoEvent(t *testing.T) {
	ctx := &mockValueGetter{
		values: map[any]any{},
	}

	output, ok := eventFromContext(ctx)

	assert.False(t, ok)
	assert.Equal(t, event.Event{}, output)
}

func TestEventFromContext_ViaValueGetter_NilValue(t *testing.T) {
	ctx := &mockValueGetter{
		values: map[any]any{
			eventCtxKey: nil,
		},
	}

	output, ok := eventFromContext(ctx)

	assert.False(t, ok)
	assert.Equal(t, event.Event{}, output)
}

func TestEventFromContext_ViaValueGetter_InvalidType(t *testing.T) {
	ctx := &mockValueGetter{
		values: map[any]any{
			eventCtxKey: 42,
		},
	}

	_, ok := eventFromContext(ctx)

	assert.False(t, ok)
}
