package temporal

import (
	"context"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

/*
healthCheckTimeout is the maximum duration for a health check request when the
caller's context has no deadline set.
*/
const healthCheckTimeout = 5 * time.Second

/*
checkHealth performs a health check against the Temporal server using the given
client. It guards against contexts with no deadline to prevent indefinite hangs.
Returns 200 on success, 503 with an errorstack on failure.
*/
func checkHealth(ctx context.Context, c client.Client) (int, error) {
	stack := errorstack.New("Integration is not in a healthy state", errorstack.WithIntegration(identifier))

	// Guard against contexts with no deadline to prevent indefinite hangs.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, healthCheckTimeout)
		defer cancel()
	}

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
