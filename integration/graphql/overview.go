// Package graphql exposes an opinionated GraphQL API server built on top of gqlgen.
// It handles the HTTP server lifecycle, the health endpoints, and integrates with
// OpenTelemetry for distributed tracing.
//
// The server is safe to expose by default: request bodies, parser tokens, query
// depth and query complexity are bounded, field suggestions are off unless
// introspection is on, and an error a resolver returns reaches the client only
// when it was written for one — an *errorstack.Error, or a *gqlerror.Error — and
// is masked as an internal error otherwise.
package graphql

// identifier represents the integration's unique identifier.
const identifier = "graphql"

// humanized represents the integration's humanized name.
const humanized = "GraphQL"
