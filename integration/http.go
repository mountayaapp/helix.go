package integration

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
)

// Attribute keys and instrument name suffixes shared by the inbound and outbound
// HTTP instrumentation. They are suffixes rather than keys because each
// integration namespaces them with its own identifier: the same request recorded
// by rest and by httpclient must not land on the same attribute.
const (
	suffixMethod       = "method"
	suffixPath         = "path"
	suffixRoute        = "route"
	suffixStatusCode   = "status_code"
	suffixEndpoint     = "endpoint"
	suffixRequestSize  = "request.size"
	suffixResponseSize = "response.size"
	suffixDuration     = "request.duration"
)

// MethodOther is what a request method outside the HTTP specification is recorded
// as, following the OpenTelemetry semantic conventions. The method is chosen by the
// client, so recording it verbatim would let any client mint a new span name and a
// new time series per request.
const MethodOther = "_OTHER"

// NormalizeMethod returns the request method to name a span or key a measurement
// by: method when it is one the HTTP specification defines, GET when it is empty —
// which net/http reads as GET — and MethodOther otherwise.
func NormalizeMethod(method string) string {
	switch method {
	case "":
		return http.MethodGet
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return MethodOther
	}
}

// durationBuckets are the bucket boundaries of every duration histogram helix
// records, in seconds. They are the ones the OpenTelemetry semantic conventions
// recommend for HTTP and database durations. Without them a histogram gets the
// SDK's default boundaries, which were chosen for milliseconds and would put
// every sub-five-second operation in the first bucket.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

// sizeBuckets are the bucket boundaries of every body size histogram, in bytes:
// powers of four from 64 bytes to 64 MiB. The SDK's defaults stop at 10,000, which
// puts every body larger than ten kilobytes in the overflow bucket.
var sizeBuckets = []float64{0, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864}

// panicError is the error a handler that panicked is recorded with. The response
// it was writing never completes — net/http aborts the connection — so it is
// recorded as a 500 whatever status, if any, the handler wrote before panicking.
type panicError struct {
	value any
}

// Error describes the panic value.
func (e panicError) Error() string {
	return fmt.Sprintf("handler panicked: %v", e.value)
}

// recorder is an http.ResponseWriter that remembers what the handler did with it.
type recorder interface {
	http.ResponseWriter

	// status returns the status code the handler wrote, or 200 when it wrote a
	// body without one, or 0 when it wrote nothing at all.
	status() int

	// written returns the number of body bytes that reached the client.
	written() int64
}

// responseWriter records the status code and body size of a response.
//
// It implements Unwrap so that http.ResponseController reaches the real writer,
// and Flush and ReadFrom so that a handler streaming a response — server-sent
// events above all, which the REST integration exists to keep alive — is not
// silently degraded by being wrapped.
type responseWriter struct {
	http.ResponseWriter

	statusCode   int
	bytesWritten int64
}

func (rw *responseWriter) status() int    { return rw.statusCode }
func (rw *responseWriter) written() int64 { return rw.bytesWritten }

// Unwrap returns the writer underneath, which is what http.ResponseController
// follows to find Flush, Hijack and the deadline setters.
func (rw *responseWriter) Unwrap() http.ResponseWriter { return rw.ResponseWriter }

// WriteHeader records the first final status code written. net/http ignores
// subsequent ones, so recording anything but the first would describe a response
// the client never saw. An informational 1xx — 103 Early Hints, say — is not the
// response, and is not recorded; 101 is, since nothing follows it.
func (rw *responseWriter) WriteHeader(status int) {
	if rw.statusCode == 0 && (status >= http.StatusOK || status == http.StatusSwitchingProtocols) {
		rw.statusCode = status
	}

	rw.ResponseWriter.WriteHeader(status)
}

// Write records the body bytes, and the implicit 200 that writing without a
// status code means.
func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}

	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)

	return n, err
}

// Flush forwards to the underlying writer when it can flush, so a streaming
// handler keeps streaming. A writer that cannot flush is left alone rather than
// erroring: this is instrumentation, and it has no business failing a response.
func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// ReadFrom forwards net/http's zero-copy body path when the underlying writer
// offers it, so that wrapping does not turn a sendfile into a buffer copy.
func (rw *responseWriter) ReadFrom(src io.Reader) (int64, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}

	if readerFrom, ok := rw.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(src)
		rw.bytesWritten += n

		return n, err
	}

	// io.Copy would find this type's own ReadFrom and recurse; writeOnly hides it.
	n, err := io.Copy(writeOnly{rw.ResponseWriter}, src)
	rw.bytesWritten += n

	return n, err
}

// writeOnly exposes nothing but Write, so that io.Copy cannot take a shortcut
// back into the method that called it.
type writeOnly struct {
	io.Writer
}

// hijackableResponseWriter is a responseWriter over a writer that can be
// hijacked.
//
// It is a second type rather than a Hijack method on responseWriter because
// hijacking is feature-detected: a WebSocket library asks whether the writer is
// an http.Hijacker, and a wrapper that always answers yes turns "this connection
// cannot be hijacked, use something else" into a failure at hijack time. Under
// HTTP/2 the real writer is not hijackable, and neither is this.
type hijackableResponseWriter struct {
	*responseWriter
	http.Hijacker
}

// Hijack hands the connection over and records it as switched protocols: a
// WebSocket library writes its 101 straight to the connection, which the recorder
// never sees.
func (rw *hijackableResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := rw.Hijacker.Hijack()
	if err == nil && rw.statusCode == 0 {
		rw.statusCode = http.StatusSwitchingProtocols
	}

	return conn, brw, err
}

// newRecorder wraps rw, preserving whether it can be hijacked.
func newRecorder(rw http.ResponseWriter) recorder {
	wrapped := &responseWriter{ResponseWriter: rw}
	if hijacker, ok := rw.(http.Hijacker); ok {
		return &hijackableResponseWriter{responseWriter: wrapped, Hijacker: hijacker}
	}

	return wrapped
}
