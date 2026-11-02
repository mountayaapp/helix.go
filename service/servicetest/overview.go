// Package servicetest builds a Service whose telemetry is recorded in memory, so
// that a test can assert on the spans, log records and metrics an integration
// actually produced.
//
// The library's central promise is automatic instrumentation. Without this
// package there is no way for a user - or for helix itself - to check that the
// instrumentation happened, which is what makes it possible to change how a span
// is produced without changing what is produced.
package servicetest
