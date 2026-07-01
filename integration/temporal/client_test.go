package temporal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflow "go.temporal.io/api/workflow/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/temporal"
)

func TestCreateSchedule_ScheduleDoesNotExist(t *testing.T) {
	mockClient := mocks.NewClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	opts := client.ScheduleOptions{
		ID: "my-schedule",
		Action: &client.ScheduleWorkflowAction{
			Workflow:  "my-workflow",
			TaskQueue: "my-queue",
		},
	}

	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "my-schedule").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(nil, serviceerror.NewNotFound("schedule not found"))
	mockScheduleClient.On("Create", mock.Anything, opts).Return(mockHandle, nil)

	c := &iclient{client: mockClient}
	err := c.createSchedule(t.Context(), opts)

	assert.NoError(t, err)
}

func TestCreateSchedule_ScheduleExistsWithSameProperties(t *testing.T) {
	mockClient := mocks.NewClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	opts := client.ScheduleOptions{
		ID: "my-schedule",
		Action: &client.ScheduleWorkflowAction{
			Workflow:  "my-workflow",
			TaskQueue: "my-queue",
		},
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_UNSPECIFIED,
	}

	desc := &client.ScheduleDescription{
		Schedule: client.Schedule{
			Action: &client.ScheduleWorkflowAction{
				Workflow:  "my-workflow",
				TaskQueue: "my-queue",
			},
			Policy: &client.SchedulePolicies{
				Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
			},
		},
	}

	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "my-schedule").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(desc, nil)

	c := &iclient{client: mockClient}
	err := c.createSchedule(t.Context(), opts)

	assert.NoError(t, err)
	mockScheduleClient.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestCreateSchedule_ScheduleExistsWithDifferentProperties(t *testing.T) {
	mockClient := mocks.NewClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	opts := client.ScheduleOptions{
		ID: "my-schedule",
		Action: &client.ScheduleWorkflowAction{
			Workflow:  "my-workflow",
			TaskQueue: "different-queue",
		},
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	}

	desc := &client.ScheduleDescription{
		Schedule: client.Schedule{
			Action: &client.ScheduleWorkflowAction{
				Workflow:  "my-workflow",
				TaskQueue: "original-queue",
			},
			Policy: &client.SchedulePolicies{
				Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
			},
		},
	}

	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "my-schedule").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(desc, nil)

	c := &iclient{client: mockClient}
	err := c.createSchedule(t.Context(), opts)

	assert.ErrorIs(t, err, temporal.ErrScheduleAlreadyRunning)
	mockScheduleClient.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestCreateSchedule_DescribeFailsWithUnexpectedError(t *testing.T) {
	mockClient := mocks.NewClient(t)
	mockScheduleClient := mocks.NewScheduleClient(t)
	mockHandle := mocks.NewScheduleHandle(t)

	opts := client.ScheduleOptions{
		ID: "my-schedule",
		Action: &client.ScheduleWorkflowAction{
			Workflow:  "my-workflow",
			TaskQueue: "my-queue",
		},
	}

	mockClient.On("ScheduleClient").Return(mockScheduleClient)
	mockScheduleClient.On("GetHandle", mock.Anything, "my-schedule").Return(mockHandle)
	mockHandle.On("Describe", mock.Anything).Return(nil, serviceerror.NewInternal("something broke"))

	c := &iclient{client: mockClient}
	err := c.createSchedule(t.Context(), opts)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "something broke")
	mockScheduleClient.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestSchedulesMatch(t *testing.T) {
	t.Run("identical schedules", func(t *testing.T) {
		desc := &client.ScheduleDescription{
			Schedule: client.Schedule{
				Action: &client.ScheduleWorkflowAction{
					Workflow:  "my-workflow",
					TaskQueue: "my-queue",
				},
				Policy: &client.SchedulePolicies{
					Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
				},
			},
		}

		opts := client.ScheduleOptions{
			Action: &client.ScheduleWorkflowAction{
				Workflow:  "my-workflow",
				TaskQueue: "my-queue",
			},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		}

		assert.True(t, schedulesMatch(desc, opts))
	})

	t.Run("overlap unspecified matches skip", func(t *testing.T) {
		desc := &client.ScheduleDescription{
			Schedule: client.Schedule{
				Action: &client.ScheduleWorkflowAction{
					Workflow:  "my-workflow",
					TaskQueue: "my-queue",
				},
				Policy: &client.SchedulePolicies{
					Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
				},
			},
		}

		opts := client.ScheduleOptions{
			Action: &client.ScheduleWorkflowAction{
				Workflow:  "my-workflow",
				TaskQueue: "my-queue",
			},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_UNSPECIFIED,
		}

		assert.True(t, schedulesMatch(desc, opts))
	})

	t.Run("overlap unspecified in desc matches skip in opts", func(t *testing.T) {
		desc := &client.ScheduleDescription{
			Schedule: client.Schedule{
				Action: &client.ScheduleWorkflowAction{
					Workflow:  "my-workflow",
					TaskQueue: "my-queue",
				},
				Policy: &client.SchedulePolicies{
					Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_UNSPECIFIED,
				},
			},
		}

		opts := client.ScheduleOptions{
			Action: &client.ScheduleWorkflowAction{
				Workflow:  "my-workflow",
				TaskQueue: "my-queue",
			},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		}

		assert.True(t, schedulesMatch(desc, opts))
	})

	t.Run("nil policy matches unspecified overlap", func(t *testing.T) {
		desc := &client.ScheduleDescription{
			Schedule: client.Schedule{
				Action: &client.ScheduleWorkflowAction{
					Workflow:  "my-workflow",
					TaskQueue: "my-queue",
				},
			},
		}

		opts := client.ScheduleOptions{
			Action: &client.ScheduleWorkflowAction{
				Workflow:  "my-workflow",
				TaskQueue: "my-queue",
			},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_UNSPECIFIED,
		}

		assert.True(t, schedulesMatch(desc, opts))
	})

	t.Run("different overlap policy", func(t *testing.T) {
		desc := &client.ScheduleDescription{
			Schedule: client.Schedule{
				Action: &client.ScheduleWorkflowAction{
					Workflow:  "my-workflow",
					TaskQueue: "my-queue",
				},
				Policy: &client.SchedulePolicies{
					Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
				},
			},
		}

		opts := client.ScheduleOptions{
			Action: &client.ScheduleWorkflowAction{
				Workflow:  "my-workflow",
				TaskQueue: "my-queue",
			},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL,
		}

		assert.False(t, schedulesMatch(desc, opts))
	})

	t.Run("different workflow name", func(t *testing.T) {
		desc := &client.ScheduleDescription{
			Schedule: client.Schedule{
				Action: &client.ScheduleWorkflowAction{
					Workflow:  "workflow-a",
					TaskQueue: "my-queue",
				},
				Policy: &client.SchedulePolicies{
					Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
				},
			},
		}

		opts := client.ScheduleOptions{
			Action: &client.ScheduleWorkflowAction{
				Workflow:  "workflow-b",
				TaskQueue: "my-queue",
			},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		}

		assert.False(t, schedulesMatch(desc, opts))
	})

	t.Run("different task queue", func(t *testing.T) {
		desc := &client.ScheduleDescription{
			Schedule: client.Schedule{
				Action: &client.ScheduleWorkflowAction{
					Workflow:  "my-workflow",
					TaskQueue: "queue-a",
				},
				Policy: &client.SchedulePolicies{
					Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
				},
			},
		}

		opts := client.ScheduleOptions{
			Action: &client.ScheduleWorkflowAction{
				Workflow:  "my-workflow",
				TaskQueue: "queue-b",
			},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		}

		assert.False(t, schedulesMatch(desc, opts))
	})

	t.Run("desc action is not a workflow action", func(t *testing.T) {
		desc := &client.ScheduleDescription{
			Schedule: client.Schedule{
				Action: nil,
				Policy: &client.SchedulePolicies{
					Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
				},
			},
		}

		opts := client.ScheduleOptions{
			Action: &client.ScheduleWorkflowAction{
				Workflow:  "my-workflow",
				TaskQueue: "my-queue",
			},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		}

		assert.False(t, schedulesMatch(desc, opts))
	})
}

