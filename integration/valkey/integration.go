package valkey

import (
	"context"

	"github.com/mountayaapp/helix.go/integration"
)

// Ensure dependency complies to the integration.Dependency type.
var _ integration.Dependency = dependency{}

// dependency adapts a connection to integration.Dependency, and is what gets
// registered with the Service.
//
// It is a distinct type rather than methods on connection so that the value handed
// back to the caller does not carry the lifecycle contract — not even reachable
// through a type assertion. The Service owns the Valkey server;
// the caller only gets to use it.
type dependency struct {
	conn *connection
}

// Close tries to gracefully close the connection with the Valkey server.
func (d dependency) Close(ctx context.Context) error {
	d.conn.client.Close()

	return nil
}

// CheckHealth reports whether the integration can reach the Valkey server.
func (d dependency) CheckHealth(ctx context.Context) error {
	return integration.Unhealthy(d.conn.client.Do(ctx, d.conn.client.B().Ping().Build()).Error())
}
