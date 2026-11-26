package trace

import (
	"context"

	"github.com/mountaya/helix.go/event"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// ScopeName is the OpenTelemetry instrumentation scope everything helix emits
// carries: spans, log records and metrics alike. There is one for the whole
// library rather than one per integration or per signal: the vocabulary is the
// same everywhere, so a backend filtering on the scope gets all of helix's
// telemetry and none of anybody else's.
const ScopeName = "github.com/mountaya/helix.go"

// Tracer wraps an OpenTelemetry TracerProvider. Created by service.New, stored in
// context, and used by the public telemetry/trace package-level functions.
type Tracer struct {
	provider oteltrace.TracerProvider
	tracer   oteltrace.Tracer
}

// Start creates an OpenTelemetry Span and a context containing it.
//
// If the context contains a Span then the newly-created Span will be a child of
// that Span, otherwise it will be a root Span.
//
// Any Span that is created must also be ended. This is the responsibility of the
// caller.
func (t *Tracer) Start(ctx context.Context, kind oteltrace.SpanKind, name string) (context.Context, oteltrace.Span) {
	return t.tracer.Start(ctx, name, oteltrace.WithSpanKind(kind))
}

// StartEntry creates a Span in the manner of Start and, when the context carries
// an Event, stamps the event's attributes onto it and merges the event's
// propagated identifiers into the context's baggage.
//
// This is the entry-point variant: it is called once per trace, by the server
// integration accepting the work (REST, GraphQL, Temporal), rather than on every
// span. Child spans inherit the trace and the baggage without paying to re-stamp
// the same attributes.
func (t *Tracer) StartEntry(ctx context.Context, kind oteltrace.SpanKind, name string) (context.Context, oteltrace.Span) {
	e, ok := event.FromContext(ctx)
	if ok {
		ctx = withPropagatedEvent(ctx, e)
	}

	ctx, span := t.tracer.Start(ctx, name, oteltrace.WithSpanKind(kind))
	if ok && span.IsRecording() {
		span.SetAttributes(e.AppendAttrs(nil)...)
	}

	return ctx, span
}

// withPropagatedEvent merges the event's allow-listed identifiers into whatever
// baggage the context already carries, so that members set by an upstream service
// survive. An event that cannot be propagated — because it does not fit the W3C
// budget, or carries a value baggage cannot encode — leaves the existing baggage
// untouched rather than replacing it with a partial set.
func withPropagatedEvent(ctx context.Context, e event.Event) context.Context {
	members, err := e.Propagated()
	if err != nil || len(members) == 0 {
		return ctx
	}

	b := baggage.FromContext(ctx)
	for _, m := range members {
		merged, err := b.SetMember(m)
		if err != nil {
			continue
		}

		b = merged
	}

	return baggage.ContextWithBaggage(ctx, b)
}

// AppendEventAttrs appends the attributes of the Event carried by ctx, if any, to
// dst. Integrations use it to enrich a span they did not start.
func AppendEventAttrs(ctx context.Context, dst []attribute.KeyValue) []attribute.KeyValue {
	e, ok := event.FromContext(ctx)
	if !ok {
		return dst
	}

	return e.AppendAttrs(dst)
}

// StampEvent stamps the Event carried by ctx onto the span already in ctx, and
// merges the event's propagated identifiers into the context's baggage.
//
// This is the entry-point hook for a server integration whose entry span was
// opened before the Event reached the context. The GraphQL and MCP servers open
// their span outermost, around the application's own middleware — which is the
// middleware that puts the Event there — so they need this innermost, once per
// request.
//
// A server whose span opens *after* the application's middleware does not need it
// at all: the Event is already in the context when the span is created, so
// StartEntry stamps it. The REST integration is that case, because its entry span
// is opened inside the router, where the matched route is known.
func StampEvent(ctx context.Context) context.Context {
	e, ok := event.FromContext(ctx)
	if !ok {
		return ctx
	}

	if span := oteltrace.SpanFromContext(ctx); span.IsRecording() {
		span.SetAttributes(e.AppendAttrs(nil)...)
	}

	return withPropagatedEvent(ctx, e)
}

// Provider returns the underlying OpenTelemetry TracerProvider. Integrations that
// need to wire OpenTelemetry-native interceptors (e.g., Temporal) use this.
//
// The return type is the API interface, not the SDK implementation, so that the
// public surface does not commit to an SDK type.
func (t *Tracer) Provider() oteltrace.TracerProvider {
	return t.provider
}

// NewTracerWithProvider creates a Tracer around a caller-supplied provider.
//
// The provider is an API type and the caller keeps ownership of its lifecycle.
// That is deliberate: it is what keeps the OpenTelemetry SDK — and with it every
// OTLP exporter — out of this module's build graph. The service package builds
// the SDK provider and is responsible for shutting it down.
func NewTracerWithProvider(provider oteltrace.TracerProvider) *Tracer {
	return &Tracer{
		provider: provider,
		tracer:   provider.Tracer(ScopeName),
	}
}

// NewNopTracer creates a Tracer that produces no-op spans and does not export. The
// provider is the OpenTelemetry no-op implementation rather than nil, so that
// Provider() never hands a caller a nil interface to dereference.
func NewNopTracer() *Tracer {
	return NewTracerWithProvider(noop.NewTracerProvider())
}
