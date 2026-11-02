package temporal

import (
	"go.temporal.io/sdk/workflow"
)

// Update is the type-safe handle for a Temporal update: a request a client sends
// to a running workflow that may change its state, and that the client waits on
// for a result. Generics [Input, Result] enforce one data contract between the
// client sending and the workflow handling.
//
// An Update carries no connection state, so handles are meant to be declared as
// package-level variables:
//
//	var AddItem = temporal.NewUpdate[ItemInput, CartResult]("add-item")
//
// Call SetHandler on it inside the workflow to handle it, and pass it to
// Client.UpdateWorkflow to send.
type Update[Input, Result any] struct {
	name string
}

// NewUpdate is the factory function for creating a new type-safe Update handle.
func NewUpdate[Input, Result any](name string) Update[Input, Result] {
	return Update[Input, Result]{
		name: name,
	}
}

// Name returns the update's name as registered on the workflow.
func (u Update[Input, Result]) Name() string {
	return u.name
}

// SetHandler registers handler as the workflow's handler for this update. Call it
// from the workflow, before the workflow first blocks, so that an update arriving
// early finds it.
//
// validator is optional. When set, it runs before handler and must not change the
// workflow's state: an update it rejects is reported to the client as an error and
// leaves no trace in the workflow's history.
func (u Update[Input, Result]) SetHandler(
	ctx workflow.Context,
	handler func(ctx workflow.Context, input Input) (Result, error),
	validator func(ctx workflow.Context, input Input) error,
) error {
	var opts workflow.UpdateHandlerOptions

	// Only a non-nil validator is passed on: a nil func in an interface field is
	// not a nil interface, and the SDK would try to call it.
	if validator != nil {
		opts.Validator = validator
	}

	return workflow.SetUpdateHandlerWithOptions(ctx, u.name, handler, opts)
}
