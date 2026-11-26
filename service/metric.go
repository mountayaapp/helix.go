package service

import (
	"github.com/mountaya/helix.go/internal/telemetry/trace"

	otelmetric "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

// meterScopeFilter is a MeterProvider that hands back a no-op meter for the
// instrumentation scopes helix suppresses, and delegates every other scope to the
// provider it wraps.
//
// It is the metric half of the suppression the tracer provider applies, over the
// same scopes: gocloud.dev/blob records its latency and byte counters on the global
// meter just as it opens spans from the global tracer, in a vocabulary nothing
// else here uses, measuring the same operations the bucket integration already
// measures.
type meterScopeFilter struct {
	otelmetric.MeterProvider
}

// Meter returns a no-op meter for a dropped scope, and the wrapped provider's
// meter for every other.
func (p meterScopeFilter) Meter(name string, opts ...otelmetric.MeterOption) otelmetric.Meter {
	if trace.DroppedScopes[name] {
		return metricnoop.NewMeterProvider().Meter(name)
	}

	return p.MeterProvider.Meter(name, opts...)
}

// withDroppedMeterScopes wraps provider so that the scopes in trace.DroppedScopes
// produce no-op meters.
func withDroppedMeterScopes(provider otelmetric.MeterProvider) otelmetric.MeterProvider {
	if len(trace.DroppedScopes) == 0 {
		return provider
	}

	return meterScopeFilter{MeterProvider: provider}
}
