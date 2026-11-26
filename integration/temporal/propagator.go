package temporal

import (
	"context"
	"reflect"

	"github.com/mountaya/helix.go/event"
	internallog "github.com/mountaya/helix.go/internal/telemetry/log"
	"github.com/mountaya/helix.go/telemetry/log"

	"go.opentelemetry.io/otel/baggage"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

// Ensure *customPropagator complies to the workflow.ContextPropagator type.
var _ workflow.ContextPropagator = (*customPropagator)(nil)

// eventKeyIdentifier is the unique internal type for storing an Event in Temporal
// workflow and activity contexts.
type eventKeyIdentifier struct{}

// eventCtxKey is the context key used to store the Event a workflow or an activity
// received in its header.
var eventCtxKey eventKeyIdentifier

// customPropagator implements workflow.ContextPropagator. It carries the Event
// across Temporal's own boundaries — from a client to the workflow it starts or
// signals, and from a workflow to its activities and child workflows — in a
// dedicated "event" header.
//
// What the header holds is the sending side's decision. By default it is the
// allow-listed identifiers event.Event.Propagated sends as baggage, and nothing
// else; ConfigClient.PropagateFullEvent opts in to the whole Event. Either way it
// is encoded with the client's DataConverter, so that a codec configured there
// applies to it as it does to arguments. That matters more here than for baggage:
// Temporal persists headers in workflow history for the namespace's retention
// period.
type customPropagator struct {
	converter converter.DataConverter
	full      bool

	// logger reports a header that cannot be decoded, for activities. A workflow
	// logs through workflow.GetLogger instead, which stays quiet during replay.
	logger *internallog.Logger
}

// newCustomPropagator creates the propagator for a client configured with cfg,
// reporting through logger.
func newCustomPropagator(cfg *ConfigClient, logger *internallog.Logger) *customPropagator {
	return &customPropagator{
		converter: cfg.dataConverter(),
		full:      cfg.PropagateFullEvent,
		logger:    logger,
	}
}

// Inject writes the Event set on ctx into the header of the workflow, signal or
// query a client sends. Only an Event set explicitly with event.NewContext is
// written: see explicitEvent.
func (p *customPropagator) Inject(ctx context.Context, hw workflow.HeaderWriter) error {
	e, ok := explicitEvent(ctx)
	if !ok {
		return nil
	}

	return p.write(e, hw)
}

// InjectFromWorkflow writes the Event a workflow received into the header of the
// activities, child workflows and signals it sends, so that it follows the work
// down the chain.
func (p *customPropagator) InjectFromWorkflow(ctx workflow.Context, hw workflow.HeaderWriter) error {
	e, ok := eventFromContext(ctx)
	if !ok {
		return nil
	}

	return p.write(e, hw)
}

// Extract reads the Event from the header of an activity, and returns a context
// carrying it both for EventFromActivity and for event.FromContext, so that the
// integrations the activity calls propagate it further.
//
// A header that is absent, empty or cannot be decoded leaves ctx as it is:
// EventFromActivity then reports that there is no Event.
func (p *customPropagator) Extract(ctx context.Context, hr workflow.HeaderReader) (context.Context, error) {
	e, ok, err := p.read(hr)
	if err != nil {
		p.logger.Warn(ctx, "Failed to decode the Event from the Temporal header",
			log.String("integration", identifier),
			log.Err(err),
		)
	}

	if !ok {
		return ctx, nil
	}

	ctx = event.NewContext(ctx, e)
	return context.WithValue(ctx, eventCtxKey, e), nil
}

// ExtractToWorkflow reads the Event from the header of a workflow, signal, query
// or update, and returns a workflow context carrying it for EventFromWorkflow and
// for InjectFromWorkflow.
//
// A header that is absent, empty or cannot be decoded leaves ctx as it is. The
// failure is logged through the workflow's logger, which says nothing while the
// workflow is replaying: a header that could not be decoded once cannot be decoded
// on any of the replays that follow either, and reporting it each time would bury
// the one line that matters.
func (p *customPropagator) ExtractToWorkflow(ctx workflow.Context, hr workflow.HeaderReader) (workflow.Context, error) {
	e, ok, err := p.read(hr)
	if err != nil {
		workflow.GetLogger(ctx).Warn("Failed to decode the Event from the Temporal header",
			"integration", identifier,
			"error", err,
		)
	}

	if !ok {
		return ctx, nil
	}

	return workflow.WithValue(ctx, eventCtxKey, e), nil
}

// write encodes the part of e this client propagates into hw. An Event with
// nothing to send writes no header at all, rather than an empty one on every
// activity and child workflow.
func (p *customPropagator) write(e event.Event, hw workflow.HeaderWriter) error {
	if !p.full {
		e = allowListed(e)
	}

	if isZeroEvent(e) {
		return nil
	}

	payload, err := p.converter.ToPayload(e)
	if err != nil {
		return err
	}

	hw.Set(event.Key, payload)
	return nil
}

// read decodes the Event in hr. ok reports whether there is one to use: false
// when the header is absent, cannot be decoded — err then says why — or decodes to
// an empty Event, which is what an earlier version of this integration sent when
// it had nothing to send.
func (p *customPropagator) read(hr workflow.HeaderReader) (event.Event, bool, error) {
	var e event.Event

	payload, found := hr.Get(event.Key)
	if !found {
		return e, false, nil
	}

	if err := p.converter.FromPayload(payload, &e); err != nil {
		return event.Event{}, false, err
	}

	return e, !isZeroEvent(e), nil
}

// explicitEvent returns the Event set on ctx with event.NewContext, and never one
// rebuilt from the baggage ctx carries.
//
// event.FromContext falls back to baggage, which is right for correlating
// telemetry and wrong here. What Inject writes is persisted in workflow history and
// handed to every worker down the chain as the Event of the run, and the baggage
// on a context serving a request is whatever its caller put in a header.
func explicitEvent(ctx context.Context) (event.Event, bool) {
	return event.FromContext(baggage.ContextWithoutBaggage(ctx))
}

// allowListed returns the part of e that crosses a process boundary: the fields
// event.Event.Propagated sends as baggage, and nothing else.
//
// The list is repeated here rather than derived, because Propagated returns
// baggage members rather than an Event. TestAllowListed_MatchesPropagated pins the
// two together, so that a field added to one cannot be missing from the other.
func allowListed(e event.Event) event.Event {
	return event.Event{
		ID:             e.ID,
		Name:           e.Name,
		OrganizationID: e.OrganizationID,
		TenantID:       e.TenantID,
		App: event.App{
			Name:    e.App.Name,
			Version: e.App.Version,
		},
	}
}

// isZeroEvent reports whether e carries nothing at all. Event holds a map and a
// slice, so it cannot be compared with ==.
func isZeroEvent(e event.Event) bool {
	return reflect.ValueOf(&e).Elem().IsZero()
}
