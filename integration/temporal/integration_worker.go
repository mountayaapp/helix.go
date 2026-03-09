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
Name returns the string representation of the Temporal integration.
*/
func (conn *serverConnection) Name() string {
	return identifier
}

/*
Start starts the Temporal worker. It respects both context cancellation and OS
interrupt signals (SIGINT/SIGTERM) for graceful shutdown.
*/
func (conn *serverConnection) Start(ctx context.Context) error {
	stack := errorstack.New("Failed to start worker", errorstack.WithIntegration(identifier))

	// Merge context cancellation with OS interrupt signals so that the worker
	// stops on whichever comes first.
	interruptCh := worker.InterruptCh()
	doneCh := make(chan any, 1)
	go func() {
		select {
		case <-ctx.Done():
			doneCh <- struct{}{}
		case sig := <-interruptCh:
			doneCh <- sig
		}
	}()

	err := conn.worker.Run(doneCh)
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
	return checkHealth(ctx, conn.client)
}
