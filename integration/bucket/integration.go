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
	if err := errorstack.Wrap(conn.client.Close(), "Failed to gracefully close connection"); err != nil {
		return err
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

	if err != nil {
		return 503, errorstack.Wrap(err, "Integration is not in a healthy state",
			errorstack.WithCode(errorstack.CodeServiceUnavailable),
		)
	}

	return 503, errorstack.New("Integration is not in a healthy state",
		errorstack.WithCode(errorstack.CodeServiceUnavailable),
	)
}
