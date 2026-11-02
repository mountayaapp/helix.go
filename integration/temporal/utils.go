package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"go.opentelemetry.io/otel/attribute"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

// checkHealth performs a health check against the Temporal server using the given
// client. The caller is responsible for providing a context with an appropriate
// deadline.
func checkHealth(ctx context.Context, c client.Client) error {
	_, err := c.CheckHealth(ctx, &client.CheckHealthRequest{})

	return integration.Unhealthy(err)
}

// Pre-computed attribute keys to avoid allocations on every call.
var (
	attrKeyServerAddress   = integration.Key(identifier, "server.address")
	attrKeyNamespace       = integration.Key(identifier, "namespace")
	attrKeyTaskQueue       = integration.Key(identifier, "worker.task_queue")
	attrKeyWorkflowID      = integration.Key(identifier, "workflow.id")
	attrKeyWorkflowRunID   = integration.Key(identifier, "workflow.run_id")
	attrKeyWorkflowType    = integration.Key(identifier, "workflow.type")
	attrKeyWorkflowAttempt = integration.Key(identifier, "workflow.attempt")
	attrKeyActivityID      = integration.Key(identifier, "activity.id")
	attrKeyActivityType    = integration.Key(identifier, "activity.type")
	attrKeyActivityAttempt = integration.Key(identifier, "activity.attempt")
	attrKeySignalType      = integration.Key(identifier, "signal.type")
	attrKeyQueryType       = integration.Key(identifier, "query.type")
	attrKeyUpdateID        = integration.Key(identifier, "update.id")
	attrKeyUpdateType      = integration.Key(identifier, "update.type")
	attrKeyScheduleID      = integration.Key(identifier, "schedule.id")
	attrKeyNexusOperation  = integration.Key(identifier, "nexus.operation")
)

// workflowAttributes appends to dst the attributes of the workflow run info
// describes that the SDK's own span tags leave out.
//
// The namespace is not among them: every span already carries temporal.namespace
// from the connection's configuration, and a workflow runs in the namespace its
// worker connected to.
func workflowAttributes(dst []attribute.KeyValue, info *workflow.Info) []attribute.KeyValue {
	if info == nil {
		return dst
	}

	return append(dst,
		attrKeyTaskQueue.String(info.TaskQueueName),
		attrKeyWorkflowAttempt.Int(int(info.Attempt)),
	)
}

// activityAttributes appends to dst the attributes of the activity execution
// info describes that the SDK's own span tags leave out, including the type of
// the workflow that scheduled it.
func activityAttributes(dst []attribute.KeyValue, info activity.Info) []attribute.KeyValue {
	dst = append(dst,
		attrKeyTaskQueue.String(info.TaskQueue),
		attrKeyActivityAttempt.Int(int(info.Attempt)),
	)

	if info.WorkflowType != nil && info.WorkflowType.Name != "" {
		dst = append(dst, attrKeyWorkflowType.String(info.WorkflowType.Name))
	}

	return dst
}

// rebase moves the validation entries a configuration reports under "config" to
// prefix, so that a ConfigClient nested in a ConfigWorker reports its entries at
// the path a caller finds them: config.client.address rather than config.address.
//
// Entries are rewritten in place and returned.
func rebase(entries []errorstack.Entry, prefix ...any) []errorstack.Entry {
	for i, entry := range entries {
		rest := entry.Path
		if len(rest) > 0 && rest[0] == "config" {
			rest = rest[1:]
		}

		path := make([]any, 0, len(prefix)+len(rest))
		path = append(path, prefix...)
		path = append(path, rest...)

		entries[i].Path = path
	}

	return entries
}
