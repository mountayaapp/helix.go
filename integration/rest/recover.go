package rest

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime/debug"

	"github.com/mountayaapp/helix.go/telemetry/log"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	oteltrace "go.opentelemetry.io/otel/trace"
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
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		headers := rw.Header().Clone()
		answer, state := newPanicAwareWriter(rw)

		defer func() {
			v := recover()
			if v == nil {
				return
			}

			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(v)
			}

			err := panicError{value: v}
			log.Error(req.Context(), "Recovered from a panic while serving a REST request",
				log.Err(err),
				log.String("stack", string(debug.Stack())),
			)
			trace.NewSpan(oteltrace.SpanFromContext(req.Context())).RecordError("failed to serve request", err)

			if state.started {
				panic(http.ErrAbortHandler)
			}

			restoreHeaders(rw.Header(), headers)
			NewResponseError[NoMetadata](req).
				SetStatus(http.StatusInternalServerError).
				Write(rw)
		}()

		next.ServeHTTP(answer, req)
	})
}

// restoreHeaders makes header hold exactly what before held.
func restoreHeaders(header http.Header, before http.Header) {
	for key := range header {
		if _, ok := before[key]; !ok {
			delete(header, key)
		}
	}

	for key, values := range before {
		header[key] = values
	}
}

// panicAwareWriter records whether any of the response has been sent, which
// decides whether a panic can still be answered.
//
// It implements Unwrap so that http.ResponseController reaches the real writer,
// and Flush and ReadFrom so that a handler streaming a response is not degraded
// by being wrapped.
type panicAwareWriter struct {
	http.ResponseWriter

	// started reports that a final status, a body byte, a flush or a hijack has
	// reached the writer underneath. An informational 1xx has not started the
	// response: the final one can still follow it.
	started bool
}

// newPanicAwareWriter wraps rw, and returns the writer to hand on along with the
// state to read once it returns. The writer can be hijacked exactly when rw can.
func newPanicAwareWriter(rw http.ResponseWriter) (http.ResponseWriter, *panicAwareWriter) {
	wrapped := &panicAwareWriter{ResponseWriter: rw}
	if hijacker, ok := rw.(http.Hijacker); ok {
		return &hijackablePanicAwareWriter{panicAwareWriter: wrapped, Hijacker: hijacker}, wrapped
	}

	return wrapped, wrapped
}

// Unwrap returns the writer underneath, which is what http.ResponseController
// follows to find what this writer does not implement itself.
func (rw *panicAwareWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// WriteHeader sends the status, and records a final one as the start of the
// response.
func (rw *panicAwareWriter) WriteHeader(status int) {
	if status >= http.StatusOK || status == http.StatusSwitchingProtocols {
		rw.started = true
	}

	rw.ResponseWriter.WriteHeader(status)
}

// Write writes body bytes, which starts the response.
func (rw *panicAwareWriter) Write(b []byte) (int, error) {
	rw.started = true

	return rw.ResponseWriter.Write(b)
}

// Flush sends what was written so far, which starts the response even when
// nothing was: the status and headers go with the first flush.
func (rw *panicAwareWriter) Flush() {
	rw.started = true

	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// ReadFrom copies src into the response, keeping the underlying writer's own
// ReadFrom when it has one.
func (rw *panicAwareWriter) ReadFrom(src io.Reader) (int64, error) {
	rw.started = true

	if readerFrom, ok := rw.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(src)
	}

	// io.Copy would find this method and recurse into it; writeOnly hides it.
	return io.Copy(writeOnly{rw.ResponseWriter}, src)
}

// hijackablePanicAwareWriter is a panicAwareWriter over a writer that can be
// hijacked, for the reason hijackableResponseWriter gives.
type hijackablePanicAwareWriter struct {
	*panicAwareWriter
	http.Hijacker
}

// Hijack hands the connection over. Nothing can be answered on it afterwards.
func (rw *hijackablePanicAwareWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	rw.started = true

	return rw.Hijacker.Hijack()
}
