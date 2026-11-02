package integration

import (
	"context"
)

// Server defines the lifecycle of a server integration (REST, GraphQL, Temporal
// Worker). Multiple servers can be registered on a single Service: a REST API
// alongside a Temporal worker is an ordinary shape.
//
// A Server does not name itself: the name is supplied by the application at Serve
// time.
type Server interface {

	// Start begins accepting work. It blocks until the server stops accepting:
	// because Stop was called, or because it failed. Returning nil means the
	// server terminated on purpose; the Service then stops too.
	//
	// ctx carries the Service's lifetime as the application handed it to Run, and
	// is cancelled when the application asks to shut down. A server should not
	// stop accepting on it — Stop is what drains — but may use it for work that
	// belongs to its startup.
	Start(ctx context.Context) error

	// Stop gracefully drains in-flight work. It is called once, when the Service
	// stops, and must be safe to call on a server that never started.
	Stop(ctx context.Context) error

	// CheckHealth reports whether the server is usable. It returns nil when
	// healthy, and an error describing the failure otherwise.
	CheckHealth(ctx context.Context) error
}
