package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/event"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/workflow"
)

/*
valueGetter is satisfied by both context.Context and workflow.Context, allowing
shared logic for extracting values from either context type.
*/
type valueGetter interface {
	Value(key any) any
}

/*
setEventSpanAttributes sets all event fields as span attributes in a single
batched call to avoid per-attribute lock acquisition overhead.
*/
func setEventSpanAttributes(span oteltrace.Span, mapped map[string]string) {
	attrs := make([]attribute.KeyValue, 0, len(mapped))
	for k, v := range mapped {
		attrs = append(attrs, attribute.String(k, v))
	}

	span.SetAttributes(attrs...)
}

/*
eventFromContext tries to retrieve an Event from the given context. Returns true
if an Event has been found, false otherwise.

If an Event was found and a span is present, event fields are added to the span
attributes.
*/
func eventFromContext(ctx valueGetter) (event.Event, bool) {
	val := ctx.Value(eventCtxKey)
	if val == nil {
		return event.Event{}, false
	}

	e, ok := val.(event.Event)
	if !ok {
		return event.Event{}, false
	}

	if span, ok := ctx.Value(spanCtxKey).(oteltrace.Span); ok && span != nil {
		setEventSpanAttributes(span, event.ToFlatMap(e))
	}

	return e, true
}

/*
EventFromWorkflow tries to retrieve an Event from the workflow's context. Returns
true if an Event has been found, false otherwise.

If an Event was found and a span is present, event fields are added to the span
attributes.
*/
func EventFromWorkflow(ctx workflow.Context) (event.Event, bool) {
	return eventFromContext(ctx)
}

/*
EventFromActivity tries to retrieve an Event from the activity's context. Returns
true if an Event has been found, false otherwise.

If an Event was found and a span is present, event fields are added to the span
attributes.
*/
func EventFromActivity(ctx context.Context) (event.Event, bool) {
	return eventFromContext(ctx)
}
