// Package sanitize holds the function an application registers to restate an
// error before telemetry writes it: on a span, and in a log record. End-users
// register it through the public telemetry/trace package (SetErrorSanitizer).
package sanitize
