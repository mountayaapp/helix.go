package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mountaya/helix.go/event"
	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// newEdgeMCP builds a server protected by OAuth whose EdgeMiddleware stamps every
// response it wraps with an X-Edge header, and whose Middleware refuses a request
// carrying X-Refuse with 429. It returns the server with the number of requests
// each middleware saw.
func newEdgeMCP(t *testing.T) (*mcp, *int, *int) {
	t.Helper()

	cfg := oauthConfig(func(_ context.Context, _ string) (*TokenInfo, error) {
		return tokenFor("user-1"), nil
	})

	var edge, inner int
	cfg.EdgeMiddleware = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			edge++
			rw.Header().Set("X-Edge", "1")
			next.ServeHTTP(rw, req)
		})
	}

	cfg.Middleware = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			inner++
			if req.Header.Get("X-Refuse") != "" {
				rw.WriteHeader(http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(rw, req)
		})
	}

	return newTestMCP(t, cfg), &edge, &inner
}

// attributeValue returns the value of the attribute key among attrs, and the
// zero value when it is not there.
func attributeValue(attrs []attribute.KeyValue, key attribute.Key) attribute.Value {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value
		}
	}

	return attribute.Value{}
}

// Every response the server gives crosses EdgeMiddleware, the probes' and the
// protected resource metadata's included, which Middleware never sees: a request
// identifier set there is on every one of them.
func TestEdgeMiddleware_WrapsEveryResponse(t *testing.T) {
	m, edge, inner := newEdgeMCP(t)

	refused := bearer("token")
	refused.Header.Set("X-Refuse", "1")

	testcases := []struct {
		name         string
		req          *http.Request
		status       int
		reachesInner bool
	}{
		{name: "the liveness probe", req: httptest.NewRequest(http.MethodGet, "/health", nil), status: http.StatusOK},
		{name: "the readiness probe", req: httptest.NewRequest(http.MethodGet, "/ready", nil), status: http.StatusOK},
		{name: "the protected resource metadata", req: httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil), status: http.StatusOK},
		{name: "the transport's answer", req: bearer("token"), status: http.StatusOK, reachesInner: true},
		{name: "a request without a token", req: newPost("/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), status: http.StatusUnauthorized, reachesInner: true},
		{name: "a refusal of Middleware", req: refused, status: http.StatusTooManyRequests, reachesInner: true},
		{name: "an unknown path", req: httptest.NewRequest(http.MethodGet, "/nowhere", nil), status: http.StatusNotFound, reachesInner: true},
		{name: "a method the transport does not take", req: httptest.NewRequest(http.MethodPut, "/mcp", nil), status: http.StatusMethodNotAllowed, reachesInner: true},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			*edge, *inner = 0, 0

			rw := httptest.NewRecorder()
			m.handler.ServeHTTP(rw, tc.req)

			require.Equal(t, tc.status, rw.Code, rw.Body.String())
			assert.Equal(t, "1", rw.Header().Get("X-Edge"))
			assert.Equal(t, 1, *edge, "EdgeMiddleware runs once per request")
			assert.Equal(t, tc.reachesInner, *inner == 1, "the probes and the metadata bypass Middleware")
		})
	}
}

// EdgeMiddleware runs inside the request's entry span: the span is in the context
// it is handed, a response it writes itself is traced, and an Event it puts in the
// context is stamped on the span once the request reaches the routes.
func TestEdgeMiddleware_RunsInsideTheEntrySpan(t *testing.T) {
	_, rec := servicetest.New(t)

	var spanned bool
	cfg := toyConfig()
	cfg.EdgeMiddleware = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			spanned = oteltrace.SpanContextFromContext(req.Context()).IsValid()

			if req.Method == http.MethodOptions {
				rw.WriteHeader(http.StatusNoContent)
				return
			}

			ctx := event.NewContext(req.Context(), event.Event{Name: "probe.read"})
			next.ServeHTTP(rw, req.WithContext(ctx))
		})
	}

	m := newTestMCP(t, cfg)

	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, httptest.NewRequest(http.MethodOptions, "/mcp", nil))
	require.Equal(t, http.StatusNoContent, rw.Code)
	assert.True(t, spanned, "the entry span is open when EdgeMiddleware runs")

	preflight := findSpan(t, rec.Spans(), "MCP: OPTIONS")
	assert.Equal(t, int64(http.StatusNoContent), attributeValue(preflight.Attributes(), "mcp.status_code").AsInt64())

	rw = httptest.NewRecorder()
	m.handler.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, rw.Code)

	probe := findSpan(t, rec.Spans(), "MCP: GET")
	assert.Equal(t, "probe.read", attributeValue(probe.Attributes(), "event.name").AsString())
}
