package temporal

import (
	"context"

	"github.com/mountaya/helix.go/event"

	oteltrace "go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/workflow"
)

// valueGetter is satisfied by both context.Context and workflow.Context, allowing
// shared logic for extracting values from either context type.
type valueGetter interface {
	Value(key any) any
}

// eventFromContext returns the Event the workflow or activity received in its
// header. Returns true if an Event has been found, false otherwise.
func eventFromContext(ctx valueGetter) (event.Event, bool) {
	e, ok := ctx.Value(eventCtxKey).(event.Event)

	return e, ok
}

// spanFromContext returns the span the SDK's tracing interceptor opened for the
// workflow or activity, which it keeps under spanCtxKey: the one place it can put
// a span on a workflow.Context. Returns nil when there is none.
func spanFromContext(ctx valueGetter) oteltrace.Span {
	span, _ := ctx.Value(spanCtxKey).(oteltrace.Span)

	return span
}

// EventFromWorkflow returns the Event the workflow received in its header. Returns
// true if an Event has been found, false otherwise — including when the workflow
// was started without one.
//
// The Event holds what the sending client put in the header: the allow-listed
// identifiers unless that client set ConfigClient.PropagateFullEvent. Its
// attributes are already on the workflow's span: there is nothing to stamp.
func EventFromWorkflow(ctx workflow.Context) (event.Event, bool) {
	return eventFromContext(ctx)
}

// EventFromActivity returns the Event the activity received in its header.
// Returns true if an Event has been found, false otherwise — including when the
// workflow scheduling the activity had none.
//
// The Event holds what the worker that scheduled the activity put in the header,
// under the same rule as EventFromWorkflow. The context also carries it for
// event.FromContext, so that the integrations the activity calls propagate it
// further.
func EventFromActivity(ctx context.Context) (event.Event, bool) {
	return eventFromContext(ctx)
}
