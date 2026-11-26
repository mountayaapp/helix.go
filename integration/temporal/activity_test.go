package temporal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mountaya/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// resolvedOptions is the part of workflow.ActivityOptions the tests below compare.
type resolvedOptions struct {
	TaskQueue           string
	StartToCloseTimeout time.Duration
	HeartbeatTimeout    time.Duration
	MaximumAttempts     int32
	WaitForCancellation bool
}

// resolve runs a workflow on the task queue "workflow-queue" that returns the
// activity options withDefaultActivityOptions resolves from defaults, after
// prepare has set whatever it sets on the context.
func resolve(t *testing.T, defaults workflow.ActivityOptions, prepare func(workflow.Context) workflow.Context) resolvedOptions {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterWorkflowWithOptions(func(ctx workflow.Context) (resolvedOptions, error) {
		if prepare != nil {
			ctx = prepare(ctx)
		}

		opts := workflow.GetActivityOptions(withDefaultActivityOptions(ctx, defaults))

		var attempts int32
		if opts.RetryPolicy != nil {
			attempts = opts.RetryPolicy.MaximumAttempts
		}

		return resolvedOptions{
			TaskQueue:           opts.TaskQueue,
			StartToCloseTimeout: opts.StartToCloseTimeout,
			HeartbeatTimeout:    opts.HeartbeatTimeout,
			MaximumAttempts:     attempts,
			WaitForCancellation: opts.WaitForCancellation,
		}, nil
	}, workflow.RegisterOptions{Name: "resolve"})

	env.SetStartWorkflowOptions(client.StartWorkflowOptions{TaskQueue: "workflow-queue"})
	env.ExecuteWorkflow("resolve")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var got resolvedOptions
	require.NoError(t, env.GetWorkflowResult(&got))

	return got
}

func TestWithDefaultActivityOptions(t *testing.T) {
	defaults := workflow.ActivityOptions{
		TaskQueue:           "activity-queue",
		StartToCloseTimeout: time.Minute,
		HeartbeatTimeout:    10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}

	t.Run("a context without options takes the activity's", func(t *testing.T) {
		assert.Equal(t, resolvedOptions{
			TaskQueue:           "activity-queue",
			StartToCloseTimeout: time.Minute,
			HeartbeatTimeout:    10 * time.Second,
			MaximumAttempts:     3,
		}, resolve(t, defaults, nil))
	})

	t.Run("options on the context win, field by field", func(t *testing.T) {
		got := resolve(t, defaults, func(ctx workflow.Context) workflow.Context {
			return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				TaskQueue:           "override-queue",
				StartToCloseTimeout: time.Hour,
				WaitForCancellation: true,
			})
		})

		assert.Equal(t, resolvedOptions{
			TaskQueue:           "override-queue",
			StartToCloseTimeout: time.Hour,
			HeartbeatTimeout:    10 * time.Second,
			MaximumAttempts:     3,
			WaitForCancellation: true,
		}, got)
	})

	t.Run("an activity declaring no task queue runs on the workflow's", func(t *testing.T) {
		got := resolve(t, workflow.ActivityOptions{StartToCloseTimeout: time.Minute}, nil)
		assert.Equal(t, "workflow-queue", got.TaskQueue)
	})
}

func TestActivityRun_AwaitIntoNilDestination(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	charge := NewActivity[orderInput, orderResult]("charge-user", workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	env.RegisterActivityWithOptions(func(context.Context, orderInput) (orderResult, error) {
		return orderResult{Charged: true}, nil
	}, activity.RegisterOptions{Name: charge.Name()})

	env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
		err := charge.Execute(ctx, orderInput{}).AwaitInto(ctx, nil)

		e, ok := errors.AsType[*errorstack.Error](err)
		if !ok {
			return "no validation error", nil
		}

		return e.Entries[0].Extensions["code"].(string), nil
	})

	require.NoError(t, env.GetWorkflowError())

	var code string
	require.NoError(t, env.GetWorkflowResult(&code))
	assert.Equal(t, errorstack.CodeValidationFailed, code)
}

func TestActivityRun_AwaitIntoMergesWithJSON(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	charge := NewActivity[orderInput, orderResult]("charge-user", workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	env.RegisterActivityWithOptions(func(context.Context, orderInput) (orderResult, error) {
		return orderResult{Charged: true}, nil
	}, activity.RegisterOptions{Name: charge.Name()})

	env.ExecuteWorkflow(func(ctx workflow.Context) (orderResult, error) {
		// Items is omitempty and the activity leaves it empty: it is absent from the
		// wire, and what dst held survives.
		dst := orderResult{Items: []string{"kept"}}
		err := charge.Execute(ctx, orderInput{}).AwaitInto(ctx, &dst)

		return dst, err
	})

	require.NoError(t, env.GetWorkflowError())

	var got orderResult
	require.NoError(t, env.GetWorkflowResult(&got))
	assert.Equal(t, orderResult{Charged: true, Items: []string{"kept"}}, got)
}

func TestSignal_Receive(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(testSignal.Name(), cancelPayload{Reason: "out of stock"})
	}, time.Second)

	env.ExecuteWorkflow(func(ctx workflow.Context) (cancelPayload, error) {
		payload, _ := testSignal.Receive(ctx)
		return payload, nil
	})

	require.NoError(t, env.GetWorkflowError())

	var got cancelPayload
	require.NoError(t, env.GetWorkflowResult(&got))
	assert.Equal(t, "out of stock", got.Reason)
}

func TestQueryAndUpdate_Handlers(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	var rejected, accepted bool

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(testUpdate.Name(), "rejected", &testsuite.TestUpdateCallback{
			OnReject:   func(error) { rejected = true },
			OnAccept:   func() {},
			OnComplete: func(any, error) {},
		}, orderInput{})

		env.UpdateWorkflow(testUpdate.Name(), "accepted", &testsuite.TestUpdateCallback{
			OnReject:   func(error) {},
			OnAccept:   func() { accepted = true },
			OnComplete: func(any, error) {},
		}, orderInput{OrderID: "o1"})
	}, time.Second)

	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(testQuery.Name(), NoInput{})
		require.NoError(t, err)

		var status orderResult
		require.NoError(t, value.Get(&status))
		assert.Equal(t, []string{"o1"}, status.Items)

		env.SignalWorkflow(testSignal.Name(), cancelPayload{})
	}, 2*time.Second)

	env.ExecuteWorkflow(func(ctx workflow.Context) (orderResult, error) {
		var status orderResult

		if err := testQuery.SetHandler(ctx, func(NoInput) (orderResult, error) {
			return status, nil
		}); err != nil {
			return status, err
		}

		if err := testUpdate.SetHandler(ctx, func(_ workflow.Context, input orderInput) (orderResult, error) {
			status.Items = append(status.Items, input.OrderID)
			return status, nil
		}, func(_ workflow.Context, input orderInput) error {
			if input.OrderID == "" {
				return errorstack.New("Order ID must be set")
			}

			return nil
		}); err != nil {
			return status, err
		}

		testSignal.Receive(ctx)
		return status, nil
	})

	require.NoError(t, env.GetWorkflowError())
	assert.True(t, rejected, "the validator did not reject the update")
	assert.True(t, accepted, "the update was not accepted")
}
