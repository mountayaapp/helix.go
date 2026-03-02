package integration

import (
	"context"
)

/*
Dependency describes the lifecycle of a dependency integration. A dependency
connects to an external system: databases, caches, blob storage, etc. Multiple
dependencies can be registered per service. Dependencies connect eagerly in
their constructor — they have no Start phase.
*/
type Dependency interface {

	// String returns the string representation of the integration.
	//
	// Examples:
	//
	//   "postgres"
	//   "valkey"
	//   "clickhouse"
	//   "bucket"
	//   "temporal"
	String() string

	// Close gracefully closes the connection with the external system.
	Close(ctx context.Context) error

	// Status executes a health check of the dependency. It returns an equivalent
	// HTTP status code of the health. It should most likely be `200` or `503`.
	// If the dependency is unhealthy, it may return an error as well depending
	// on the underlying client.
	Status(ctx context.Context) (int, error)
}
