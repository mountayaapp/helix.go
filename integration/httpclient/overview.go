/*
Package httpclient exposes an opinionated HTTP client to communicate with
external HTTP APIs.

A single client can target one or more interchangeable endpoints: requests are
round-robined across them with automatic failover, while the health of every
endpoint is reported through the Service's readiness status. It brings automatic
distributed tracing and registers itself as a dependency of the Service via
service.Attach.
*/
package httpclient

/*
identifier represents the integration's unique identifier.
*/
const identifier = "httpclient"

/*
humanized represents the integration's humanized name.
*/
const humanized = "HTTP Client"
