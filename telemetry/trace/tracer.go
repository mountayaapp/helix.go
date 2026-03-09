package trace

import (
	"context"

	"github.com/mountayaapp/helix.go/internal/telemetry/trace"

	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"
)

/*
Start creates a Span and a context containing the newly-created Span.

If the context contains a Span then the newly-created Span will be a child of
that Span, otherwise it will be a root Span.

Any Span that is created must also be ended. This is the responsibility of the
caller. If no Tracer is found in the context, returns a no-op Span.
*/
func Start(ctx context.Context, kind SpanKind, name string) (context.Context, *Span) {
	t := trace.TracerFromContext(ctx)
	if t != nil {
		ctx, span := t.Start(ctx, kind, name)
		return ctx, NewSpan(span)
	}

	// Fall back to globally registered provider set by service.New().
	// This path skips event-to-baggage propagation (handled by the context-
	// based tracer) but still creates proper named spans with correct status.
	ctx, span := otel.Tracer("github.com/mountayaapp/helix.go").Start(ctx, name, oteltrace.WithSpanKind(oteltrace.SpanKind(kind)))
	return ctx, NewSpan(span)
}
