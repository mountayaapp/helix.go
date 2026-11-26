package rest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mountaya/helix.go/event"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// newEdgeREST builds an API whose EdgeMiddleware stamps every response it wraps
// with an X-Edge header, and whose Middleware refuses a request without an
// Authorization header. It returns the API with the number of requests each
// middleware saw.
func newEdgeREST(t *testing.T, cfg Config) (*rest, *int, *int) {
	t.Helper()

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
			if req.Header.Get("Authorization") == "" {
				NewResponseError[NoMetadata](req).SetStatus(http.StatusUnauthorized).Write(rw)
				return
			}

			next.ServeHTTP(rw, req)
		})
	}

	api, _ := newTestREST(t, cfg)
	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {})
	api.POST("/things", func(rw http.ResponseWriter, req *http.Request) {})

	return api, &edge, &inner
}

// Every response the server gives but the probes' crosses EdgeMiddleware, the ones
// this package answers before Middleware runs included: a CORS header set there is
// on a redirect, a refused body and a forged request as it is on a route's answer.
func TestEdgeMiddleware_WrapsEveryResponse(t *testing.T) {
	api, edge, inner := newEdgeREST(t, Config{
		CrossOriginProtection: http.NewCrossOriginProtection(),
		MaxRequestBodyBytes:   8,
	})

	testcases := []struct {
		name          string
		method        string
		target        string
		body          string
		header        map[string]string
		status        int
		reachesInner  bool
		authenticated bool
	}{
		{name: "a non-canonical read", method: http.MethodGet, target: "/things/42/", status: http.StatusPermanentRedirect},
		{name: "a non-canonical write", method: http.MethodPost, target: "/things/", status: http.StatusNotFound},
		{name: "a body over the limit", method: http.MethodPost, target: "/things", body: "123456789", status: http.StatusRequestEntityTooLarge},
		{name: "a forged cross-origin write", method: http.MethodPost, target: "/things", header: map[string]string{"Sec-Fetch-Site": "cross-site"}, status: http.StatusForbidden},
		{name: "a refusal of Middleware", method: http.MethodGet, target: "/things/42", status: http.StatusUnauthorized, reachesInner: true},
		{name: "a route's answer", method: http.MethodGet, target: "/things/42", status: http.StatusOK, reachesInner: true, authenticated: true},
		{name: "an unknown path", method: http.MethodGet, target: "/nowhere", status: http.StatusNotFound, reachesInner: true, authenticated: true},
		{name: "a method the path does not take", method: http.MethodDelete, target: "/things/42", status: http.StatusMethodNotAllowed, reachesInner: true, authenticated: true},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			*edge, *inner = 0, 0

			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			for name, value := range tc.header {
				req.Header.Set(name, value)
			}
			if tc.authenticated {
				req.Header.Set("Authorization", "Bearer token")
			}

			rw := httptest.NewRecorder()
			api.Handler().ServeHTTP(rw, req)

			require.Equal(t, tc.status, rw.Code)
			assert.Equal(t, "1", rw.Header().Get("X-Edge"))
			assert.Equal(t, 1, *edge, "EdgeMiddleware runs once per request")
			assert.Equal(t, tc.reachesInner, *inner == 1, "Middleware runs only for a request it may be spent on")
		})
	}
}

// The probes answer without any of the application's middleware, the edge's
// included: a Kubernetes probe is not a client.
func TestEdgeMiddleware_ProbesBypassIt(t *testing.T) {
	api, edge, _ := newEdgeREST(t, Config{})

	for _, path := range []string{"/health", "/ready"} {
		rw := serve(api.Handler(), http.MethodGet, path, nil)

		assert.Equal(t, http.StatusOK, rw.Code)
		assert.Empty(t, rw.Header().Get("X-Edge"))
	}

	assert.Zero(t, *edge)
}

// EdgeMiddleware runs inside the request's entry span: the span is in the context
// it is handed, a response it writes itself is traced, and an Event it puts in the
// context is stamped on the span once the request reaches the router.
func TestEdgeMiddleware_RunsInsideTheEntrySpan(t *testing.T) {
	var spanned bool
	api, rec := newTestREST(t, Config{
		EdgeMiddleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				spanned = oteltrace.SpanContextFromContext(req.Context()).IsValid()

				if req.Method == http.MethodOptions {
					rw.WriteHeader(http.StatusNoContent)
					return
				}

				ctx := event.NewContext(req.Context(), event.Event{Name: "thing.read"})
				next.ServeHTTP(rw, req.WithContext(ctx))
			})
		},
	})

	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {})

	require.Equal(t, http.StatusNoContent, serve(api.Handler(), http.MethodOptions, "/things/42", nil).Code)
	assert.True(t, spanned, "the entry span is open when EdgeMiddleware runs")

	preflight := spanNamed(t, rec, "REST: OPTIONS")
	assert.Equal(t, int64(http.StatusNoContent), attributesOf(preflight)["rest.status_code"].AsInt64())

	require.Equal(t, http.StatusOK, serve(api.Handler(), http.MethodGet, "/things/42", nil).Code)

	attrs := attributesOf(spanNamed(t, rec, "REST: GET /things/:id"))
	assert.Equal(t, "thing.read", attrs["event.name"].AsString())
}
