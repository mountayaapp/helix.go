package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/event"
	"github.com/mountayaapp/helix.go/telemetry/log"

	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

/*
eventKeyIdentifier is the unique internal type for storing an Event in Temporal
workflow and activity contexts.
*/
type eventKeyIdentifier struct{}

/*
eventCtxKey is the context key used to store the current Event.
*/
var eventCtxKey eventKeyIdentifier

/*
defaultConverter is cached to avoid repeated function calls on every Inject/Extract
operation.
*/
var defaultConverter = converter.GetDefaultDataConverter()

/*
noopSpan is a pre-allocated noop span reused when no span is present in the
context, avoiding allocations of TracerProvider/Tracer/Span on every Extract.
*/
var noopSpan oteltrace.Span

func init() {
	_, noopSpan = noop.NewTracerProvider().Tracer("noop").Start(context.Background(), "")
}

/*
custompropagator implements the workflow.ContextPropagator interface, allowing
to set custom context propagation logic across Temporal workflows and activities.
*/
type custompropagator struct {
	cachedCtx context.Context
}

/*
Inject injects information from a Go context into headers.
*/
func (p *custompropagator) Inject(ctx context.Context, hw workflow.HeaderWriter) error {
	e, ok := event.EventFromContext(ctx)
	if !ok {
		return nil
	}

	// Retrieve the current span, and set Event's attributes.
	if span, ok := ctx.Value(spanCtxKey).(oteltrace.Span); ok && span != nil {
		setEventSpanAttributes(span, event.ToFlatMap(e))
	}

	// Transform the Event found to a Temporal payload so we can set it in header
	// right after.
	payload, err := defaultConverter.ToPayload(e)
	if err != nil {
		return err
	}

	hw.Set(event.Key, payload)
	return nil
}

/*
InjectFromWorkflow injects information from a workflow's context into headers.
*/
func (p *custompropagator) InjectFromWorkflow(ctx workflow.Context, hw workflow.HeaderWriter) error {
	e, ok := ctx.Value(eventCtxKey).(event.Event)
	if !ok {
		return nil
	}

	// Retrieve the current span, and set Event's attributes.
	// Also set the workflow's attributes from its info.
	if span, ok := ctx.Value(spanCtxKey).(oteltrace.Span); ok && span != nil {
		setEventSpanAttributes(span, event.ToFlatMap(e))
		setWorkflowAttributes(span, workflow.GetInfo(ctx))
	}

	// Transform the Event found to a Temporal payload so we can set it in header
	// right after.
	payload, err := defaultConverter.ToPayload(e)
	if err != nil {
		return err
	}

	hw.Set(event.Key, payload)
	return nil
}

/*
Extract extracts context information from headers and returns a context object.
*/
func (p *custompropagator) Extract(ctx context.Context, hr workflow.HeaderReader) (context.Context, error) {
	var e event.Event
	if value, ok := hr.Get(event.Key); ok {
		if err := defaultConverter.FromPayload(value, &e); err != nil {
			log.Warn(p.cachedCtx, "Failed to deserialize event from Temporal header",
				log.String("integration", identifier),
				log.String("error", err.Error()),
			)

			return ctx, nil
		}
	}

	// Retrieve the current span, and set Event's attributes. Make sure a span is
	// set.
	span, ok := ctx.Value(spanCtxKey).(oteltrace.Span)
	if span == nil || !ok {
		span = noopSpan
		ctx = context.WithValue(ctx, spanCtxKey, span)
	}

	setEventSpanAttributes(span, event.ToFlatMap(e))

	// Also set the activity's attributes from its info.
	setActivityAttributes(span, activity.GetInfo(ctx))

	// Add the Event to the context returned.
	ctx = event.ContextWithEvent(ctx, e)
	ctx = context.WithValue(ctx, eventCtxKey, e)

	return ctx, nil
}

/*
ExtractToWorkflow extracts context information from headers and returns a workflow
context.
*/
func (p *custompropagator) ExtractToWorkflow(ctx workflow.Context, hr workflow.HeaderReader) (workflow.Context, error) {
	var e event.Event
	if value, ok := hr.Get(event.Key); ok {
		if err := defaultConverter.FromPayload(value, &e); err != nil {
			log.Warn(p.cachedCtx, "Failed to deserialize event from Temporal header",
				log.String("integration", identifier),
				log.String("error", err.Error()),
			)

			return ctx, nil
		}
	}

	// Retrieve the current span, and set Event's attributes. Make sure a span is
	// set.
	span, ok := ctx.Value(spanCtxKey).(oteltrace.Span)
	if span == nil || !ok {
		span = noopSpan
		ctx = workflow.WithValue(ctx, spanCtxKey, span)
	}

	setEventSpanAttributes(span, event.ToFlatMap(e))

	// Also set the workflow's attributes from its info.
	setWorkflowAttributes(span, workflow.GetInfo(ctx))

	// Add the Event to the context returned.
	ctx = workflow.WithValue(ctx, eventCtxKey, e)

	return ctx, nil
}
