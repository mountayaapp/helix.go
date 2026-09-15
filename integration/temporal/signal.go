package temporal

import (
	"go.temporal.io/sdk/workflow"
)

// Signal is the type-safe handle for a Temporal signal. Generics [Payload] enforce a
// consistent payload type between the sender (client side) and the receiver
// (workflow side), mirroring the Workflow and Activity handles.
//
// A Signal carries no connection state, so handles are meant to be declared as
// package-level variables:
//
//	var CancelOrder = temporal.NewSignal[CancelPayload]("cancel-order")
//
// Pass the handle to Client.SignalWorkflow to send from the client side; call
// Channel on it to receive from inside a workflow.
type Signal[Payload any] struct {
	name string
}

// NewSignal is the factory function for creating a new type-safe Signal handle.
func NewSignal[Payload any](name string) Signal[Payload] {
	return Signal[Payload]{
		name: name,
	}
}

// Name returns the signal's name. Use it when correlating signals or for
// observability; the workflow side should prefer Channel.
func (s Signal[Payload]) Name() string {
	return s.name
}

// Channel returns the workflow-side receive channel for this signal, scoped to the
// signal's name. Use it inside a workflow (e.g. with a workflow.Selector) to receive
// [Payload] values via Receive.
//
// Channel takes no Client: inside a workflow the dispatcher travels on the
// workflow.Context.
func (s Signal[Payload]) Channel(ctx workflow.Context) workflow.ReceiveChannel {
	return workflow.GetSignalChannel(ctx, s.name)
}
