package bucket

import (
	"context"
	"errors"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"
)

// errNotAccessible reports a bucket the driver found not accessible — mostly, one
// that does not exist — so that the "why" of a health or Connect failure is never
// empty.
var errNotAccessible = errors.New("bucket is not accessible")

// Ensure dependency complies to the integration.Dependency type.
var _ integration.Dependency = dependency{}

// dependency adapts a connection to integration.Dependency, and is what gets
// registered with the Service.
//
// It is a distinct type rather than methods on connection so that the value handed
// back to the caller does not carry the lifecycle contract — not even reachable
// through a type assertion. The Service owns the bucket;
// the caller only gets to use it.
type dependency struct {
	conn *connection
}

// Close tries to gracefully close the connection with the bucket. Only the first
// call reaches the driver; a later one returns nil.
func (d dependency) Close(ctx context.Context) error {
	if err := d.conn.close(); err != nil {
		return errorstack.Wrap(err, "Failed to gracefully close connection",
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	return nil
}

// CheckHealth reports whether the integration can reach the bucket, by listing
// it — the same check Connect makes.
func (d dependency) CheckHealth(ctx context.Context) error {
	return integration.Unhealthy(accessible(ctx, d.conn.client))
}
