package temporal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

type orderInput struct {
	OrderID string
}

type orderResult struct {
	Charged bool
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
)

func TestHandles_NameRoundTrip(t *testing.T) {
	assert.Equal(t, "process-order", testWorkflow.Name())
	assert.Equal(t, "charge-user", testActivity.Name())
	assert.Equal(t, "charge-each", testRepeatable.Name())
	assert.Equal(t, "cancel-order", testSignal.Name())
}

func TestHandles_ZeroValueIsUsableAsAHandle(t *testing.T) {
	var wf Workflow[orderInput, orderResult]
	assert.Equal(t, "", wf.Name())
}

func TestWorkflowRun_MetadataOnNilReceiver(t *testing.T) {
	var wr *WorkflowRun[orderResult]
	assert.Nil(t, wr.Metadata())
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
	_ = c.SignalWorkflow(ctx, testSignal, "workflow-id", "run-id", cancelPayload{})
	_ = c.CreateSchedule(ctx, testWorkflow, ScheduleOptions{})
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
}
