package temporal

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// newTestClient returns a Client over a mocked SDK client, encoding with the SDK's
// default DataConverter.
func newTestClient(t *testing.T) (*Client, *mocks.Client) {
	t.Helper()

	mockClient := mocks.NewClient(t)

	return newClient(mockClient, &ConfigClient{}), mockClient
}

// testScheduleOptions are the options CreateSchedule builds for testWorkflow with
// opts and input.
func testScheduleOptions(t *testing.T, c *Client, opts ScheduleOptions, input orderInput) client.ScheduleOptions {
	t.Helper()

	request, err := c.scheduleOptions(testWorkflow.Name(), opts, input)
	require.NoError(t, err)

	return request
}

// describedAs returns the description Temporal gives of the schedule request
// created: memo values come back as the payloads the server stores.
func describedAs(t *testing.T, request client.ScheduleOptions) *client.ScheduleDescription {
	t.Helper()

	action := *request.Action.(*client.ScheduleWorkflowAction)

	memo := make(map[string]any, len(action.Memo))
	for k, v := range action.Memo {
		payload, err := converter.GetDefaultDataConverter().ToPayload(v)
		require.NoError(t, err)

		memo[k] = payload
	}

	action.Memo = memo

	return &client.ScheduleDescription{
		Schedule: client.Schedule{
			Action: &action,
			Spec:   &client.ScheduleSpec{},
			Policy: &client.SchedulePolicies{Overlap: request.Overlap},
			State:  &client.ScheduleState{Paused: true, Note: "paused by an operator"},
		},
	}
}

func TestCreateSchedule_ScheduleDoesNotExist(t *testing.T) {
	c, mockClient := newTestClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	request := testScheduleOptions(t, c, ScheduleOptions{TaskQueue: "orders", CronExpressions: []string{"0 1 * * *"}}, orderInput{OrderID: "o1"})

	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "process-order").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(nil, serviceerror.NewNotFound("schedule not found"))
	mockScheduleClient.On("Create", mock.Anything, request).Return(mockHandle, nil)

	require.NoError(t, c.createSchedule(t.Context(), request))

	// The input reaches the scheduled workflow.
	action := request.Action.(*client.ScheduleWorkflowAction)
	assert.Equal(t, []any{orderInput{OrderID: "o1"}}, action.Args)
}

func TestCreateSchedule_ScheduleExistsUnchanged(t *testing.T) {
	c, mockClient := newTestClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	request := testScheduleOptions(t, c, ScheduleOptions{TaskQueue: "orders", CronExpressions: []string{"0 1 * * *"}}, orderInput{})

	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "process-order").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(describedAs(t, request), nil)

	require.NoError(t, c.createSchedule(t.Context(), request))

	mockScheduleClient.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	mockHandle.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}

func TestCreateSchedule_ChangedCronUpdatesTheSchedule(t *testing.T) {
	c, mockClient := newTestClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	existing := describedAs(t, testScheduleOptions(t, c, ScheduleOptions{TaskQueue: "orders", CronExpressions: []string{"0 1 * * *"}}, orderInput{}))
	request := testScheduleOptions(t, c, ScheduleOptions{TaskQueue: "orders", CronExpressions: []string{"0 2 * * *"}}, orderInput{})

	var update *client.ScheduleUpdate
	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "process-order").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(existing, nil)
	mockHandle.On("Update", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		opts := args.Get(1).(client.ScheduleUpdateOptions)

		var err error
		update, err = opts.DoUpdate(client.ScheduleUpdateInput{Description: *existing})
		require.NoError(t, err)
	}).Return(nil)

	require.NoError(t, c.createSchedule(t.Context(), request))

	require.NotNil(t, update)
	assert.Equal(t, []string{"0 2 * * *"}, update.Schedule.Spec.CronExpressions)
	assert.Equal(t, request.Action, update.Schedule.Action)

	// The schedule's state is kept: an operator's pause survives a deploy.
	assert.True(t, update.Schedule.State.Paused)
	assert.Equal(t, "paused by an operator", update.Schedule.State.Note)
}

func TestCreateSchedule_ChangedInputUpdatesTheSchedule(t *testing.T) {
	c, mockClient := newTestClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	existing := describedAs(t, testScheduleOptions(t, c, ScheduleOptions{TaskQueue: "orders"}, orderInput{OrderID: "o1"}))
	request := testScheduleOptions(t, c, ScheduleOptions{TaskQueue: "orders"}, orderInput{OrderID: "o2"})

	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "process-order").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(existing, nil)
	mockHandle.On("Update", mock.Anything, mock.Anything).Return(nil)

	require.NoError(t, c.createSchedule(t.Context(), request))
}

