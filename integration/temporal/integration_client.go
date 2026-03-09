package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/integration"

	"go.temporal.io/sdk/client"
)

/*
Ensure *clientConnection complies to the integration.Dependency type.
*/
var _ integration.Dependency = (*clientConnection)(nil)

/*
clientConnection represents a client-only Temporal connection registered as a
dependency.
*/
type clientConnection struct {
	client client.Client
}

/*
Name returns the string representation of the Temporal integration.
*/
func (conn *clientConnection) Name() string {
	return identifier
}

/*
Close gracefully closes the Temporal client's connection with the server.
*/
func (conn *clientConnection) Close(ctx context.Context) error {
	conn.client.Close()
	return nil
}

/*
Status indicates if the integration is able to connect to the Temporal server or
not. Returns `200` if connection is working, `503` otherwise.
*/
func (conn *clientConnection) Status(ctx context.Context) (int, error) {
	return checkHealth(ctx, conn.client)
}
