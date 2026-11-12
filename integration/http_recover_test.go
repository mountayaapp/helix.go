package integration

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// recoveringHandler serves h the way a server integration does: under the entry
// span of server, and inside RecoverPanics.
func recoveringHandler(server recordingHTTPServer, h http.HandlerFunc) http.Handler {
	return server.Middleware(RecoverPanics(h, "Recovered from a panic while serving a request", nil))
}

// eventAttribute returns the attribute key of the event named event on span, and
// the empty string when there is none.
func eventAttribute(span sdktrace.ReadOnlySpan, event string, key string) string {
	for _, e := range span.Events() {
		if e.Name != event {
			continue
		}

		for _, attr := range e.Attributes {
			if string(attr.Key) == key {
				return attr.Value.AsString()
			}
		}
	}

	return ""
}

// hijackableRecorder is a ResponseRecorder over a connection that can be taken
// over, as an HTTP/1.1 one can.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
}

func (hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, nil
}

// A request whose handling panics is answered the fixed 500 envelope, which holds
// nothing of the panic, and carries the headers the response had before the
// handler ran and none it set. The panic goes to the request's span: its value as
// an exception, and the stack of the goroutine that panicked as an event.
func TestRecoverPanics_AnswersTheEnvelopeAndRecordsThePanic(t *testing.T) {
	server := newRecordingHTTPServer(t, HTTPServerOptions{})

	h := recoveringHandler(server, func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Cache-Control", "public, max-age=86400")
		rw.Header().Set("X-Trace-Ref", "overwritten")
		panic("lookup failed for token tok_0123456789abcdef")
	})

	edge := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("X-Trace-Ref", "ref-42")
		h.ServeHTTP(rw, req)
	})

	rw := httptest.NewRecorder()
	edge.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/things", nil))

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.JSONEq(t, `{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_ERROR"}}]}`, rw.Body.String())
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.Equal(t, "ref-42", rw.Header().Get("X-Trace-Ref"))
	assert.Empty(t, rw.Header().Get("Cache-Control"))

	spans := server.spans.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Error, spans[0].Status().Code)
	assert.Equal(t, "panic: lookup failed for token tok_0123456789abcdef", eventAttribute(spans[0], "exception", "exception.message"))
	assert.Contains(t, eventAttribute(spans[0], "panic", "exception.stacktrace"), "TestRecoverPanics_AnswersTheEnvelopeAndRecordsThePanic",
		"the stack is not the one of the goroutine that panicked")
}

// A panic once the response has started cannot be answered: it is recorded, and
// the response is aborted the way net/http knows not to log.
func TestRecoverPanics_AfterTheResponseStartedAborts(t *testing.T) {
	server := newRecordingHTTPServer(t, HTTPServerOptions{})

	h := recoveringHandler(server, func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("partial"))
		panic("stream bug")
	})

	rw := httptest.NewRecorder()
	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		h.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/things", nil))
	})

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "partial", rw.Body.String())

	spans := server.spans.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "panic: stream bug", eventAttribute(spans[0], "exception", "exception.message"))
}

// A handler's deliberate abort is not a panic to answer or to record.
func TestRecoverPanics_ErrAbortHandlerGoesOn(t *testing.T) {
	server := newRecordingHTTPServer(t, HTTPServerOptions{})

	h := recoveringHandler(server, func(rw http.ResponseWriter, req *http.Request) {
		panic(http.ErrAbortHandler)
	})

	rw := httptest.NewRecorder()
	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		h.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/things", nil))
	})

	assert.Empty(t, rw.Body.String())

	spans := server.spans.Ended()
	require.Len(t, spans, 1)
	assert.Empty(t, eventAttribute(spans[0], "panic", "exception.stacktrace"))
}

// The writer handed on keeps what the one it wraps can do: a streaming handler
// can still flush, and a hijackable connection is still hijackable.
func TestRecoverPanics_WriterKeepsFlushAndHijack(t *testing.T) {
	plain, _ := newPanicAwareWriter(httptest.NewRecorder())
	_, flushes := plain.(http.Flusher)
	_, hijacks := plain.(http.Hijacker)
	assert.True(t, flushes)
	assert.False(t, hijacks, "a writer that cannot be hijacked must not claim it can")

	hijackable, state := newPanicAwareWriter(hijackableRecorder{httptest.NewRecorder()})
	_, hijacks = hijackable.(http.Hijacker)
	assert.True(t, hijacks)
	assert.False(t, state.started)

	require.NoError(t, http.NewResponseController(hijackable).Flush())
	assert.True(t, state.started)
}

// A panic whose value is an error stays one: what recovered it can still be
// asked which error it was.
func TestRecoveredPanic_UnwrapsAnErrorValue(t *testing.T) {
	cause := errors.New("connection reset")

	assert.ErrorIs(t, recoveredPanic{value: cause}, cause)
	assert.Equal(t, "panic: connection reset", recoveredPanic{value: cause}.Error())
	assert.NoError(t, recoveredPanic{value: "not an error"}.Unwrap())
}
