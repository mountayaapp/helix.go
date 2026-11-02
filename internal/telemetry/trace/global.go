package trace

import (
	"reflect"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var (

	// defaultTracer is the process-wide Tracer installed by SetDefault, if any.
	defaultTracer atomic.Pointer[Tracer]

	// fallbackTracer is the Tracer Default last built over the global provider. It
	// is reused for as long as the global provider stays the same one, so that a
	// process running with no default installed does not build a Tracer per span.
	fallbackTracer atomic.Pointer[Tracer]
)

// SetDefault installs t as the process-wide Tracer: the one the public
// telemetry/trace package falls back to when a context carries none. service.New
// installs its own alongside the OpenTelemetry globals, unless Config.SkipGlobals
// says not to. It returns the one installed before, or nil.
func SetDefault(t *Tracer) *Tracer {
	return defaultTracer.Swap(t)
}

// Default returns the process-wide Tracer: the one SetDefault installed or, when
// none was, a Tracer over the global OpenTelemetry TracerProvider.
//
// The fallback reads the global provider at each call rather than once, so that
// it always follows the provider installed last. Until one is, the global
// provider discards everything.
func Default() *Tracer {
	if t := defaultTracer.Load(); t != nil {
		return t
	}

	provider := otel.GetTracerProvider()
	if t := fallbackTracer.Load(); t != nil && sameProvider(t.provider, provider) {
		return t
	}

	t := NewTracerWithProvider(provider)
	fallbackTracer.Store(t)

	return t
}

// sameProvider reports whether a and b are the same provider. A provider whose
// dynamic type cannot be compared is never considered the same, rather than
// letting the comparison panic: the cost is a rebuilt fallback, not a crash.
func sameProvider(a, b oteltrace.TracerProvider) bool {
	return a != nil && reflect.TypeOf(a).Comparable() && a == b
}
