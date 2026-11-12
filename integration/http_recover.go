package integration

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime/debug"

	"github.com/mountayaapp/helix.go/telemetry/log"
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
)

// panicEvent is the name of the span event the stack of a recovered panic is
// recorded as, and attrKeyStackTrace the attribute of it holding the stack.
const (
	panicEvent        = "panic"
	attrKeyStackTrace = attribute.Key("exception.stacktrace")
)

// recoveredPanic is a recovered panic, as an error.
type recoveredPanic struct {
	value any
}

// Error renders the panic the way the runtime prints it.
func (e recoveredPanic) Error() string {
	return fmt.Sprintf("panic: %v", e.value)
}

// Unwrap returns the value the code panicked with, when it is an error.
func (e recoveredPanic) Unwrap() error {
	err, _ := e.value.(error)
	return err
}

// RecordPanic records a panic an integration recovered from, where the
// operators of the service read it and nowhere a caller does: it is logged,
// under the message logged, with fields and the stack of the goroutine that
// panicked, and recorded on the span ctx carries, the value v as an exception
// under the status message recorded, and the stack as an event beside it. The
// value is logged and recorded as an error, "panic: <v>", which goes through the
// function registered with trace.SetErrorSanitizer as every error helix writes
// does.
//
// It reads the stack itself, so it is called from the deferred function that
// recovered the panic, on the goroutine that panicked, while the frames of the
// panic are still there. What the caller answers the panic with is its own
// business, and holds nothing of v.
func RecordPanic(ctx context.Context, logged string, recorded string, v any, fields ...log.Field) {
	recordPanic(ctx, logged, recorded, recoveredPanic{value: v}, fields)
}

// recordPanic is RecordPanic, the panic being already the error it is logged and
// recorded as.
func recordPanic(ctx context.Context, logged string, recorded string, panicked error, fields []log.Field) {
	stack := string(debug.Stack())

	log.Error(ctx, logged, append(fields[:len(fields):len(fields)],
		log.Err(panicked),
		log.String("stack", stack),
	)...)

	span := trace.SpanFromContext(ctx)
	span.RecordError(recorded, panicked)
	span.AddEvent(panicEvent, attrKeyStackTrace.String(stack))
}

// RecoverPanics wraps next so that a request whose handling panicked is answered
// with the 500 error envelope, INTERNAL_ERROR, when nothing of its response has
// been sent yet. logged is the message the panic is logged under, which names the
// server the request was for.
//
// Without it a panic unwinds to net/http, which closes the connection with no
// status line at all and prints the panic to the process's standard error: the
// client sees the connection drop, or, behind a proxy, an opaque 502 with no
// envelope and none of the headers the server had already set, so not even a
// request id to quote. The envelope written here says nothing of the panic: its
// value and its stack are logged and recorded on the request's span (RecordPanic).
//
// What the answer carries is restored to the headers the response had when the
// request reached this handler: a header set further in for the answer that was
// never finished, a Cache-Control or an ETag, does not describe the 500. A
// deferred function further in (one that settles a resource or records the
// request, then lets the panic go on) still runs first, because it is deeper in
// the stack.
//
// A panic once the response has started (a stream already flushed, a body partly
// written, a hijacked connection) cannot be answered any more: it is logged and
// recorded the same way, and the response is then aborted with
// http.ErrAbortHandler, which net/http closes the connection on without logging
// it a second time. http.ErrAbortHandler itself, a handler's deliberate abort, goes
// on unchanged.
//
// asError makes the error the panic is logged and recorded as from its value, so
// that an integration keeps the type its spans have always recorded a panic
// under. With nil, it is "panic: <v>" of a type of this package's.
func RecoverPanics(next http.Handler, logged string, asError func(v any) error) http.Handler {
	if asError == nil {
		asError = func(v any) error { return recoveredPanic{value: v} }
	}

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

			recordPanic(req.Context(), logged, "failed to serve request", asError(v), nil)

			if state.started {
				panic(http.ErrAbortHandler)
			}

			restoreHeaders(rw.Header(), headers)
			WriteError(rw, req, http.StatusInternalServerError)
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
// hijacked. It is a type of its own so that a writer claims http.Hijacker only
// when the one it wraps does.
type hijackablePanicAwareWriter struct {
	*panicAwareWriter
	http.Hijacker
}

// Hijack hands the connection over. Nothing can be answered on it afterwards.
func (rw *hijackablePanicAwareWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	rw.started = true

	return rw.Hijacker.Hijack()
}
