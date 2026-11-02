// Package service provides the foundation for building microservices with
// OpenTelemetry observability built in. Every integration — REST, GraphQL, Temporal,
// database connections, etc. — ships with distributed tracing, structured logging
// with OpenTelemetry export, error recording, and health checks. No manual
// instrumentation, no boilerplate.
//
// More than one Service may exist in a process. Global registration — the
// OpenTelemetry providers and propagator, and the default logger and tracer that
// telemetry/log and telemetry/trace fall back to — is the only genuinely
// once-per-process act here, and Config.SkipGlobals is what controls it — so a
// second Service is built with it set.
//
// # One vocabulary
//
// Every span helix emits is named "<Humanized>: <Operation>" and carries
// attribute keys of the form "<identifier>.<path>" — "PostgreSQL: Exec" with
// postgres.query, "Bucket: Write" with bucket.key. Integrations build them
// through integration.Instrument, which is the only place either is constructed,
// and service/servicetest.AssertConvention is what holds them to it.
//
// The cost of a namespace of our own is that a backend's built-in HTTP and
// database views, which key off the OpenTelemetry semantic conventions, do not
// recognise these attributes. The alternative was worse: the upstream
// instrumentation packages are each pinned to whichever semantic convention
// version they happened to adopt, years apart from one another, and composing
// them produces one span written in two vocabularies at once.
//
// # Suppressed instrumentation
//
// The tracer and meter providers a Service resolves are filtered views, and one
// third-party instrumentation scope is discarded rather than exported:
// gocloud.dev/blob, which the bucket integration uses.
//
// gocloud creates its spans and metrics from the global providers, with no way to
// inject one and no flag to turn them off, so a single bucket operation produced
// two spans — the integration's, carrying the driver, bucket and key, and
// gocloud's nested inside it, carrying one gocdk_method attribute in a vocabulary
// nothing else here uses — and two latency measurements. Suppressing it is what
// makes one operation mean one span and one measurement.
//
// This is the only place helix discards somebody else's telemetry, and it is
// documented here because doing so silently would be worse than the duplication.
package service
