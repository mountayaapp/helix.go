package integration

import (
	"context"
)

// Dependency defines the lifecycle of a dependency integration (databases, caches,
// blob storage). Multiple dependencies can be attached to a single Service.
//
// A Dependency does not name itself: the name is supplied by the application at
// Attach time, so that two pools of the same kind remain distinguishable.
type Dependency interface {

	// Close gracefully closes the connection. It is called once, when the Service
	// stops, and must be safe to call on a connection that never became healthy.
	Close(ctx context.Context) error

	// CheckHealth reports whether the dependency is usable. It returns nil when
	// healthy, and an error describing the failure otherwise. It must not return
	// a transport-specific status code: mapping health to a transport is the
	// responsibility of the server exposing the probe.
	CheckHealth(ctx context.Context) error
}
