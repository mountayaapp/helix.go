package postgres

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
// through a type assertion. The Service owns the PostgreSQL database;
// the caller only gets to use it.
type dependency struct {
	conn *connection
}

// Close closes every Listener still open, then the pool.
//
// Closing the pool waits for every connection a caller still holds — a Rows not
// yet closed, a transaction not yet committed or rolled back — to be released
// before closing it, and the driver offers no way to bound that wait. Close
// bounds it with ctx instead: when ctx ends first, it returns an error and the
// pool finishes closing in the background, closing each held connection as it
// is released. A connection that is never released is never closed, which is a
// leak in the code holding it.
func (d dependency) Close(ctx context.Context) error {

	// Errors closing a Listener are not reported: its connection is closed
	// whatever the server made of the goodbye, and there is nothing left to do
	// about it at shutdown.
	for _, l := range d.conn.shutdown() {
		_ = l.Close(ctx)
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		d.conn.client.Close()
	}()

	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		return errorstack.Wrap(ctx.Err(), "Failed to close every connection in time; some were still in use",
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(ctx.Err())),
		)
	}
}

// CheckHealth reports whether the integration can reach the PostgreSQL database.
//
// The ping is not traced: the readiness probe runs it on every poll, and the
// acquire — and connect, when the pool is empty — it goes through would
// otherwise add root spans describing the probe rather than the service's work.
func (d dependency) CheckHealth(ctx context.Context) error {
	return integration.Unhealthy(d.conn.client.Ping(untraced(ctx)))
}
