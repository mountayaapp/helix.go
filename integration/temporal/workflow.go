package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/integration/temporal/temporalrest"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

/*
NoInput is a placeholder struct used to denote a workflow that takes no specific
input.
*/
type NoInput struct{}

/*
NoResult is a placeholder struct used to denote a workflow that produces no
specific result.
*/
type NoResult struct{}

/*
Workflow defines the type-safe contract for a Temporal workflow definition.
Generics [Input, Result] enforce correct type usage across registration, execution,
and result retrieval.
*/
type Workflow[Input, Result any] interface {

	// Register links the workflow implementation to its name on a worker. The
	// function signature of impl is strictly checked against [Input] and [Result].
	Register(worker Worker, impl func(ctx workflow.Context, input Input) (Result, error))

	// Execute starts the workflow via the Temporal Client, enforcing the [Input]
	// type.
	Execute(ctx context.Context, c Client, options client.StartWorkflowOptions, input Input) (WorkflowRun[Result], error)

	// GetResult retrieves the final result of a completed workflow run, enforcing
	// the [Result] type.
	GetResult(ctx context.Context, run WorkflowRun[Result]) (Result, error)

	// CreateSchedule creates a new Temporal Schedule for this workflow definition.
	// If a schedule with the same ID already exists and has identical properties
	// (workflow name, task queue, overlap policy), the call is a no-op: no error
	// is returned and no error is recorded in traces. If the existing schedule
	// differs, an error is returned.
	CreateSchedule(ctx context.Context, c Client, opts ScheduleOptions) error
}

/*
workflowDefinition is the concrete implementation of a type-safe Temporal workflow.
*/
type workflowDefinition[Input, Result any] struct {
	name string
}

/*
NewWorkflow is the factory function for creating a new type-safe Workflow handle.
*/
func NewWorkflow[Input, Result any](name string) Workflow[Input, Result] {
	return &workflowDefinition[Input, Result]{
		name: name,
	}
}

/*
Register registers the workflow implementation with the worker. The primary value
of this wrapper is ensuring the function signature matches the defined Workflow
contract ([Input], [Result]) at compile time.
*/
func (d *workflowDefinition[Input, Result]) Register(
	worker Worker,
	impl func(ctx workflow.Context, input Input) (Result, error),
) {
	worker.registerWorkflow(impl, workflow.RegisterOptions{
		Name: d.name,
	})
}

/*
Execute starts the workflow via the client. The [Input] type constraint on input
prevents runtime errors from passing incorrectly typed arguments to the workflow
via interface{}.
*/
func (d *workflowDefinition[Input, Result]) Execute(
	ctx context.Context,
	c Client,
	options client.StartWorkflowOptions,
	input Input,
) (WorkflowRun[Result], error) {
	run, err := c.executeWorkflow(ctx, options, d.name, input)
	result := workflowRun[Result]{
		run: run,
	}

	return &result, err
}

/*
GetResult retrieves the workflow result into the correct [Result] type. This
function performs the final type-safe deserialization of the workflow output.
*/
func (d *workflowDefinition[Input, Result]) GetResult(
	ctx context.Context,
	run WorkflowRun[Result],
) (Result, error) {
	var output Result
	err := run.GetResult(ctx, &output)

	return output, err
}

/*
WorkflowRun abstracts the Temporal SDK's client.WorkflowRun, adding type safety
for result retrieval via the generic [Result] type.
*/
type WorkflowRun[Result any] interface {

	// GetId returns the immutable Workflow Id.
	GetId() string

	// GetRunId returns the unique run Id for this specific execution.
	GetRunId() string

	// GetResult blocks and attempts to unmarshal the result into the provided
	// pointer. Enforcing [Result] prevents runtime errors when reading results.
	GetResult(ctx context.Context, valuePtr *Result) error

	// GetMetadata returns an external utility struct containing key run identifiers.
	GetMetadata() *temporalrest.Metadata
}

/*
workflowRun is the concrete implementation around the Temporal client.WorkflowRun.
*/
type workflowRun[Result any] struct {
	run client.WorkflowRun
}

/*
GetId returns the immutable Workflow Id.
*/
func (wr *workflowRun[Result]) GetId() string {
	return wr.run.GetID()
}

/*
GetRunId returns the unique run Id for this specific execution.
*/
func (wr *workflowRun[Result]) GetRunId() string {
	return wr.run.GetRunID()
}

/*
GetResult delegates to client.WorkflowRun.Get, ensuring the target pointer is of
the correct generic [Result] type.
*/
func (wr *workflowRun[Result]) GetResult(ctx context.Context, valuePtr *Result) error {
	return wr.run.Get(ctx, valuePtr)
}

/*
GetMetadata constructs a Temporal metadata struct suitable for external REST
responses. This is used to expose workflow and run identifiers without exposing
the entire Temporal client object.
*/
func (wr *workflowRun[Result]) GetMetadata() *temporalrest.Metadata {
	if wr == nil {
		return nil
	}

	return &temporalrest.Metadata{
		Workflow: &temporalrest.MetadataWorkflow{
			Id: wr.run.GetID(),
			Run: &temporalrest.MetadataRun{
				Id: wr.run.GetRunID(),
			},
		},
	}
}

/*
ScheduleOptions holds the options to pass for creating a new schedule of a
workflow.
*/
type ScheduleOptions struct {
	TaskQueue       string
	CronExpressions []string

	// OverlapPolicy controls what happens when an Action would be started by a
	// Schedule at the same time that an older Action is still running.
	//
	// Optional: defaults to SCHEDULE_OVERLAP_POLICY_SKIP.
	OverlapPolicy enumspb.ScheduleOverlapPolicy
}

/*
CreateSchedule wraps the Temporal Client's schedule creation, defining a Schedule
that executes this specific workflow. If a schedule with the same ID already exists
and has identical properties (workflow name, task queue, overlap policy), the call
is a no-op: no error is returned and no error is recorded in traces. If the
existing schedule differs, an error is returned.
*/
func (wr *workflowDefinition[Input, Result]) CreateSchedule(ctx context.Context, c Client, opts ScheduleOptions) error {
	cfg := client.ScheduleOptions{
		ID: wr.name,
		Spec: client.ScheduleSpec{
			CronExpressions: opts.CronExpressions,
		},
		Action: &client.ScheduleWorkflowAction{
			ID:        wr.name,
			Workflow:  wr.name,
			TaskQueue: opts.TaskQueue,
		},
		Overlap: opts.OverlapPolicy,
	}

	return c.createSchedule(ctx, cfg)
}
