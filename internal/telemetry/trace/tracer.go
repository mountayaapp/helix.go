package trace

import (
	"context"
	"strings"

	"github.com/mountayaapp/helix.go/event"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/sdk/resource"
	sdk "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

/*
Tracer wraps an OpenTelemetry TracerProvider. Created by service.New, stored in
context, and used by the public telemetry/trace.Start function.
*/
type Tracer struct {
	provider    oteltrace.TracerProvider
	tracer      oteltrace.Tracer
	sdkProvider *sdk.TracerProvider
}

/*
Start creates an OpenTelemetry Span and a context containing it.

If the context contains a Span then the newly-created Span will be a child of
that Span, otherwise it will be a root Span.

Any Span that is created must also be ended. This is the responsibility of the
caller.
*/
func (t *Tracer) Start(ctx context.Context, kind oteltrace.SpanKind, name string) (context.Context, oteltrace.Span) {
	var attrs []attribute.KeyValue

	if e, ok := event.EventFromContext(ctx); ok {
		if mapped := event.ToFlatMap(e); len(mapped) > 0 {
			members := make([]baggage.Member, 0, len(mapped))
			attrs = make([]attribute.KeyValue, 0, len(mapped))

			for k, v := range mapped {
				attrs = append(attrs, attribute.String(k, v))
				if m, err := baggage.NewMember(flatMapKeyToBaggageKey(k), v); err == nil {
					members = append(members, m)
				}
			}

			if len(members) > 0 {
				if b, err := baggage.New(members...); err == nil {
					ctx = baggage.ContextWithBaggage(ctx, b)
				}
			}
		}
	}

	ctx, span := t.tracer.Start(ctx, name, oteltrace.WithSpanKind(kind))
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	return ctx, span
}

/*
Provider returns the underlying OpenTelemetry TracerProvider. Integrations that
need to wire OTEL-native interceptors (e.g., Temporal) use this.
*/
func (t *Tracer) Provider() oteltrace.TracerProvider {
	return t.provider
}

/*
Shutdown gracefully shuts down the tracer's provider, flushing pending spans.
*/
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t.sdkProvider == nil {
		return nil
	}

	return t.sdkProvider.Shutdown(ctx)
}

/*
NewTracer creates a new Tracer with an exporter auto-detected from the
OTEL_TRACES_EXPORTER environment variable (defaults to OTLP). The OTLP
exporter respects all standard OTEL_EXPORTER_OTLP_* environment variables.
The caller is responsible for global OpenTelemetry registration
(otel.SetTracerProvider, otel.SetTextMapPropagator).
*/
func NewTracer(res *resource.Resource) (*Tracer, error) {
	ctx := context.Background()

	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}

	provider := sdk.NewTracerProvider(
		sdk.WithResource(res),
		sdk.WithBatcher(exporter),
	)

	// Wrap the provider so that every span created — whether by helix internals,
	// otelhttp, Temporal, or any other OTEL-native integration — automatically
	// gets its status set to codes.Ok on End() unless an error was recorded.
	// Without this, third-party libraries that create and end raw OTEL spans
	// would leave the status as "Unset".
	wrapped := &statusTracerProvider{TracerProvider: provider}

	return &Tracer{
		provider:    wrapped,
		tracer:      wrapped.Tracer("github.com/mountayaapp/helix.go"),
		sdkProvider: provider,
	}, nil
}

/*
NewNopTracer creates a Tracer that produces no-op spans and does not export.
*/
func NewNopTracer() *Tracer {
	provider := noop.NewTracerProvider()
	return &Tracer{
		provider: provider,
		tracer:   provider.Tracer("noop"),
	}
}

/*
flatMapKeyToBaggageKey converts a flat map key (e.g. "event[app][name]") to a
dot-separated baggage key (e.g. "event.app.name").
*/
func flatMapKeyToBaggageKey(k string) string {
	k = strings.ReplaceAll(k, "[", ".")
	k = strings.ReplaceAll(k, "].", ".")
	k = strings.ReplaceAll(k, "]", "")
	return k
}
