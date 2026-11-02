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
// Receive on it to receive from inside a workflow, or Channel to wait on it
// alongside other channels with a workflow.Selector.
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
// observability; the workflow side should prefer Receive or Channel.
func (s Signal[Payload]) Name() string {
	return s.name
}

// Receive blocks until the workflow receives this signal, and returns its payload.
// The bool is false only once the channel is closed, which a signal channel never
// is in practice.
//
// It does not return when ctx is cancelled: a workflow that must also react to
// cancellation, a timer or another signal waits on Channel with a
// workflow.Selector instead.
func (s Signal[Payload]) Receive(ctx workflow.Context) (Payload, bool) {
	var payload Payload
	more := workflow.GetSignalChannel(ctx, s.name).Receive(ctx, &payload)

	return payload, more
}

// Channel returns the workflow-side receive channel for this signal, scoped to the
// signal's name. Use it inside a workflow with a workflow.Selector, and decode what
// it delivers into a [Payload]; Receive does both for the common case of waiting on
// this signal alone.
//
// Channel takes no Client: inside a workflow the dispatcher travels on the
// workflow.Context.
func (s Signal[Payload]) Channel(ctx workflow.Context) workflow.ReceiveChannel {
	return workflow.GetSignalChannel(ctx, s.name)
}