func TestClosedWorkflowResult_Running(t *testing.T) {
	mockClient := mocks.NewClient(t)

	desc := &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflow.WorkflowExecutionInfo{
			Status: enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,
		},
	}

	mockClient.On("DescribeWorkflowExecution", mock.Anything, "my-workflow", "my-run").Return(desc, nil)

	c := &iclient{client: mockClient}

	var got string
	closed, err := c.closedWorkflowResult(t.Context(), "my-workflow", "my-run", &got)

	assert.NoError(t, err)
	assert.False(t, closed)
	assert.Empty(t, got)
	mockClient.AssertNotCalled(t, "GetWorkflow", mock.Anything, mock.Anything, mock.Anything)
}

func TestClosedWorkflowResult_Completed(t *testing.T) {
	mockClient := mocks.NewClient(t)
	mockRun := mocks.NewWorkflowRun(t)

	desc := &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflow.WorkflowExecutionInfo{
			Status: enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED,
		},
	}

	mockClient.On("DescribeWorkflowExecution", mock.Anything, "my-workflow", "my-run").Return(desc, nil)
	mockClient.On("GetWorkflow", mock.Anything, "my-workflow", "my-run").Return(mockRun)
	mockRun.On("Get", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		valuePtr := args.Get(1).(*string)
		*valuePtr = "the-result"
	}).Return(nil)

	c := &iclient{client: mockClient}

	var got string
	closed, err := c.closedWorkflowResult(t.Context(), "my-workflow", "my-run", &got)

	assert.NoError(t, err)
	assert.True(t, closed)
	assert.Equal(t, "the-result", got)
}

func TestClosedWorkflowResult_NotFound(t *testing.T) {
	mockClient := mocks.NewClient(t)

	mockClient.On("DescribeWorkflowExecution", mock.Anything, "my-workflow", "my-run").
		Return(nil, serviceerror.NewNotFound("workflow not found"))

	c := &iclient{client: mockClient}

	var got string
	closed, err := c.closedWorkflowResult(t.Context(), "my-workflow", "my-run", &got)

	assert.NoError(t, err)
	assert.False(t, closed)
	assert.Empty(t, got)
	mockClient.AssertNotCalled(t, "GetWorkflow", mock.Anything, mock.Anything, mock.Anything)
}
