package service

import (
	"context"

	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/internal/telemetry/log"
	"github.com/mountaya/helix.go/internal/telemetry/trace"

	otellog "go.opentelemetry.io/otel/log"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Serve registers a server integration under the given name for the given
// Service. Servers define how the service accepts work: a REST API, a GraphQL
// API, a Temporal worker. More than one may be registered; Run starts them all
// and Stop drains them all.
//
// The name identifies the server in health output and error context, and must be
// unique among the servers of this Service.
//
// This is part of the integration API. End-users don't call this directly;
// integration constructors (rest.New, graphql.New, temporal.New) call it.
func Serve(svc *Service, name string, server integration.Server) error {
	return svc.serve(name, server)
}

// Attach registers a dependency integration under the given name for the given
// Service. Dependencies are connections to external systems: databases, caches,
// blob storage. Their Close method is automatically called when the Service
// stops.
//
// The name identifies the dependency in health output and error context, and must
// be unique among the dependencies of this Service. Two pools of the same kind —
// a primary and a replica — are told apart by it.
//
// This is part of the integration API. End-users don't call this directly;
// integration constructors (postgres.Connect, valkey.Connect, etc.) call it.
func Attach(svc *Service, name string, dep integration.Dependency) error {
	return svc.attach(name, dep)
}

// Context returns a copy of the given context enriched with the Service's logger
// and tracer. Integrations call this to propagate observability into
// request/workflow contexts so that the telemetry/log and telemetry/trace packages
// can extract them.
//
// This is part of the integration API.
func Context(ctx context.Context, svc *Service) context.Context {
	ctx = log.NewContext(ctx, svc.logger)
	ctx = trace.NewContext(ctx, svc.tracer)

	return ctx
}

// TracerProvider returns the underlying OpenTelemetry TracerProvider. Integrations
// hand it to integration.NewInstrument, and the two that wire an
// OpenTelemetry-native interceptor - Temporal's tracing interceptor - use it
// directly.
//
// Note that what comes back is a filtered view of the provider: see the note on
// suppressed instrumentation in Service.
//
// This is part of the integration API.
func TracerProvider(svc *Service) oteltrace.TracerProvider {
	return svc.tracer.Provider()
}

// LoggerProvider returns the underlying OpenTelemetry LoggerProvider. Integrations
// that need to wire OpenTelemetry-native log bridges use this.
//
// This is part of the integration API.
func LoggerProvider(svc *Service) otellog.LoggerProvider {
	return svc.logger.Provider()
}

// MeterProvider returns the underlying OpenTelemetry MeterProvider. Integrations
// hand it to integration.NewInstrument, which is what keeps their instruments off
// the global provider — and that matters when the Service was built with
// Config.SkipGlobals.
//
// This is part of the integration API.
func MeterProvider(svc *Service) otelmetric.MeterProvider {
	return svc.meterProvider
}

// Propagator returns the TextMapPropagator this Service crosses process
// boundaries with. Integrations hand it to integration.NewInstrument, and the one
// that wires an OpenTelemetry-native interceptor — Temporal's — uses it directly.
//
// Reading it from here rather than from otel.GetTextMapPropagator is what keeps
// propagation working under Config.SkipGlobals, where the global registry holds
// an empty propagator that would extract and inject nothing.
//
// This is part of the integration API.
func Propagator(svc *Service) propagation.TextMapPropagator {
	return svc.propagator
}
