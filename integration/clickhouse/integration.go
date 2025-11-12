package clickhouse

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
)

/*
Ensure *connection complies to the integration.Integration type.
*/
var _ integration.Integration = (*connection)(nil)

/*
String returns the string representation of the ClickHouse integration.
*/
func (conn *connection) String() string {
	return identifier
}

/*
Start does nothing since the ClickHouse integration only exposes a client to
communicate with a ClickHouse database.
*/
func (conn *connection) Start(ctx context.Context) error {
	return nil
}

/*
Close tries to gracefully close the connection with the ClickHouse database.
*/
func (conn *connection) Close(ctx context.Context) error {
	stack := errorstack.New("Failed to gracefully close connection with database", errorstack.WithIntegration(identifier))

	err := conn.client.Close()
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: err.Error(),
		})

		return stack
	}

	return nil
}

/*
Status indicates if the integration is able to ping the ClickHouse database or
not. Returns `200` if connection is working, `503` otherwise.
*/
func (conn *connection) Status(ctx context.Context) (int, error) {
	stack := errorstack.New("Integration is not in a healthy state", errorstack.WithIntegration(identifier))

	err := conn.client.Ping(ctx)
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: err.Error(),
		})

		return 503, stack
	}

	return 200, nil
}
