package trace

import (
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// DroppedScopes are the instrumentation scopes whose spans helix suppresses. The
// service package applies the same set to meters.
//
// gocloud.dev/blob is the only entry, and it is there because it leaves no
// choice. It creates its spans from the *global* tracer provider — there is no
// option to inject one and no flag to turn it off — so every bucket operation
// produces two spans: the one the integration opened, carrying the driver, bucket
// and key, and a nested one carrying a single gocdk_method attribute in a
// vocabulary nothing else in this library uses.
//
// Neither obvious mechanism can remove it. A SpanProcessor observes spans, it
// cannot prevent their export. A Sampler is given the name, kind, attributes and
// links of a span but not its instrumentation scope, so it cannot tell whose span
// it is looking at without matching on the name, which is brittle. Replacing the
// tracer itself is what is left.
var DroppedScopes = map[string]bool{
	"gocloud.dev/blob": true,
}

// scopeFilter is a TracerProvider that hands back a no-op tracer for the scopes
// in drop, and delegates every other scope to the provider it wraps.
type scopeFilter struct {
	oteltrace.TracerProvider

	drop map[string]bool
}

// Tracer returns a no-op tracer for a dropped scope, and the wrapped provider's
// tracer for every other.
func (p scopeFilter) Tracer(name string, opts ...oteltrace.TracerOption) oteltrace.Tracer {
	if p.drop[name] {
		return noop.NewTracerProvider().Tracer(name)
	}

	return p.TracerProvider.Tracer(name, opts...)
}

// WithDroppedScopes wraps provider so that the scopes in DroppedScopes produce
// no-op tracers.
//
// service.New applies it to whichever provider it resolved — the one it built
// from the environment, or the one supplied through Config — before installing it
// globally, so the suppression holds identically in production and under
// servicetest.
func WithDroppedScopes(provider oteltrace.TracerProvider) oteltrace.TracerProvider {
	if len(DroppedScopes) == 0 {
		return provider
	}

	return scopeFilter{TracerProvider: provider, drop: DroppedScopes}
}
