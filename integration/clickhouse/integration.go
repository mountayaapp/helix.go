package clickhouse

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
)

// Ensure dependency complies to the integration.Dependency type.
var _ integration.Dependency = dependency{}

// dependency adapts a connection to integration.Dependency, and is what gets
// registered with the Service.
//
// It is a distinct type rather than methods on connection so that the value handed
// back to the caller does not carry the lifecycle contract — not even reachable
// through a type assertion. The Service owns the ClickHouse database;
// the caller only gets to use it.
type dependency struct {
	conn *connection
}

// Close tries to gracefully close the connection with the ClickHouse database.
// The pool's metrics are unregistered first, so that no collection reads a pool
// that is being torn down.
func (d dependency) Close(ctx context.Context) error {
	d.conn.pool.unregister()

	if err := d.conn.client.Close(); err != nil {
		return errorstack.Wrap(err, "Failed to gracefully close connection",
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	return nil
}

// CheckHealth reports whether the integration can reach the ClickHouse database.
func (d dependency) CheckHealth(ctx context.Context) error {
	return integration.Unhealthy(d.conn.client.Ping(ctx))
}
