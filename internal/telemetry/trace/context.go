package trace

import (
	"context"
)

/*
tracerKeyIdentifier is the unique internal type for storing a Tracer in context.
*/
type tracerKeyIdentifier struct{}

var tracerKey tracerKeyIdentifier

/*
ContextWithTracer returns a copy of the context with the Tracer associated to it.
*/
func ContextWithTracer(ctx context.Context, t *Tracer) context.Context {
	return context.WithValue(ctx, tracerKey, t)
}

/*
TracerFromContext returns the Tracer stored in the context, if any. Returns nil
if no Tracer is found.
*/
func TracerFromContext(ctx context.Context) *Tracer {
	t, _ := ctx.Value(tracerKey).(*Tracer)
	return t
}
