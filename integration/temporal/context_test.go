package temporal

import (
	"context"
	"testing"

	"github.com/mountayaapp/helix.go/event"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// mockValueGetter satisfies the valueGetter interface, allowing us to test the
// lookups shared by workflow and activity contexts without a real
// workflow.Context.
type mockValueGetter struct {
	values map[any]any
}

func (m *mockValueGetter) Value(key any) any {
	return m.values[key]
}

func TestEventFromContext(t *testing.T) {
	e := event.Event{Name: "order.placed"}

	testcases := []struct {
		name   string
		values map[any]any
		want   event.Event
		ok     bool
	}{
		{name: "an Event is found", values: map[any]any{eventCtxKey: e}, want: e, ok: true},
		{name: "no Event", values: map[any]any{}},
		{name: "a nil value", values: map[any]any{eventCtxKey: nil}},
		{name: "a value of another type", values: map[any]any{eventCtxKey: 42}},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := eventFromContext(&mockValueGetter{values: tc.values})

			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestEventFromActivity_ReadsOnlyTheHeaderEvent(t *testing.T) {
	// An Event set by the activity's own code with event.NewContext is not one the
	// activity received.
	ctx := event.NewContext(t.Context(), event.Event{Name: "local"})

	_, ok := EventFromActivity(ctx)
	assert.False(t, ok)

	ctx = context.WithValue(ctx, eventCtxKey, event.Event{Name: "received"})

	e, ok := EventFromActivity(ctx)
	assert.True(t, ok)
	assert.Equal(t, "received", e.Name)
}

func TestSpanFromContext(t *testing.T) {
	_, span := noop.NewTracerProvider().Tracer("test").Start(t.Context(), "test")

	assert.Equal(t, span, spanFromContext(&mockValueGetter{values: map[any]any{spanCtxKey: span}}))
	assert.Nil(t, spanFromContext(&mockValueGetter{values: map[any]any{}}))
	assert.Nil(t, spanFromContext(&mockValueGetter{values: map[any]any{spanCtxKey: "not a span"}}))
	assert.Nil(t, spanFromContext(&mockValueGetter{values: map[any]any{spanCtxKey: (trace.Span)(nil)}}))
}
