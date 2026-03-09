package errorstack

import (
	"strings"
	"sync"
)

/*
Ensure *Error complies to Go error type.
*/
var _ error = (*Error)(nil)

/*
Error implements the Go native error type and is designed for handling errors
in the helix ecosystem. When exposing errors to clients (such as via HTTP API),
the root error should not give away too much information such as internal messages.
*/
type Error struct {

	// Integration is the name of the integration returning the error, if applicable.
	// Omit integration when working with JSON: we don't want to give internal
	// information to clients consuming HTTP APIs.
	//
	// Examples:
	//
	//   "rest"
	//   "temporal"
	Integration string `json:"-"`

	// Message is the top-level message of the error.
	Message string `json:"message,omitempty"`

	// Validations represents a list of failure validations related to the error
	// itself. This allows to pass/retrieve additional details, such as validation
	// failures encountered in the request payload.
	Validations []Validation `json:"validations,omitempty"`

	// cause is the wrapped error for errors.Unwrap() support.
	cause error

	// mu protects children from concurrent access. Validations are NOT
	// mutex-protected — they must only be mutated from a single goroutine
	// (typically during error construction).
	mu sync.RWMutex

	// children holds child errors encountered in cascade related to the current
	// error.
	children []error
}

/*
Validation holds some details about a validation failure.
*/
type Validation struct {

	// Message is the cause of the validation failure.
	Message string `json:"message"`

	// Path represents the path to the key where the validation failure occurred.
	//
	// Example:
	//
	//   []string{"request", "body", "user", "email"}
	Path []string `json:"path,omitempty"`
}

/*
New returns a new error given the message and options passed.
*/
func New(message string, opts ...Option) *Error {
	return newError(message, opts)
}

/*
Wrap creates a new error that wraps an existing error. The original error is
preserved for errors.Unwrap()/Is()/As() support.
*/
func Wrap(existing error, message string, opts ...Option) *Error {
	if existing == nil {
		return nil
	}

	err := newError(message, opts)
	err.cause = existing
	return err
}

/*
newError allocates an Error, applies all options, and returns it.
*/
func newError(message string, opts []Option) *Error {
	err := &Error{
		Message:     message,
		Validations: []Validation{},
	}

	for _, opt := range opts {
		opt(err)
	}

	return err
}

/*
WithValidations adds validation failures to an error.
*/
func (err *Error) WithValidations(validations ...Validation) *Error {
	err.Validations = append(err.Validations, validations...)
	return err
}

/*
HasValidations indicates if an error encountered validation failures.
*/
func (err *Error) HasValidations() bool {
	return len(err.Validations) > 0
}

/*
WithChildren adds a list of child errors encountered related to the current
error. Nil errors are silently ignored. Thread-safe for concurrent use.
*/
func (err *Error) WithChildren(children ...error) *Error {
	err.mu.Lock()
	defer err.mu.Unlock()

	for _, child := range children {
		if child != nil {
			err.children = append(err.children, child)
		}
	}

	return err
}

/*
HasChildren indicates if an error caused other (a.k.a. children) errors.
Thread-safe for concurrent use.
*/
func (err *Error) HasChildren() bool {
	err.mu.RLock()
	defer err.mu.RUnlock()

	return len(err.children) > 0
}

/*
Unwrap returns the wrapped cause for errors.Is()/errors.As() support.
*/
func (err *Error) Unwrap() error {
	return err.cause
}

/*
Error returns the stringified version of the error, including its validation
failures and children errors. Thread-safe for concurrent reads of children.
*/
func (err *Error) Error() string {
	var b strings.Builder

	if err.Integration != "" {
		b.WriteString(err.Integration)
		b.WriteString(": ")
	}

	if err.Message != "" {
		b.WriteString(err.Message)
	}

	if len(err.Validations) > 0 {
		b.WriteString(". Reasons:\n")

		for _, validation := range err.Validations {
			b.WriteString("    - ")
			b.WriteString(validation.Message)
			if len(validation.Path) > 0 {
				b.WriteString("\n      at ")
				b.WriteString(strings.Join(validation.Path, " > "))
			}

			b.WriteString(".\n")
		}
	} else {
		b.WriteByte('.')
	}

	err.mu.RLock()
	if len(err.children) > 0 {
		b.WriteString(" Caused by:\n\n")
		for _, child := range err.children {
			b.WriteString("- ")
			b.WriteString(child.Error())
			b.WriteByte('\n')
		}
	}
	err.mu.RUnlock()

	return b.String()
}
