package bucket

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
Name returns the string representation of the Bucket integration.
*/
func (conn *connection) Name() string {
	return identifier
}

/*
Close tries to gracefully close the connection with the bucket.
*/
func (conn *connection) Close(ctx context.Context) error {
	err := conn.client.Close()
	if err != nil {
		stack := errorstack.New("Failed to gracefully close connection with bucket", errorstack.WithIntegration(identifier))
		stack.WithValidations(errorstack.Validation{
			Message: err.Error(),
		})

		return stack
	}

	return nil
}

/*
Status indicates if the integration is able to access the bucket or not. Returns
`200` if bucket is accessible, `503` otherwise.
*/
func (conn *connection) Status(ctx context.Context) (int, error) {
	up, err := conn.client.IsAccessible(ctx)
	if up && err == nil {
		return 200, nil
	}

	stack := errorstack.New("Integration is not in a healthy state", errorstack.WithIntegration(identifier))
	if err != nil {
		stack.WithValidations(errorstack.Validation{
			Message: err.Error(),
		})
	}

	return 503, stack
}
