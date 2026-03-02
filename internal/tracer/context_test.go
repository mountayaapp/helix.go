package tracer

import (
	"net/url"
	"testing"

	"github.com/mountayaapp/helix.go/event"

	"github.com/stretchr/testify/assert"
)

func TestFromContextToBaggageMembers_NoEvent(t *testing.T) {
	ctx := t.Context()

	members := FromContextToBaggageMembers(ctx)

	assert.Empty(t, members)
}

func TestFromContextToBaggageMembers_WithEvent(t *testing.T) {
	e := event.Event{
		Name:   "subscribed",
		UserId: "user_123",
	}

	ctx := event.ContextWithEvent(t.Context(), e)
	members := FromContextToBaggageMembers(ctx)

	assert.NotEmpty(t, members)

	found := make(map[string]string)
	for _, m := range members {
		found[m.Key()] = m.Value()
	}

	assert.Equal(t, "subscribed", found["event.name"])
	assert.Equal(t, "user_123", found["event.user_id"])
}

func TestFromContextToBaggageMembers_WithParams(t *testing.T) {
	e := event.Event{
		Name: "search",
		Params: url.Values{
			"filters": []string{"a", "b"},
		},
	}

	ctx := event.ContextWithEvent(t.Context(), e)
	members := FromContextToBaggageMembers(ctx)

	found := make(map[string]string)
	for _, m := range members {
		found[m.Key()] = m.Value()
	}

	assert.Equal(t, "a", found["event.params.filters.0"])
	assert.Equal(t, "b", found["event.params.filters.1"])
}

func TestFromContextToBaggageMembers_WithMeta(t *testing.T) {
	e := event.Event{
		Name: "click",
		Meta: map[string]string{
			"source": "web",
			"page":   "home",
		},
	}

	ctx := event.ContextWithEvent(t.Context(), e)
	members := FromContextToBaggageMembers(ctx)

	found := make(map[string]string)
	for _, m := range members {
		found[m.Key()] = m.Value()
	}

	assert.Equal(t, "web", found["event.meta.source"])
	assert.Equal(t, "home", found["event.meta.page"])
}

func TestFromContextToSpanAttributes_NoEvent(t *testing.T) {
	ctx := t.Context()

	attrs := FromContextToSpanAttributes(ctx)

	assert.Empty(t, attrs)
}

func TestFromContextToSpanAttributes_WithEvent(t *testing.T) {
	e := event.Event{
		Name:   "subscribed",
		UserId: "user_123",
	}

	ctx := event.ContextWithEvent(t.Context(), e)
	attrs := FromContextToSpanAttributes(ctx)

	assert.NotEmpty(t, attrs)

	found := make(map[string]string)
	for _, a := range attrs {
		found[string(a.Key)] = a.Value.Emit()
	}

	assert.Equal(t, "subscribed", found["event.name"])
	assert.Equal(t, "user_123", found["event.user_id"])
}

func TestFromContextToSpanAttributes_WithParams(t *testing.T) {
	e := event.Event{
		Name: "search",
		Params: url.Values{
			"query": []string{"hello"},
		},
	}

	ctx := event.ContextWithEvent(t.Context(), e)
	attrs := FromContextToSpanAttributes(ctx)

	found := make(map[string]string)
	for _, a := range attrs {
		found[string(a.Key)] = a.Value.Emit()
	}

	assert.Equal(t, "hello", found["event.params.query.0"])
}

func TestFromContextToSpanAttributes_WithApp(t *testing.T) {
	e := event.Event{
		Name: "startup",
		App: event.App{
			Name:    "my-app",
			Version: "1.0.0",
		},
	}

	ctx := event.ContextWithEvent(t.Context(), e)
	attrs := FromContextToSpanAttributes(ctx)

	found := make(map[string]string)
	for _, a := range attrs {
		found[string(a.Key)] = a.Value.Emit()
	}

	assert.Equal(t, "my-app", found["event.app.name"])
	assert.Equal(t, "1.0.0", found["event.app.version"])
}

func TestFromContextToSpanAttributes_EmptyEvent(t *testing.T) {

	// An empty Event (no Name) won't be found by EventFromContext because
	// eventFromBaggage checks for non-empty Name.
	e := event.Event{}

	ctx := event.ContextWithEvent(t.Context(), e)
	attrs := FromContextToSpanAttributes(ctx)

	// ContextWithEvent directly stores in context, so EventFromContext finds it
	// via the context key (not baggage). But ToFlatMap(Event{}) returns an empty
	// map after cleaning zero values, so no attributes are produced.
	assert.Empty(t, attrs)
}
