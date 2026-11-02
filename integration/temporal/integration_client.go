package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/integration"

	"go.temporal.io/sdk/client"
)

// Ensure *clientConnection complies to the integration.Dependency type.
var _ integration.Dependency = (*clientConnection)(nil)

// clientConnection represents a Temporal client connection registered as a
// dependency: the one Connect dials, and the one a worker built by New polls with.
//
// It is a separate, unexported type rather than methods on Client for a reason:
// Client is handed to the end-user so they can execute and signal workflows, and
// putting Close on it would let them tear down a connection the Service owns.
//
// A worker's client is a dependency rather than part of the worker's server for
// the same reason a database is: the Service closes dependencies only once every
// server has drained, and a REST handler still draining may be starting a workflow
// through the Client New returned when the worker stops.
type clientConnection struct {
	client client.Client
}

// Close gracefully closes the Temporal client's connection with the server.
func (conn *clientConnection) Close(ctx context.Context) error {
	conn.client.Close()
	return nil
}

// CheckHealth reports whether the integration can reach the Temporal server.
func (conn *clientConnection) CheckHealth(ctx context.Context) error {
	return checkHealth(ctx, conn.client)
}
