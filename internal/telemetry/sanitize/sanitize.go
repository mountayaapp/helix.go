package sanitize

import (
	"errors"
	"sync/atomic"
)

// registered is the function Set registered, if any.
var registered atomic.Pointer[func(err error) error]

// ErrPanicked is what Error returns in place of an error whose sanitizer
// panicked. The error itself is not returned: nothing says the sanitizer was done
// with it.
var ErrPanicked = errors.New("the error sanitizer panicked")

// Set registers sanitize as the function every error goes through before
// telemetry writes it, and returns the one registered before, or nil. A nil
// sanitize leaves nothing registered.
func Set(sanitize func(err error) error) func(err error) error {
	var previous *func(err error) error
	if sanitize == nil {
		previous = registered.Swap(nil)
	} else {
		previous = registered.Swap(&sanitize)
	}

	if previous == nil {
		return nil
	}

	return *previous
}

// Registered reports whether a function is registered.
func Registered() bool {
	return registered.Load() != nil
}

// Error returns what the registered function makes of err, and err itself when
// none is registered. A nil err stays nil, and the function is never handed one.
// A function that panics is recovered, and ErrPanicked returned in place of what
// it would have.
func Error(err error) (sanitized error) {
	if err == nil {
		return nil
	}

	sanitize := registered.Load()
	if sanitize == nil {
		return err
	}

	defer func() {
		if recover() != nil {
			sanitized = ErrPanicked
		}
	}()

	return (*sanitize)(err)
}
