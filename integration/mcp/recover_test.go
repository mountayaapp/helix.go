package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
)

// internalErrorBody is the envelope a panic is answered with.
const internalErrorBody = `{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_ERROR"}}]}`

// panicValue is what the middlewares below panic with: a sentence quoting a
// value shaped like a credential, as code holding one may.
const panicValue = "exchange failed for token tok_0123456789abcdef"

// panickingConfig returns the toy server's Config with an EdgeMiddleware stamping
// every answer with X-Trace-Ref, and a Middleware that runs during, then hands
// the request on.
func panickingConfig(during func(rw http.ResponseWriter, req *http.Request)) Config {
	cfg := toyConfig()

	cfg.EdgeMiddleware = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			rw.Header().Set("X-Trace-Ref", "ref-42")
			next.ServeHTTP(rw, req)
		})
	}

	cfg.Middleware = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			during(rw, req)
			next.ServeHTTP(rw, req)
		})
	}

	return cfg
}

// A panic on the request's goroutine, in the application's Middleware, is
// answered with the 500 envelope this integration's other HTTP-layer errors use,
// which holds nothing of the panic, carries what EdgeMiddleware set and none of
// the headers set further in. net/http is handed no panic: it would close the
// connection without an answer and print the panic to standard error. The panic
// goes to the request's span, its value and the stack of its goroutine.
func TestRecover_PanicInMiddlewareIsA500CarryingTheEdgeHeaders(t *testing.T) {
	_, rec := servicetest.New(t)

	m := newTestMCP(t, panickingConfig(func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Cache-Control", "public, max-age=86400")
		panic(panicValue)
	}))

	rw := httptest.NewRecorder()
	require.NotPanics(t, func() {
		m.handler.ServeHTTP(rw, newPost("/mcp", initializeBody))
	})

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.JSONEq(t, internalErrorBody, rw.Body.String())
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.Equal(t, "ref-42", rw.Header().Get("X-Trace-Ref"))
	assert.Empty(t, rw.Header().Get("Cache-Control"))

	span := findSpan(t, rec.Spans(), "MCP: POST")
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, int64(http.StatusInternalServerError), attributeValue(span.Attributes(), "mcp.status_code").AsInt64())

	// The entry span records the 500 as an exception of its own beside the panic.
	var exceptions, stacks []string
	for _, event := range span.Events() {
		for _, attr := range event.Attributes {
			switch event.Name + "/" + string(attr.Key) {
			case "exception/exception.message":
				exceptions = append(exceptions, attr.Value.AsString())
			case "panic/exception.stacktrace":
				stacks = append(stacks, attr.Value.AsString())
			}
		}
	}

	assert.Contains(t, exceptions, "panic: "+panicValue)
	require.Len(t, stacks, 1)
	assert.Contains(t, stacks[0], "TestRecover_PanicInMiddlewareIsA500CarryingTheEdgeHeaders",
		"the stack is not the one of the goroutine that panicked")
}

// The probes and the protected resource metadata never reach Middleware, and are
// inside the recovery all the same: a readiness check that panics is a 500, not a
// dropped connection.
func TestRecover_PanicInAProbeIsA500(t *testing.T) {
	cfg := toyConfig()
	cfg.Readiness = func(*http.Request) int {
		panic(panicValue)
	}

	m := newTestMCP(t, cfg)

	rw := httptest.NewRecorder()
	require.NotPanics(t, func() {
		m.handler.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/ready", nil))
	})

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.JSONEq(t, internalErrorBody, rw.Body.String())
}

// A panic once the response has started cannot be answered: the response is
// aborted the way net/http knows not to log, and what was sent stays as it was.
func TestRecover_PanicAfterTheResponseStartedAborts(t *testing.T) {
	m := newTestMCP(t, panickingConfig(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("event: message\n"))
		panic(panicValue)
	}))

	rw := httptest.NewRecorder()
	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		m.handler.ServeHTTP(rw, newPost("/mcp", initializeBody))
	})

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "event: message\n", rw.Body.String())
}

// A handler's deliberate abort goes on to net/http unchanged.
func TestRecover_ErrAbortHandlerGoesOn(t *testing.T) {
	m := newTestMCP(t, panickingConfig(func(rw http.ResponseWriter, req *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	rw := httptest.NewRecorder()
	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		m.handler.ServeHTTP(rw, newPost("/mcp", initializeBody))
	})

	assert.Empty(t, rw.Body.String())
}

// A panic in EdgeMiddleware itself is outside the recovery, as it is in the
// other HTTP server integrations: it is the one thing every answer crosses.
func TestRecover_PanicInEdgeMiddlewareIsNotAnswered(t *testing.T) {
	cfg := toyConfig()
	cfg.EdgeMiddleware = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			panic("edge bug")
		})
	}

	m := newTestMCP(t, cfg)

	assert.PanicsWithValue(t, "edge bug", func() {
		m.handler.ServeHTTP(httptest.NewRecorder(), newPost("/mcp", initializeBody))
	})
}

// A request nothing panics on is answered as it was.
func TestRecover_LeavesAnAnsweredRequestAlone(t *testing.T) {
	m := newTestMCP(t, panickingConfig(func(http.ResponseWriter, *http.Request) {}))

	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, newPost("/mcp", initializeBody))

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Contains(t, rw.Body.String(), `"protocolVersion"`)
	assert.Equal(t, "ref-42", rw.Header().Get("X-Trace-Ref"))
}
