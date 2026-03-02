package temporal

import (
	"fmt"
	"unicode"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"
)

/*
setWorkflowAttributes sets workflow attributes to a trace span. It uses the trace
type from the OpenTelemetry package since this happens in the interceptor and
we only have access via type assertion.
*/
func setWorkflowAttributes(span oteltrace.Span, info *workflow.Info) {
	if info != nil {
		span.SetAttributes(attribute.String(fmt.Sprintf("%s.worker.taskqueue", identifier), info.TaskQueueName))
		span.SetAttributes(attribute.String(fmt.Sprintf("%s.workflow.namespace", identifier), info.Namespace))
		span.SetAttributes(attribute.String(fmt.Sprintf("%s.workflow.type", identifier), info.WorkflowType.Name))
		span.SetAttributes(attribute.Int(fmt.Sprintf("%s.workflow.attempt", identifier), int(info.Attempt)))
	}
}

/*
setActivityAttributes sets activity attributes to a trace span. It uses the trace
type from the OpenTelemetry package since this happens in the interceptor and
we only have access via type assertion.
*/
func setActivityAttributes(span oteltrace.Span, info activity.Info) {
	span.SetAttributes(attribute.String(fmt.Sprintf("%s.worker.taskqueue", identifier), info.TaskQueue))
	span.SetAttributes(attribute.String(fmt.Sprintf("%s.workflow.namespace", identifier), info.WorkflowNamespace))
	span.SetAttributes(attribute.String(fmt.Sprintf("%s.workflow.type", identifier), info.WorkflowType.Name))
	span.SetAttributes(attribute.String(fmt.Sprintf("%s.activity.type", identifier), info.ActivityType.Name))
	span.SetAttributes(attribute.Int(fmt.Sprintf("%s.activity.attempt", identifier), int(info.Attempt)))
}

/*
normalizeErrorMessage normalizes an error returned by the Temporal client to match
the format of helix.go. This is only used inside Connect and New for a better
readability in the terminal. Otherwise, functions return native Temporal errors.

Example:

	"dial tcp 127.0.0.1:7233: connect: connection refused"

Becomes:

	"Dial tcp 127.0.0.1:7233: connect: connection refused"
*/
func normalizeErrorMessage(err error) string {
	var msg string = err.Error()
	runes := []rune(msg)
	runes[0] = unicode.ToUpper(runes[0])

	return string(runes)
}
