package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

/*
Ensure *serverConnection complies to the integration.Server type.
*/
var _ integration.Server = (*serverConnection)(nil)

/*
serverConnection represents a Temporal worker connection registered as a server.
*/
type serverConnection struct {
	client client.Client
	worker worker.Worker
}

/*
String returns the string representation of the Temporal integration.
*/
func (conn *serverConnection) String() string {
	return identifier
}

/*
Start starts the Temporal worker.
*/
func (conn *serverConnection) Start(ctx context.Context) error {
	stack := errorstack.New("Failed to start worker", errorstack.WithIntegration(identifier))

	err := conn.worker.Run(worker.InterruptCh())
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: err.Error(),
		})

		return stack
	}

	return nil
}

/*
Stop gracefully stops the Temporal worker and closes the client's connection
with the server.
*/
func (conn *serverConnection) Stop(ctx context.Context) error {
	conn.worker.Stop()
	conn.client.Close()
	return nil
}

/*
Status indicates if the integration is able to connect to the Temporal server or
not. Returns `200` if connection is working, `503` otherwise.
*/
func (conn *serverConnection) Status(ctx context.Context) (int, error) {
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
