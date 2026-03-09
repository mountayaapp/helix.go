package postgres

import (
	"context"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
)

/*
Ensure *connection complies to the integration.Dependency type.
*/
var _ integration.Dependency = (*connection)(nil)

/*
Name returns the string representation of the PostgreSQL integration.
*/
func (conn *connection) Name() string {
	return identifier
}

/*
Close tries to gracefully close the connection with the PostgreSQL database.
*/
func (conn *connection) Close(ctx context.Context) error {
	conn.client.Close()

	return nil
}

/*
Status indicates if the integration is able to ping the PostgreSQL database or
not. Returns `200` if connection is working, `503` otherwise.
*/
func (conn *connection) Status(ctx context.Context) (int, error) {
	err := conn.client.Ping(ctx)
	if err == nil {
		return 200, nil
	}

	stack := errorstack.New("Integration is not in a healthy state", errorstack.WithIntegration(identifier))
	stack.WithValidations(errorstack.Validation{
		Message: err.Error(),
	})

	return 503, stack
}
