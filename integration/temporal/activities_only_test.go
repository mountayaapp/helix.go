package temporal

import (
	"context"
	"testing"
	"time"

	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

// TestAWorkerServingActivitiesAlonePollsNoWorkflowTask pins
// ConfigWorker.DisableWorkflowWorker against a server. Two workers built by New
// share one Service, the second serving activities alone. A workflow run by the
// first schedules an activity on the second's task queue, and the second runs it.
// The server then sees an activity poller on that queue and no workflow poller,
// where the first worker's queue has one of each. Registering a workflow on the
// second panics.
func TestAWorkerServingActivitiesAlonePollsNoWorkflowTask(t *testing.T) {
	address := startTemporal(t)
	svc, _ := servicetest.New(t)

	c, orders, err := New(t.Context(), svc, ConfigWorker{
		Client:                     ConfigClient{Address: address},
		TaskQueue:                  "orders",
		MaxConcurrentWorkflowTasks: 2,
	})
	require.NoError(t, err)

	_, heavy, err := New(t.Context(), svc, ConfigWorker{
		Client:                ConfigClient{Address: address},
		TaskQueue:             "orders-heavy",
		DisableWorkflowWorker: true,
	})
	require.NoError(t, err)

	charge := NewActivity[string, string]("charge-heavy", workflow.ActivityOptions{
		TaskQueue:           "orders-heavy",
		StartToCloseTimeout: time.Minute,
	})
	heavy.RegisterActivity(charge, func(ctx context.Context, input string) (string, error) {
		return activity.GetInfo(ctx).TaskQueue + ":" + input, nil
	})

	assert.Panics(t, func() {
		heavy.RegisterWorkflow(NewWorkflow[string, string]("refused-workflow"), func(workflow.Context, string) (string, error) {
			return "", nil
		})
	}, "a worker serving activities alone took a workflow")

	order := NewWorkflow[string, string]("order-workflow")
	orders.RegisterWorkflow(order, func(ctx workflow.Context, input string) (string, error) {
		return charge.Execute(ctx, input).Await(ctx)
	})

	require.NoError(t, svc.Start(t.Context()))
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	run, err := c.ExecuteWorkflow(t.Context(), order, client.StartWorkflowOptions{ID: "order", TaskQueue: "orders"}, "input")
	require.NoError(t, err)

	result, err := run.Await(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "orders-heavy:input", result, "the activity did not run on the queue its options name")

	pollers := func(queue string, kind enumspb.TaskQueueType) int {
		described, err := c.client.DescribeTaskQueue(t.Context(), queue, kind)
		require.NoError(t, err)

		return len(described.GetPollers())
	}

	// The control: the server reports a workflow poller of a worker that polls
	// them, so the zero below is the worker's, not the server's.
	require.Eventually(t, func() bool { return pollers("orders", enumspb.TASK_QUEUE_TYPE_WORKFLOW) > 0 },
		10*time.Second, 100*time.Millisecond, "the server reports no workflow poller of the worker that runs the workflow")

	assert.Positive(t, pollers("orders-heavy", enumspb.TASK_QUEUE_TYPE_ACTIVITY), "the server reports no activity poller on the activities' queue")
	assert.Zero(t, pollers("orders-heavy", enumspb.TASK_QUEUE_TYPE_WORKFLOW), "the worker serving activities alone polled workflow tasks")
}
