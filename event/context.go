package event

import (
	"context"

	"go.opentelemetry.io/otel/baggage"
)

// eventKeyIdentifier is the unique internal type to get/set an Event when
// interacting with a Go context.
type eventKeyIdentifier struct{}

var eventKey eventKeyIdentifier

// FromContext returns the Event found in the context passed, if any. If no
// Event has been found, it tries to rebuild one from the Baggage found in the
// context. Returns true if an Event has been found, false otherwise.
//
// An Event rebuilt from baggage carries only the fields Propagated sends — the
// event and application identity, and the organization and tenant identifiers —
// and nothing else, whatever the baggage holds.
//
// Treat an Event rebuilt from baggage as client input. On a server reachable from
// outside, the baggage is whatever the caller put in its request headers: it is
// fine for correlating telemetry, and not a basis for an authorization decision.
// The HTTP server integrations can be told to ignore inbound baggage entirely
// with their Config.PublicEndpoint.
func FromContext(ctx context.Context) (Event, bool) {
	e, ok := ctx.Value(eventKey).(Event)
	if !ok {
		return eventFromBaggage(baggage.FromContext(ctx))
	}

	return e, true
}

// NewContext returns a copy of the context passed with the Event associated
// to it.
func NewContext(ctx context.Context, e Event) context.Context {
	return context.WithValue(ctx, eventKey, e)
}

// eventFromBaggage returns the Event found in the Baggage passed, if any. Returns
// true if an Event has been found, false otherwise. An event is considered found
// if — and only if — the name is not empty.
//
// It reads the allow-listed keys and nothing else. That is not only a privacy
// rule but a robustness one: a decoder that followed whatever keys the baggage
// named would let a single header decide how much it allocates.
func eventFromBaggage(b baggage.Baggage) (Event, bool) {
	var e Event
	if b.Len() == 0 {
		return e, false
	}

	for _, f := range propagatedFields {
		*f.field(&e) = b.Member(f.key).Value()
	}

	if e.Name == "" {
		return e, false
	}

	return e, true
}
