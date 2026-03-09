package integration

import (
	"context"
)

/*
Dependency defines the lifecycle of a dependency integration (databases, caches,
blob storage). Multiple dependencies can be attached to a single Service.
*/
type Dependency interface {

	// Name returns a human-readable name for logging and error context.
	//
	// Examples:
	//
	//   "PostgreSQL"
	//   "Valkey"
	//   "Bucket"
	Name() string

	// Close gracefully closes the connection.
	Close(ctx context.Context) error

	// Status returns an HTTP-equivalent status code and optional error.
	Status(ctx context.Context) (int, error)
}
