package event

import (
	"context"

	"go.opentelemetry.io/otel/baggage"
)

/*
eventKeyIdentifier is the unique internal type to get/set an Event when
interacting with a Go context.
*/
type eventKeyIdentifier struct{}

var eventKey eventKeyIdentifier

/*
EventFromContext returns the Event found in the context passed, if any. If no
Event has been found, it tries to find and build one if a Baggage was found in
the context. Returns true if an Event has been found, false otherwise.
*/
func EventFromContext(ctx context.Context) (Event, bool) {
	e, ok := ctx.Value(eventKey).(Event)
	if !ok {
		return eventFromBaggage(baggage.FromContext(ctx))
	}

	return e, true
}

/*
ContextWithEvent returns a copy of the context passed with the Event associated
to it.
*/
func ContextWithEvent(ctx context.Context, e Event) context.Context {
	return context.WithValue(ctx, eventKey, e)
}

/*
eventFromBaggage returns the Event found in the Baggage passed, if any. Returns
true if an Event has been found, false otherwise. An event is considered found
if — and only if — the name is not empty.
*/
func eventFromBaggage(b baggage.Baggage) (Event, bool) {
	m := make(map[string]string)
	for _, member := range b.Members() {
		m[member.Key()] = member.Value()
	}

	e := FromFlatMap(m)
	if e.Name == "" {
		return e, false
	}

	return e, true
}
