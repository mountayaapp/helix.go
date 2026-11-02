// Package log provides structured logging via the Service's logger, with automatic
// OpenTelemetry export and trace correlation.
//
// Every function logs through the Logger carried by its context, which the
// integrations put there. A context that carries none — one built in main, or
// detached for background work — falls back to the process-wide Logger: the
// Service's own, which service.New installs unless Config.SkipGlobals says not to,
// and otherwise one writing to the global OpenTelemetry LoggerProvider. The
// telemetry/trace package falls back the same way.
package log