func TestCreateSchedule_ScheduleChangedOutsideTheCodeIsUpdated(t *testing.T) {
	c, mockClient := newTestClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	request := testScheduleOptions(t, c, ScheduleOptions{TaskQueue: "orders"}, orderInput{})

	// Same fingerprint, but the task queue was changed in the UI.
	existing := describedAs(t, request)
	existing.Schedule.Action.(*client.ScheduleWorkflowAction).TaskQueue = "elsewhere"

	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "process-order").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(existing, nil)
	mockHandle.On("Update", mock.Anything, mock.Anything).Return(nil)

	require.NoError(t, c.createSchedule(t.Context(), request))
}

func TestCreateSchedule_ScheduleWithoutFingerprintIsUpdated(t *testing.T) {
	c, mockClient := newTestClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	request := testScheduleOptions(t, c, ScheduleOptions{TaskQueue: "orders"}, orderInput{})

	// A schedule an earlier version created: it matches on everything that can be
	// compared, but carries no fingerprint, so whether its cron matches is unknown.
	existing := &client.ScheduleDescription{
		Schedule: client.Schedule{
			Action: &client.ScheduleWorkflowAction{Workflow: "process-order", TaskQueue: "orders"},
			Policy: &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP},
		},
	}

	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "process-order").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(existing, nil)
	mockHandle.On("Update", mock.Anything, mock.Anything).Return(nil)

	require.NoError(t, c.createSchedule(t.Context(), request))
}

func TestCreateSchedule_ConcurrentCreateIsNotAConflict(t *testing.T) {
	c, mockClient := newTestClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	request := testScheduleOptions(t, c, ScheduleOptions{TaskQueue: "orders"}, orderInput{})

	// Another replica creates the schedule between this one's Describe and Create.
	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "process-order").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(nil, serviceerror.NewNotFound("schedule not found")).Once()
	mockScheduleClient.On("Create", mock.Anything, request).Return(nil, temporal.ErrScheduleAlreadyRunning)
	mockHandle.On("Describe", mock.Anything).Return(describedAs(t, request), nil).Once()

	assert.NoError(t, c.createSchedule(t.Context(), request))
	mockHandle.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}

func TestCreateSchedule_DescribeFailsWithUnexpectedError(t *testing.T) {
	c, mockClient := newTestClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	request := testScheduleOptions(t, c, ScheduleOptions{TaskQueue: "orders"}, orderInput{})

	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "process-order").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(nil, serviceerror.NewInternal("something broke"))

	err := c.createSchedule(t.Context(), request)

	assert.ErrorContains(t, err, "something broke")
	mockScheduleClient.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestScheduleOptions(t *testing.T) {
	c, _ := newTestClient(t)

	t.Run("the schedule is named after the workflow unless given an ID", func(t *testing.T) {
		assert.Equal(t, "process-order", testScheduleOptions(t, c, ScheduleOptions{}, orderInput{}).ID)
		assert.Equal(t, "nightly", testScheduleOptions(t, c, ScheduleOptions{ID: "nightly"}, orderInput{}).ID)
	})

	t.Run("the fingerprint changes with every part of the request", func(t *testing.T) {
		fingerprint := func(opts ScheduleOptions, input orderInput) any {
			action := testScheduleOptions(t, c, opts, input).Action.(*client.ScheduleWorkflowAction)
			return action.Memo[scheduleFingerprintMemo]
		}

		base := ScheduleOptions{TaskQueue: "orders", CronExpressions: []string{"0 1 * * *"}}
		baseline := fingerprint(base, orderInput{})
		assert.Equal(t, baseline, fingerprint(base, orderInput{}), "the same request has the same fingerprint")
		assert.NotEqual(t, baseline, fingerprint(base, orderInput{OrderID: "o1"}))
		assert.NotEqual(t, baseline, fingerprint(ScheduleOptions{TaskQueue: "other", CronExpressions: base.CronExpressions}, orderInput{}))
		assert.NotEqual(t, baseline, fingerprint(ScheduleOptions{TaskQueue: "orders"}, orderInput{}))
		assert.NotEqual(t, baseline, fingerprint(ScheduleOptions{
			TaskQueue:       "orders",
			CronExpressions: base.CronExpressions,
			OverlapPolicy:   enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL,
		}, orderInput{}))

		// Unspecified is the server's skip: the two are the same request.
		assert.Equal(t, baseline, fingerprint(ScheduleOptions{
			TaskQueue:       "orders",
			CronExpressions: base.CronExpressions,
			OverlapPolicy:   enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		}, orderInput{}))
	})

	t.Run("an input that cannot be encoded is a validation error", func(t *testing.T) {
		_, err := c.scheduleOptions("process-order", ScheduleOptions{}, make(chan int))
		assert.ErrorContains(t, err, "Must be serializable as JSON")
	})

	// Every schedule carries the fingerprint the release that created it recorded,
	// so a change to how it is computed updates every schedule once, undoing what
	// was changed in the Temporal UI. The input holds what json/v2 would encode
	// differently or not at all: a duration, a map, characters v1 escapes for
	// HTML, and a nil slice.
	t.Run("the fingerprint is the one earlier releases recorded", func(t *testing.T) {
		got, err := scheduleFingerprint("process-order", "orders", []string{"0 1 * * *"}, enumspb.SCHEDULE_OVERLAP_POLICY_SKIP, map[string]any{
			"order_id": "o1",
			"timeout":  30 * time.Second,
			"note":     "<b>&</b>",
			"tags":     []string(nil),
		})
		require.NoError(t, err)
		assert.Equal(t, "16ff198ebe2ea09da92eb5c739aca4858a4a136c05f74218f08ae003c8104213", got)
	})
}

