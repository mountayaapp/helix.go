// Package trace provides distributed tracing via the Service's OpenTelemetry tracer.
//
// Every function starts its span from the Tracer carried by its context, which the
// integrations put there. A context that carries none — one built in main, or
// detached for background work — falls back to the process-wide Tracer: the
// Service's own, which service.New installs unless Config.SkipGlobals says not to,
// and otherwise one over the global OpenTelemetry TracerProvider. The telemetry/log
// package falls back the same way.
package trace
