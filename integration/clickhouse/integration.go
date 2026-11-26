package clickhouse

import (
	"context"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"
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
// The prober is stopped, waited for within ctx, and the pool's metrics are
// unregistered first, so that nothing reads a pool that is being torn down.
func (d dependency) Close(ctx context.Context) error {
	d.conn.prober.close(ctx)
	d.conn.pool.unregister()

	if err := d.conn.client.Close(); err != nil {
		return errorstack.Wrap(err, "Failed to gracefully close connection",
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	return nil
}

// CheckHealth reports whether the integration can reach the ClickHouse database,
// and records it for the clickhouse.reachable gauge.
//
// An optional connection (Config.Optional) is reported healthy at once, without
// a ping: what it reaches does not gate the Service's readiness, and a ping to a
// server that hangs would hold a readiness probe for as long as a dial may last.
// Its prober keeps the gauge current instead.
func (d dependency) CheckHealth(ctx context.Context) error {
	if d.conn.optional {
		return nil
	}

	err := d.conn.client.Ping(ctx)
	d.conn.reachable.Store(err == nil)

	return integration.Unhealthy(err)
}
