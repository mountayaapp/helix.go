package rest

import (
	"fmt"
	"net/http"

	"github.com/mountayaapp/helix.go/integration"
)

// panicError is a recovered panic, as an error: the type a request's span has
// always recorded a handler's panic under.
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

// recoverPanics answers a request whose handling panicked with the 500 error
// envelope, INTERNAL_ERROR, when nothing of its response has been sent yet.
//
// Without it a panic unwinds to net/http, which closes the connection with no
// status line at all: the client sees the connection drop, or, behind a proxy, an
// opaque 502 with no envelope and none of the headers the edge had already set,
// so not even a request id to quote. A 500 is what every operation's description
// says a failure of the server is answered with, and it is what the GraphQL
// integration already answers a panicking resolver with.
//
// It sits just inside Config.EdgeMiddleware, so it covers the application's
// Config.Middleware as well as the route chain and the handler, and the 500 it
// writes carries whatever the edge put on the response before passing the request
// on. A deferred function further in (one that settles a resource or records the
// request, then lets the panic go on) still runs first, because it is deeper in
// the stack.
//
// What the answer carries is restored to the headers the response had when the
// request reached it: a header the handler set for the answer it never finished,
// a Cache-Control or an ETag, does not describe the 500.
//
// A panic once the response has started (a stream already flushed, a body partly
// written, a hijacked connection) cannot be answered any more: it is logged and
// recorded the same way, and the response is then aborted with
// http.ErrAbortHandler, which net/http closes the connection on without logging
// it a second time. http.ErrAbortHandler itself, a handler's deliberate abort, goes
// on unchanged.
//
// The recovery itself is the one every HTTP server integration shares
// (integration.RecoverPanics).
func recoverPanics(next http.Handler) http.Handler {
	return integration.RecoverPanics(next, "Recovered from a panic while serving a REST request", func(v any) error {
		return panicError{value: v}
	})
}
