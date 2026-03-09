package integration

import (
	"context"
)

/*
Server defines the lifecycle of a server integration (REST, GraphQL, Temporal
Worker). Only one server can be registered per Service.
*/
type Server interface {

	// Name returns a human-readable name for logging and error context.
	//
	// Examples:
	//
	//   "REST"
	//   "GraphQL"
	//   "Temporal Worker"
	Name() string

	// Start blocks, processing work until ctx is cancelled or an error occurs.
	Start(ctx context.Context) error

	// Stop gracefully drains in-flight work.
	Stop(ctx context.Context) error

	// Status returns an HTTP-equivalent status code and optional error.
	Status(ctx context.Context) (int, error)
}
