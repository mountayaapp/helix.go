package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration/temporal/temporalrest"

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

// ID returns the immutable workflow ID.
func (wr *WorkflowRun[Result]) ID() string {
	return wr.run.GetID()
}

// RunID returns the unique run ID for this specific execution.
func (wr *WorkflowRun[Result]) RunID() string {
	return wr.run.GetRunID()
}

// Await blocks until the workflow run completes and returns its result, deserialized
// into the generic [Result] type. It is the blocking counterpart to
// Client.WorkflowResultIfClosed.
//
// A run that continues as new, is retried or runs on a cron is followed to the run
// that ends the chain, whose result Await returns.
func (wr *WorkflowRun[Result]) Await(ctx context.Context) (Result, error) {
	var output Result
	err := wr.run.Get(ctx, &output)

	return output, err
}

// AwaitInto blocks until the workflow run completes and deserializes its result
// into dst, which the caller supplies already populated when the run's result
// should be merged into an accumulating value rather than replace it. It returns
// a validation error when dst is nil, rather than discarding the result.
//
// The merge is a property of the DataConverter, not of AwaitInto. With the
// default JSON encoding, a field the workflow left at its zero value under
// omitempty is absent from the wire and leaves whatever dst already held, while a
// field without omitempty transmits its zero value and overwrites. A protobuf
// result is decoded by protojson or proto, which both reset dst before decoding
// into it: nothing is merged. Callers that want a clean result want Await instead.
func (wr *WorkflowRun[Result]) AwaitInto(ctx context.Context, dst *Result) error {
	if dst == nil {
		return nilDestination()
	}

	return wr.run.Get(ctx, dst)
}

// Metadata constructs a Temporal metadata struct suitable for external REST
// responses. This is used to expose workflow and run identifiers without exposing
// the entire Temporal client object.
//
// The workflow ID reaches the REST client as it is: an ID built from personal data
// or a secret discloses it.
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

// nilDestination returns the error AwaitInto reports when it is given no value to
// deserialize into. It is built on every call because an *errorstack.Error can be
// appended to, and one shared across calls would collect what each caller added.
func nilDestination() error {
	return errorstack.NewValidation(errorstack.Entry{
		Message: "Must be set",
		Path:    []any{"dst"},
	})
}
