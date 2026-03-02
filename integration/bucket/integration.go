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
String returns the string representation of the Bucket integration.
*/
func (conn *connection) String() string {
	return identifier
}

/*
Close tries to gracefully close the connection with the bucket.
*/
func (conn *connection) Close(ctx context.Context) error {
	stack := errorstack.New("Failed to gracefully close connection with bucket", errorstack.WithIntegration(identifier))

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
Status indicates if the integration is able to access the bucket or not. Returns
`200` if bucket is accessible, `503` otherwise.
*/
func (conn *connection) Status(ctx context.Context) (int, error) {
	stack := errorstack.New("Integration is not in a healthy state", errorstack.WithIntegration(identifier))

	up, err := conn.client.IsAccessible(ctx)
	if !up || err != nil {
		if err != nil {
			stack.WithValidations(errorstack.Validation{
				Message: err.Error(),
			})
		}

		return 503, stack
	}

	return 200, nil
}
