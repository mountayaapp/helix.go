package graphql

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/telemetry/log"
)

// panicError is a recovered panic, as an error.
type panicError struct {
	value any
}

// Error renders the panic the way the runtime prints it.
func (e panicError) Error() string {
	return fmt.Sprintf("panic: %v", e.value)
}

// Unwrap returns the value the code panicked with, when it is an error.
func (e panicError) Unwrap() error {
	err, _ := e.value.(error)
	return err
}

// recoverFunc is the gqlgen recover function wired in graphql.New. gqlgen calls
// it for a resolver that panics, and for a panic anywhere else in the handling
// of a request.
//
// gqlgen's default writes the panic and its stack to stderr, where it is neither
// correlated with the request's trace nor shipped with the Service's logs. This
// logs it through the Service's logger instead, against the request context, so
// the record carries the trace it belongs to.
//
// The error returned is what the client sees, a plain internal error, with the
// panic as its cause: the error presenter keeps that cause for the operation
// span to record, and the resolver span records the panic itself.
func recoverFunc(ctx context.Context, v any) error {
	err := panicError{value: v}
	log.Error(ctx, "Recovered from a panic while serving a GraphQL operation",
		log.Err(err),
		log.String("stack", string(debug.Stack())),
	)

	return errorstack.Wrap(err, "Internal server error")
}
