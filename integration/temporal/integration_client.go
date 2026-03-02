package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"
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
String returns the string representation of the Temporal integration.
*/
func (conn *clientConnection) String() string {
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
	stack := errorstack.New("Integration is not in a healthy state", errorstack.WithIntegration(identifier))

	_, err := conn.client.CheckHealth(ctx, &client.CheckHealthRequest{})
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: err.Error(),
		})

		return 503, stack
	}

	return 200, nil
}
