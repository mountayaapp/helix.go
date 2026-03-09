package service

import (
	"context"

	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/internal/telemetry/log"
	"github.com/mountayaapp/helix.go/internal/telemetry/trace"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	oteltrace "go.opentelemetry.io/otel/trace"
)

/*
Serve registers a server integration for the given Service. Only one server can
be registered — calling Serve twice returns an error. Server integrations define
how the service accepts work: REST API, GraphQL API, or Temporal Worker.

This is part of the integration API. End-users don't call this directly;
integration constructors (rest.New, graphql.New, temporal.New) call it.
*/
func Serve(svc *Service, server integration.Server) error {
	return svc.serve(server)
}

/*
Attach registers a dependency integration for the given Service. Dependencies
are connections to external systems: databases, caches, blob storage, etc. The
dependency's Close method is automatically called when the Service stops.

This is part of the integration API. End-users don't call this directly;
integration constructors (postgres.Connect, valkey.Connect, etc.) call it.
*/
func Attach(svc *Service, dep integration.Dependency) error {
	return svc.attach(dep)
}

/*
Context returns a copy of the given context enriched with the Service's logger
and tracer. Integrations call this to propagate observability into
request/workflow contexts so that the telemetry/log and telemetry/trace packages
can extract them.

This is part of the integration API.
*/
func Context(svc *Service, ctx context.Context) context.Context {
	ctx = log.ContextWithLogger(ctx, svc.logger)
	ctx = trace.ContextWithTracer(ctx, svc.tracer)
	return ctx
}

/*
TracerProvider returns the underlying OpenTelemetry TracerProvider. Integrations
that need to wire OTEL-native interceptors (e.g., Temporal) use this.

This is part of the integration API.
*/
func TracerProvider(svc *Service) oteltrace.TracerProvider {
	return svc.tracer.Provider()
}

/*
LoggerProvider returns the underlying OpenTelemetry LoggerProvider. Integrations
that need to wire OpenTelemetry-native log processors use this.

This is part of the integration API.
*/
func LoggerProvider(svc *Service) *sdklog.LoggerProvider {
	return svc.logger.Provider()
}
