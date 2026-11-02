package temporal

import (
	"go.temporal.io/sdk/workflow"
)

// Query is the type-safe handle for a Temporal query: a read-only request a
// client sends to a workflow, answered from the workflow's current state without
// changing it or its history. Generics [Input, Result] enforce one data contract
// between the client asking and the workflow answering.
//
// A Query carries no connection state, so handles are meant to be declared as
// package-level variables:
//
//	var OrderStatus = temporal.NewQuery[temporal.NoInput, StatusResult]("order-status")
//
// Call SetHandler on it inside the workflow to answer it, and pass it to
// Client.QueryWorkflow to ask.
type Query[Input, Result any] struct {
	name string
}

// NewQuery is the factory function for creating a new type-safe Query handle.
func NewQuery[Input, Result any](name string) Query[Input, Result] {
	return Query[Input, Result]{
		name: name,
	}
}

// Name returns the query's type as registered on the workflow.
func (q Query[Input, Result]) Name() string {
	return q.name
}

// SetHandler registers handler as the workflow's answer to this query. Call it
// from the workflow, before the workflow first blocks, so that a query arriving
// early finds it.
//
// handler must not change the workflow's state or block: a query is answered
// from state as it is, without a workflow task.
func (q Query[Input, Result]) SetHandler(ctx workflow.Context, handler func(input Input) (Result, error)) error {
	return workflow.SetQueryHandler(ctx, q.name, handler)
}
