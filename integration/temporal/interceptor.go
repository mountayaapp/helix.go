package temporal

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
)

// Ensure attributesInterceptor complies to the interceptor.ClientInterceptor type.
var _ interceptor.ClientInterceptor = (*attributesInterceptor)(nil)

// attributesInterceptor stamps the entry span of a workflow run or an activity
// execution with what the SDK's span tags leave out: the Event the work arrived
// with, and the task queue and attempt of the run.
//
// It is an interceptor, rather than a step of customPropagator, because of when
// each runs. The SDK extracts headers before any interceptor runs, so the span the
// tracing interceptor goes on to open does not exist yet when the propagator sees
// the Event: attributes the propagator stamped landed on no span at all. An
// interceptor placed after the tracing interceptor runs inside it, with the span
// already on the context.
//
// Only the entry spans are stamped, the way every server integration stamps the
// Event on the span it accepts work with and not on the spans under it.
type attributesInterceptor struct {
	interceptor.InterceptorBase
}

// InterceptActivity wraps the inbound activity interceptor chain.
func (*attributesInterceptor) InterceptActivity(
	ctx context.Context,
	next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	i := &activityAttributesInterceptor{}
	i.Next = next

	return i
}

// InterceptWorkflow wraps the inbound workflow interceptor chain.
func (*attributesInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	i := &workflowAttributesInterceptor{}
	i.Next = next

	return i
}

// activityAttributesInterceptor stamps the span of an activity execution.
type activityAttributesInterceptor struct {
	interceptor.ActivityInboundInterceptorBase
}

// ExecuteActivity stamps the span the tracing interceptor opened for the activity,
// then runs it.
func (i *activityAttributesInterceptor) ExecuteActivity(
	ctx context.Context,
	in *interceptor.ExecuteActivityInput,
) (any, error) {
	if span := spanFromContext(ctx); span != nil && span.IsRecording() {
		attrs := activityAttributes(make([]attribute.KeyValue, 0, 3), activity.GetInfo(ctx))
		if e, ok := eventFromContext(ctx); ok {
			attrs = e.AppendAttrs(attrs)
		}

		span.SetAttributes(attrs...)
	}

	return i.Next.ExecuteActivity(ctx, in)
}

// workflowAttributesInterceptor stamps the span of a workflow run.
type workflowAttributesInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
}

// ExecuteWorkflow stamps the span the tracing interceptor opened for the workflow
// run, then runs it.
//
// Setting attributes is not a workflow command, so it is safe on replay: a replay
// opens a span of its own, and stamps it the same way.
func (i *workflowAttributesInterceptor) ExecuteWorkflow(
	ctx workflow.Context,
	in *interceptor.ExecuteWorkflowInput,
) (any, error) {
	if span := spanFromContext(ctx); span != nil && span.IsRecording() {
		attrs := workflowAttributes(make([]attribute.KeyValue, 0, 2), workflow.GetInfo(ctx))
		if e, ok := eventFromContext(ctx); ok {
			attrs = e.AppendAttrs(attrs)
		}

		span.SetAttributes(attrs...)
	}

	return i.Next.ExecuteWorkflow(ctx, in)
}
