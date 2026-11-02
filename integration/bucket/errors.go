package bucket

import (
	"errors"

	"gocloud.dev/gcerrors"
)

// ErrNotFound is the error a Bucket reports when there is no blob at the key it
// was asked to read or delete. The error returned is not ErrNotFound itself but
// wraps it, alongside the driver's own error, so test for it with errors.Is or
// IsNotFound:
//
//	value, err := b.Read(ctx, key)
//	if bucket.IsNotFound(err) {
//	  // There is no blob at key.
//	}
//
// A missing blob is an answer rather than a failure, so it is not recorded as an
// error on the operation's span.
var ErrNotFound = errors.New("blob not found")

// IsNotFound reports whether err means there is no blob at the key. It is a
// shorthand for errors.Is(err, ErrNotFound).
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// notFoundError is a driver's not-found error marked as ErrNotFound. It keeps the
// driver's error in the chain and its message as its own, so that neither
// gcerrors.Code nor anything matching on the message stops working.
type notFoundError struct {
	cause error
}

// Error returns the driver's message.
func (e *notFoundError) Error() string {
	return e.cause.Error()
}

// Unwrap returns both ErrNotFound and the driver's error, so that errors.Is and
// errors.As reach either.
func (e *notFoundError) Unwrap() []error {
	return []error{ErrNotFound, e.cause}
}

// asNotFound returns err marked as ErrNotFound when it is the driver reporting a
// missing blob, and nil otherwise.
func asNotFound(err error) error {
	if err == nil || gcerrors.Code(err) != gcerrors.NotFound {
		return nil
	}

	return &notFoundError{cause: err}
}