func TestSchedulesMatch(t *testing.T) {
	action := func(workflow, taskQueue string) *client.ScheduleWorkflowAction {
		return &client.ScheduleWorkflowAction{Workflow: workflow, TaskQueue: taskQueue}
	}

	testcases := []struct {
		name     string
		existing client.Schedule
		opts     client.ScheduleOptions
		want     bool
	}{
		{
			name: "identical schedules",
			existing: client.Schedule{
				Action: action("my-workflow", "my-queue"),
				Policy: &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP},
			},
			opts: client.ScheduleOptions{Action: action("my-workflow", "my-queue"), Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP},
			want: true,
		},
		{
			name: "overlap unspecified matches skip",
			existing: client.Schedule{
				Action: action("my-workflow", "my-queue"),
				Policy: &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP},
			},
			opts: client.ScheduleOptions{Action: action("my-workflow", "my-queue")},
			want: true,
		},
		{
			name:     "nil policy matches unspecified overlap",
			existing: client.Schedule{Action: action("my-workflow", "my-queue")},
			opts:     client.ScheduleOptions{Action: action("my-workflow", "my-queue")},
			want:     true,
		},
		{
			name: "different overlap policy",
			existing: client.Schedule{
				Action: action("my-workflow", "my-queue"),
				Policy: &client.SchedulePolicies{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP},
			},
			opts: client.ScheduleOptions{Action: action("my-workflow", "my-queue"), Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL},
			want: false,
		},
		{
			name:     "different workflow name",
			existing: client.Schedule{Action: action("workflow-a", "my-queue")},
			opts:     client.ScheduleOptions{Action: action("workflow-b", "my-queue")},
			want:     false,
		},
		{
			name:     "different task queue",
			existing: client.Schedule{Action: action("my-workflow", "queue-a")},
			opts:     client.ScheduleOptions{Action: action("my-workflow", "queue-b")},
			want:     false,
		},
		{
			name:     "existing action is not a workflow action",
			existing: client.Schedule{},
			opts:     client.ScheduleOptions{Action: action("my-workflow", "my-queue")},
			want:     false,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, schedulesMatch(&client.ScheduleDescription{Schedule: tc.existing}, tc.opts))
		})
	}
}

// describedWithStatus returns a description of the run "my-run" in status.
func describedWithStatus(status enumspb.WorkflowExecutionStatus) *client.WorkflowExecutionDescription {
	return &client.WorkflowExecutionDescription{
		WorkflowExecution: workflow.Execution{ID: "my-workflow", RunID: "my-run"},
		Status:            status,
	}
}

// noFollowing is the option every read of a closed run must carry.
var noFollowing = client.WorkflowRunGetOptions{DisableFollowingRuns: true}

