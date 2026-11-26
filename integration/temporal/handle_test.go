package temporal

import (
	"context"
	"testing"

	"github.com/mountaya/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/workflow"
)

type orderInput struct {
	OrderID string
}

type orderResult struct {
	Charged bool
	Items   []string `json:",omitempty"`
}

type chargeConfig struct {
	Retries int
}

type cancelPayload struct {
	Reason string
}

// Handles are declared at package level on purpose: it is the documented usage,
// and it proves they are constructible at init time without a live connection.
var (
	testWorkflow   = NewWorkflow[orderInput, orderResult]("process-order")
	testActivity   = NewActivity[orderInput, orderResult]("charge-user", workflow.ActivityOptions{})
	testRepeatable = NewRepeatableActivity[orderInput, chargeConfig, orderResult]("charge-each", workflow.ActivityOptions{})
	testSignal     = NewSignal[cancelPayload]("cancel-order")
	testQuery      = NewQuery[NoInput, orderResult]("order-status")
	testUpdate     = NewUpdate[orderInput, orderResult]("amend-order")
)

func TestHandles_NameRoundTrip(t *testing.T) {
	assert.Equal(t, "process-order", testWorkflow.Name())
	assert.Equal(t, "charge-user", testActivity.Name())
	assert.Equal(t, "charge-each", testRepeatable.Name())
	assert.Equal(t, "cancel-order", testSignal.Name())
	assert.Equal(t, "order-status", testQuery.Name())
	assert.Equal(t, "amend-order", testUpdate.Name())
}

func TestHandles_ZeroValueIsUsableAsAHandle(t *testing.T) {
	var wf Workflow[orderInput, orderResult]
	assert.Empty(t, wf.Name())
}

func TestWorkflowRun_MetadataOnNilReceiver(t *testing.T) {
	var wr *WorkflowRun[orderResult]
	assert.Nil(t, wr.Metadata())
}

func TestWorkflowRun_Metadata(t *testing.T) {
	run := mocks.NewWorkflowRun(t)
	run.On("GetID").Return("order-1")
	run.On("GetRunID").Return("run-1")

	wr := &WorkflowRun[orderResult]{run: run}
	metadata := wr.Metadata()

	require.NotNil(t, metadata)
	assert.Equal(t, "order-1", metadata.Workflow.ID)
	assert.Equal(t, "run-1", metadata.Workflow.Run.ID)
}

func TestWorkflowRun_AwaitInto(t *testing.T) {
	t.Run("a nil destination is a validation error, not a discarded result", func(t *testing.T) {
		run := mocks.NewWorkflowRun(t)
		wr := &WorkflowRun[orderResult]{run: run}

		err := wr.AwaitInto(t.Context(), nil)

		var e *errorstack.Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, errorstack.CodeValidationFailed, e.Entries[0].Extensions["code"])
		run.AssertNotCalled(t, "Get", mock.Anything, mock.Anything)
	})

	t.Run("the result is decoded into the destination given", func(t *testing.T) {
		run := mocks.NewWorkflowRun(t)
		run.On("Get", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			args.Get(1).(*orderResult).Charged = true
		}).Return(nil)

		dst := orderResult{Items: []string{"kept"}}
		wr := &WorkflowRun[orderResult]{run: run}

		require.NoError(t, wr.AwaitInto(t.Context(), &dst))
		assert.Equal(t, orderResult{Charged: true, Items: []string{"kept"}}, dst)
	})
}

// assertInference is never called. It exists so the compiler verifies that every
// generic method infers [Input], [Result], [Config] and [Payload] from the handle
// alone, with no explicit instantiation at any call site.
//
// Passing a value of the wrong type to any of these is a build failure, which is the
// property this refactor exists to provide:
//
//	c.ExecuteWorkflow(ctx, testWorkflow, opts, cancelPayload{})
//	  // in call to c.ExecuteWorkflow, type cancelPayload of cancelPayload{}
//	  // does not match inferred type orderInput for Input
//
//nolint:unused
func assertInference(ctx context.Context, c *Client, w *Worker, wctx workflow.Context) {
	_, _ = c.ExecuteWorkflow(ctx, testWorkflow, client.StartWorkflowOptions{}, orderInput{})
	_ = c.WorkflowRun(ctx, testWorkflow, "workflow-id", "run-id")
	_ = c.SignalWorkflow(ctx, testSignal, "workflow-id", "run-id", cancelPayload{})
	_, _ = c.SignalWithStartWorkflow(ctx, testWorkflow, testSignal, client.StartWorkflowOptions{}, orderInput{}, cancelPayload{})
	_, _ = c.QueryWorkflow(ctx, testQuery, "workflow-id", "run-id", NoInput{})
	_, _ = c.UpdateWorkflow(ctx, testUpdate, "workflow-id", "run-id", orderInput{})
	_ = c.CreateSchedule(ctx, testWorkflow, ScheduleOptions{}, orderInput{})
	_, _, _ = c.WorkflowResultIfClosed(ctx, testWorkflow, "workflow-id", "run-id")

	w.RegisterWorkflow(testWorkflow, func(workflow.Context, orderInput) (orderResult, error) {
		return orderResult{}, nil
	})
	w.RegisterActivity(testActivity, func(context.Context, orderInput) (orderResult, error) {
		return orderResult{}, nil
	})
	w.RegisterRepeatableActivity(testRepeatable, func(context.Context, orderInput, chargeConfig) (orderResult, error) {
		return orderResult{}, nil
	})

	_ = testActivity.Execute(wctx, orderInput{})
	_ = testRepeatable.Execute(wctx, orderInput{}, chargeConfig{})
	_ = testSignal.Channel(wctx)
	_, _ = testSignal.Receive(wctx)
	_ = testQuery.SetHandler(wctx, func(NoInput) (orderResult, error) {
		return orderResult{}, nil
	})
	_ = testUpdate.SetHandler(wctx, func(workflow.Context, orderInput) (orderResult, error) {
		return orderResult{}, nil
	}, nil)
}
