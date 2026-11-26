package trace

import (
	"context"

	"github.com/mountaya/helix.go/internal/telemetry/trace"
)

// Start creates a Span and a context containing the newly-created Span.
//
// If the context contains a Span then the newly-created Span will be a child of
// that Span, otherwise it will be a root Span.
//
// Any Span that is created must also be ended. This is the responsibility of the
// caller. The Span comes from the context's Tracer, or from the process-wide one
// when the context carries none.
//
// Start does not stamp the context's Event onto the span. Event attributes belong
// on the entry span of a trace, which StartEntry creates; child spans inherit the
// trace without paying to repeat them.
func Start(ctx context.Context, kind SpanKind, name string) (context.Context, *Span) {
	ctx, span := tracerFrom(ctx).Start(ctx, kind, name)
	return ctx, NewSpan(span)
}

// StartEntry creates the entry Span of a trace: the span a server integration
// opens when it accepts work. In addition to what Start does, it stamps the
// attributes of the Event carried by ctx onto the span, and merges the event's
// propagated identifiers into the context's baggage.
//
// Merging preserves baggage set by an upstream service. Only the allow-listed,
// non-personal subset of the Event travels — see event.Event.Propagated.
func StartEntry(ctx context.Context, kind SpanKind, name string) (context.Context, *Span) {
	ctx, span := tracerFrom(ctx).StartEntry(ctx, kind, name)
	return ctx, NewSpan(span)
}

// tracerFrom returns the Tracer carried by ctx or, when it carries none, the
// process-wide one.
func tracerFrom(ctx context.Context) *trace.Tracer {
	if t := trace.FromContext(ctx); t != nil {
		return t
	}

	return trace.Default()
}

// StampEvent stamps the Event carried by ctx onto the span already in ctx, and
// merges the event's allow-listed identifiers into the context's baggage. It
// returns the enriched context.
//
// Server integrations call this once per request, on the entry span. Applications
// only need it when they create an entry span themselves — with Start, event
// attributes are deliberately not repeated on child spans.
func StampEvent(ctx context.Context) context.Context {
	return trace.StampEvent(ctx)
}
