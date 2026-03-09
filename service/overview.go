/*
Package service provides the foundation for building microservices with
OpenTelemetry observability built in. Every integration — REST, GraphQL, Temporal,
database connections, etc. — ships with distributed tracing, structured logging
with OpenTelemetry export, error recording, and health checks. No manual
instrumentation, no boilerplate.

Only one Service instance is allowed per application. Calling New more than once
returns an error.
*/
package service
