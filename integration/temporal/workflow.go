package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/integration/temporal/temporalrest"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// NoInput is a placeholder struct used to denote a workflow that takes no specific
// input.
type NoInput struct{}

// NoResult is a placeholder struct used to denote a workflow that produces no
// specific result.
type NoResult struct{}

// Workflow is the type-safe handle for a Temporal workflow definition. Generics
// [Input, Result] enforce correct type usage across registration, execution, and
// result retrieval.
//
// A Workflow carries no connection state, so handles are meant to be declared as
// package-level variables and shared between the client and worker sides of a
// service:
//
//	var ProcessOrder = temporal.NewWorkflow[OrderInput, OrderResult]("process-order")
//
// Pass the handle to Worker.RegisterWorkflow to bind an implementation, and to
// Client.ExecuteWorkflow to start a run. Both infer [Input] and [Result] from it.
type Workflow[Input, Result any] struct {
	name string
}

// NewWorkflow is the factory function for creating a new type-safe Workflow handle.
func NewWorkflow[Input, Result any](name string) Workflow[Input, Result] {
	return Workflow[Input, Result]{
		name: name,
	}
}

// Name returns the workflow's name as registered with Temporal. Use it when
// correlating runs or for observability.
func (wf Workflow[Input, Result]) Name() string {
	return wf.name
}

// WorkflowRun wraps the Temporal SDK's client.WorkflowRun, adding type safety for
// result retrieval via the generic [Result] type.
type WorkflowRun[Result any] struct {
	run client.WorkflowRun
}

// ID returns the immutable workflow Id.
func (wr *WorkflowRun[Result]) ID() string {
	return wr.run.GetID()
}

// RunID returns the unique run Id for this specific execution.
func (wr *WorkflowRun[Result]) RunID() string {
	return wr.run.GetRunID()
}

// Await blocks until the workflow run completes and returns its result, deserialized
// into the generic [Result] type. It is the blocking counterpart to
// Client.WorkflowResultIfClosed.
func (wr *WorkflowRun[Result]) Await(ctx context.Context) (Result, error) {
	var output Result
	err := wr.run.Get(ctx, &output)

	return output, err
}

// Metadata constructs a Temporal metadata struct suitable for external REST
// responses. This is used to expose workflow and run identifiers without exposing
// the entire Temporal client object.
func (wr *WorkflowRun[Result]) Metadata() *temporalrest.Metadata {
	if wr == nil {
		return nil
	}

	return &temporalrest.Metadata{
		Workflow: &temporalrest.MetadataWorkflow{
			ID: wr.run.GetID(),
			Run: &temporalrest.MetadataRun{
				ID: wr.run.GetRunID(),
			},
		},
	}
}

// ScheduleOptions holds the options to pass for creating a new schedule of a
// workflow.
type ScheduleOptions struct {
	TaskQueue       string
	CronExpressions []string

	// OverlapPolicy controls what happens when an Action would be started by a
	// Schedule at the same time that an older Action is still running.
	//
	// Optional: defaults to SCHEDULE_OVERLAP_POLICY_SKIP.
	OverlapPolicy enumspb.ScheduleOverlapPolicy
}
