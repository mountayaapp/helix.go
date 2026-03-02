package integration

import (
	"context"
)

/*
Server describes the lifecycle of a server integration. A server defines how
the service accepts work: REST API, GraphQL API, or Temporal Worker. Only one
server can be registered per service. Its Start method is blocking — it listens
for and processes incoming work until the service stops.
*/
type Server interface {

	// String returns the string representation of the integration.
	//
	// Examples:
	//
	//   "rest"
	//   "graphql"
	//   "temporal"
	String() string

	// Start starts the server. This function is blocking — it listens for and
	// processes incoming work until the service stops. The service package
	// executes Start in its own goroutine, and returns an error as soon as it
	// returns a non-nil error.
	Start(ctx context.Context) error

	// Stop gracefully stops the server, draining in-flight requests or tasks.
	Stop(ctx context.Context) error

	// Status executes a health check of the server. It returns an equivalent
	// HTTP status code of the health. It should most likely be `200` or `503`.
	// If the server is unhealthy, it may return an error as well depending on
	// the underlying client.
	Status(ctx context.Context) (int, error)
}