func TestClosedWorkflowResult_OpenRunsAreNotRead(t *testing.T) {
	for _, status := range []enumspb.WorkflowExecutionStatus{
		enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,

		// Paused is not running, and has not closed either: reading it would block
		// until it is resumed and finishes.
		enumspb.WORKFLOW_EXECUTION_STATUS_PAUSED,
		enumspb.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED,
	} {
		t.Run(status.String(), func(t *testing.T) {
			c, mockClient := newTestClient(t)
			mockClient.On("DescribeWorkflow", mock.Anything, "my-workflow", "").Return(describedWithStatus(status), nil)

			var got string
			closed, err := c.closedWorkflowResult(t.Context(), "my-workflow", "", &got)

			assert.NoError(t, err)
			assert.False(t, closed)
			assert.Empty(t, got)
			mockClient.AssertNotCalled(t, "GetWorkflow", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestClosedWorkflowResult_Completed(t *testing.T) {
	c, mockClient := newTestClient(t)
	mockRun := mocks.NewWorkflowRun(t)

	// The run is read by the ID Describe returned, not the empty one it was asked
	// about: a new run may have started in between.
	mockClient.On("DescribeWorkflow", mock.Anything, "my-workflow", "").
		Return(describedWithStatus(enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED), nil)
	mockClient.On("GetWorkflow", mock.Anything, "my-workflow", "my-run").Return(mockRun)
	mockRun.On("GetWithOptions", mock.Anything, mock.Anything, noFollowing).Run(func(args mock.Arguments) {
		*args.Get(1).(*string) = "the-result"
	}).Return(nil)

	var got string
	closed, err := c.closedWorkflowResult(t.Context(), "my-workflow", "", &got)

	assert.NoError(t, err)
	assert.True(t, closed)
	assert.Equal(t, "the-result", got)
}

func TestClosedWorkflowResult_ContinuedAsNewIsNotFollowed(t *testing.T) {
	c, mockClient := newTestClient(t)
	mockRun := mocks.NewWorkflowRun(t)

	continued := &workflow.ContinueAsNewError{}

	mockClient.On("DescribeWorkflow", mock.Anything, "my-workflow", "my-run").
		Return(describedWithStatus(enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW), nil)
	mockClient.On("GetWorkflow", mock.Anything, "my-workflow", "my-run").Return(mockRun)
	mockRun.On("GetWithOptions", mock.Anything, mock.Anything, noFollowing).Return(continued)

	var got string
	closed, err := c.closedWorkflowResult(t.Context(), "my-workflow", "my-run", &got)

	assert.True(t, closed)

	var asNew *workflow.ContinueAsNewError
	assert.ErrorAs(t, err, &asNew)
	mockRun.AssertNotCalled(t, "Get", mock.Anything, mock.Anything)
}

func TestClosedWorkflowResult_NotFound(t *testing.T) {
	c, mockClient := newTestClient(t)

	mockClient.On("DescribeWorkflow", mock.Anything, "my-workflow", "my-run").
		Return(nil, serviceerror.NewNotFound("workflow not found"))

	var got string
	closed, err := c.closedWorkflowResult(t.Context(), "my-workflow", "my-run", &got)

	assert.NoError(t, err)
	assert.False(t, closed)
	mockClient.AssertNotCalled(t, "GetWorkflow", mock.Anything, mock.Anything, mock.Anything)
}

func TestClosedWorkflowResult_DescribeFails(t *testing.T) {
	c, mockClient := newTestClient(t)

	mockClient.On("DescribeWorkflow", mock.Anything, "my-workflow", "my-run").
		Return(nil, serviceerror.NewUnavailable("try later"))

	closed, err := c.closedWorkflowResult(t.Context(), "my-workflow", "my-run", new(string))

	assert.False(t, closed)
	assert.ErrorContains(t, err, "try later")
}

func TestExecuteWorkflow_ReturnsNoRunOnError(t *testing.T) {
	c, mockClient := newTestClient(t)

	failure := errors.New("namespace not found")
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, "process-order", orderInput{}).Return(nil, failure)

	run, err := c.ExecuteWorkflow(t.Context(), testWorkflow, client.StartWorkflowOptions{}, orderInput{})

	assert.ErrorIs(t, err, failure)
	assert.Nil(t, run, "a run handle over no run panics on ID, RunID and Metadata")
}

func TestExecuteWorkflow(t *testing.T) {
	c, mockClient := newTestClient(t)
	mockRun := mocks.NewWorkflowRun(t)

	opts := client.StartWorkflowOptions{ID: "order-1", TaskQueue: "orders"}
	mockClient.On("ExecuteWorkflow", mock.Anything, opts, "process-order", orderInput{OrderID: "o1"}).Return(mockRun, nil)
	mockRun.On("GetID").Return("order-1")

	run, err := c.ExecuteWorkflow(t.Context(), testWorkflow, opts, orderInput{OrderID: "o1"})

	require.NoError(t, err)
	assert.Equal(t, "order-1", run.ID())
}

func TestClient_WorkflowOperations(t *testing.T) {
	t.Run("WorkflowRun", func(t *testing.T) {
		c, mockClient := newTestClient(t)
		mockRun := mocks.NewWorkflowRun(t)

		mockClient.On("GetWorkflow", mock.Anything, "order-1", "").Return(mockRun)
		mockRun.On("Get", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			args.Get(1).(*orderResult).Charged = true
		}).Return(nil)

		result, err := c.WorkflowRun(t.Context(), testWorkflow, "order-1", "").Await(t.Context())

		require.NoError(t, err)
		assert.True(t, result.Charged)
	})

	t.Run("SignalWithStartWorkflow", func(t *testing.T) {
		c, mockClient := newTestClient(t)
		mockRun := mocks.NewWorkflowRun(t)

		opts := client.StartWorkflowOptions{ID: "order-1", TaskQueue: "orders"}
		mockClient.On("SignalWithStartWorkflow", mock.Anything, "order-1", "cancel-order", cancelPayload{Reason: "r"},
			opts, "process-order", orderInput{OrderID: "o1"}).Return(mockRun, nil)

		run, err := c.SignalWithStartWorkflow(t.Context(), testWorkflow, testSignal, opts, orderInput{OrderID: "o1"}, cancelPayload{Reason: "r"})

		require.NoError(t, err)
		assert.NotNil(t, run)
	})

	t.Run("SignalWithStartWorkflow reports no run on error", func(t *testing.T) {
		c, mockClient := newTestClient(t)

		mockClient.On("SignalWithStartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything,
			mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("boom"))

		run, err := c.SignalWithStartWorkflow(t.Context(), testWorkflow, testSignal, client.StartWorkflowOptions{}, orderInput{}, cancelPayload{})

		assert.Error(t, err)
		assert.Nil(t, run)
	})

	t.Run("QueryWorkflow", func(t *testing.T) {
		c, mockClient := newTestClient(t)

		payloads, err := converter.GetDefaultDataConverter().ToPayloads(orderResult{Charged: true})
		require.NoError(t, err)

		mockClient.On("QueryWorkflow", mock.Anything, "order-1", "", "order-status", NoInput{}).
			Return(client.NewValue(payloads), nil)

		result, err := c.QueryWorkflow(t.Context(), testQuery, "order-1", "", NoInput{})

		require.NoError(t, err)
		assert.True(t, result.Charged)
	})

	t.Run("UpdateWorkflow waits for the update to complete", func(t *testing.T) {
		c, mockClient := newTestClient(t)
		mockHandle := mocks.NewWorkflowUpdateHandle(t)

		mockClient.On("UpdateWorkflow", mock.Anything, client.UpdateWorkflowOptions{
			WorkflowID:   "order-1",
			UpdateName:   "amend-order",
			Args:         []any{orderInput{OrderID: "o1"}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		}).Return(mockHandle, nil)
		mockHandle.On("Get", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			args.Get(1).(*orderResult).Charged = true
		}).Return(nil)

		result, err := c.UpdateWorkflow(t.Context(), testUpdate, "order-1", "", orderInput{OrderID: "o1"})

		require.NoError(t, err)
		assert.True(t, result.Charged)
	})

	t.Run("CancelWorkflow", func(t *testing.T) {
		c, mockClient := newTestClient(t)
		mockClient.On("CancelWorkflow", mock.Anything, "order-1", "run-1").Return(nil)

		assert.NoError(t, c.CancelWorkflow(t.Context(), "order-1", "run-1"))
	})

	t.Run("TerminateWorkflow", func(t *testing.T) {
		c, mockClient := newTestClient(t)
		mockClient.On("TerminateWorkflow", mock.Anything, "order-1", "", "operator request").Return(nil)

		assert.NoError(t, c.TerminateWorkflow(t.Context(), "order-1", "", "operator request"))
	})

	t.Run("DescribeWorkflow", func(t *testing.T) {
		c, mockClient := newTestClient(t)

		desc := &client.WorkflowExecutionDescription{}
		mockClient.On("DescribeWorkflow", mock.Anything, "order-1", "").Return(desc, nil)

		got, err := c.DescribeWorkflow(t.Context(), "order-1", "")

		require.NoError(t, err)
		assert.Same(t, desc, got)
	})
}
