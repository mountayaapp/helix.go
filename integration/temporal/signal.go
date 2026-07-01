package temporal

import (
	"context"

	"go.temporal.io/sdk/workflow"
)

/*
Signal defines the type-safe contract for a Temporal signal. Generics [Payload]
enforce a consistent payload type between the sender (client side) and the receiver
(workflow side), mirroring the Workflow and Activity handles.
*/
type Signal[Payload any] interface {

	// Name returns the signal's name. Use it when correlating signals or for
	// observability; the workflow side should prefer Channel.
	Name() string

	// Channel returns the workflow-side receive channel for this signal. Use it
	// inside a workflow (e.g. with a workflow.Selector) to receive [Payload] values
	// via Receive.
	Channel(ctx workflow.Context) workflow.ReceiveChannel

	// Signal sends a [Payload] to a running workflow execution from the client
	// side. A run Id is optional; pass an empty string to target the latest run.
	Signal(ctx context.Context, c Client, workflowID, runID string, payload Payload) error
}

/*
signalDefinition is the concrete implementation of a type-safe Temporal signal.
*/
type signalDefinition[Payload any] struct {
	name string
}

/*
NewSignal is the factory function for creating a new type-safe Signal handle.
*/
func NewSignal[Payload any](name string) Signal[Payload] {
	return &signalDefinition[Payload]{
		name: name,
	}
}

/*
Name returns the signal's name.
*/
func (d *signalDefinition[Payload]) Name() string {
	return d.name
}

/*
Channel returns the workflow-side receive channel for this signal, scoped to the
signal's name.
*/
func (d *signalDefinition[Payload]) Channel(ctx workflow.Context) workflow.ReceiveChannel {
	return workflow.GetSignalChannel(ctx, d.name)
}

/*
Signal sends a payload to a running workflow execution. The [Payload] type
constraint prevents runtime errors from passing incorrectly typed arguments to the
signal via interface{}.
*/
func (d *signalDefinition[Payload]) Signal(
	ctx context.Context,
	c Client,
	workflowID, runID string,
	payload Payload,
) error {
	return c.signalWorkflow(ctx, workflowID, runID, d.name, payload)
}
