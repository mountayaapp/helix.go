package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

/*
checkHealth performs a health check against the Temporal server using the given
client. Returns 200 on success, 503 with an errorstack on failure. The caller
is responsible for providing a context with an appropriate deadline.
*/
func checkHealth(ctx context.Context, c client.Client) (int, error) {
	stack := errorstack.New("Integration is not in a healthy state", errorstack.WithIntegration(identifier))

	_, err := c.CheckHealth(ctx, &client.CheckHealthRequest{})
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: err.Error(),
		})

		return 503, stack
	}

	return 200, nil
}

/*
Pre-computed span names to avoid allocations on every call.
*/
var (
	attrKeyWorkerTaskQueue   = attribute.Key(identifier + ".worker.taskqueue")
	attrKeyWorkflowNamespace = attribute.Key(identifier + ".workflow.namespace")
	attrKeyWorkflowType      = attribute.Key(identifier + ".workflow.type")
	attrKeyWorkflowAttempt   = attribute.Key(identifier + ".workflow.attempt")
	attrKeyActivityType      = attribute.Key(identifier + ".activity.type")
	attrKeyActivityAttempt   = attribute.Key(identifier + ".activity.attempt")
	attrKeyServerAddress     = attribute.Key(identifier + ".server.address")
	attrKeyNamespace         = attribute.Key(identifier + ".namespace")
)

/*
setWorkflowAttributes sets workflow attributes to a trace span. It uses the trace
type from the OpenTelemetry package since this happens in the interceptor and
we only have access via type assertion.
*/
func setWorkflowAttributes(span oteltrace.Span, info *workflow.Info) {
	if info != nil {
		span.SetAttributes(
			attrKeyWorkerTaskQueue.String(info.TaskQueueName),
			attrKeyWorkflowNamespace.String(info.Namespace),
			attrKeyWorkflowType.String(info.WorkflowType.Name),
			attrKeyWorkflowAttempt.Int(int(info.Attempt)),
		)
	}
}

/*
setActivityAttributes sets activity attributes to a trace span. It uses the trace
type from the OpenTelemetry package since this happens in the interceptor and
we only have access via type assertion.
*/
func setActivityAttributes(span oteltrace.Span, info activity.Info) {
	span.SetAttributes(
		attrKeyWorkerTaskQueue.String(info.TaskQueue),
		attrKeyWorkflowNamespace.String(info.Namespace),
		attrKeyWorkflowType.String(info.WorkflowType.Name),
		attrKeyActivityType.String(info.ActivityType.Name),
		attrKeyActivityAttempt.Int(int(info.Attempt)),
	)
}
