package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCanonicalREST builds an API whose Middleware refuses every request it sees,
// with a route of each kind a non-canonical path can concern, and returns it with
// the number of times a handler ran.
func newCanonicalREST(t *testing.T) (*rest, *int) {
	t.Helper()

	api, _ := newTestREST(t, Config{
		Middleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				if req.Header.Get("Authorization") == "" {
					NewResponseError[NoMetadata](req).SetStatus(http.StatusUnauthorized).Write(rw)
					return
				}

				next.ServeHTTP(rw, req)
			})
		},
	})

	var calls int
	handler := func(rw http.ResponseWriter, req *http.Request) {
		calls++
		path, _ := ParamFromContext(req.Context(), "path")
		_, _ = rw.Write([]byte(path))
	}

	api.GET("/things/:id", handler)
	api.POST("/things", handler)
	api.GET("/files/*path", handler)
	api.GET("/folders/", handler)

	return api, &calls
}

// A path that names a route only once it is made canonical is answered before
// Middleware runs: a GET or a HEAD is redirected there with its method kept, and
// any other method is told the path it sent names nothing, rather than being
// redirected into a GET of the same path.
func TestCanonicalPaths_AreAnsweredBeforeMiddleware(t *testing.T) {
	api, calls := newCanonicalREST(t)

	testcases := []struct {
		name     string
		method   string
		target   string
		status   int
		location string
	}{
		{name: "trailing slash", method: http.MethodGet, target: "/things/42/", status: http.StatusPermanentRedirect, location: "/things/42"},
		{name: "trailing slash with a query", method: http.MethodGet, target: "/things/42/?page=2", status: http.StatusPermanentRedirect, location: "/things/42?page=2"},
		{name: "trailing slash on a HEAD", method: http.MethodHead, target: "/things/42/", status: http.StatusPermanentRedirect, location: "/things/42"},
		{name: "parent segment", method: http.MethodGet, target: "/things/42/../43", status: http.StatusPermanentRedirect, location: "/things/43"},
		{name: "dot segment", method: http.MethodGet, target: "/things/./42", status: http.StatusPermanentRedirect, location: "/things/42"},
		{name: "missing trailing slash", method: http.MethodGet, target: "/folders", status: http.StatusPermanentRedirect, location: "/folders/"},
		{name: "escaped segment", method: http.MethodGet, target: "/things/a%2Fb/", status: http.StatusPermanentRedirect, location: "/things/a%2Fb"},
		{name: "a write", method: http.MethodPost, target: "/things/", status: http.StatusNotFound},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			*calls = 0
			rw := serve(api.Handler(), tc.method, tc.target, nil)

			require.Equal(t, tc.status, rw.Code)
			assert.Equal(t, tc.location, rw.Header().Get("Location"))
			assert.Zero(t, *calls, "no handler may run")

			if tc.status == http.StatusNotFound {
				assert.JSONEq(t, `{"errors":[{"message":"Resource does not exist","extensions":{"code":"NOT_FOUND"}}]}`, rw.Body.String())
			}
		})
	}
}

// A path served as written, by a wildcard route above all, or that names no route
// even made canonical, goes through Middleware and the router as any other.
func TestCanonicalPaths_LeaveOtherPathsToTheRouter(t *testing.T) {
	api, calls := newCanonicalREST(t)

	testcases := []struct {
		name   string
		method string
		target string
		status int
		body   string
	}{
		{name: "a wildcard route's own path", method: http.MethodGet, target: "/files/a/", status: http.StatusOK, body: "a/"},
		{name: "a canonical path", method: http.MethodGet, target: "/things/42", status: http.StatusOK},
		{name: "a slash route's own path", method: http.MethodGet, target: "/folders/", status: http.StatusOK},
		{name: "no route at all", method: http.MethodGet, target: "/nowhere/", status: http.StatusNotFound},
		{name: "a method no route takes", method: http.MethodDelete, target: "/things/42/", status: http.StatusNotFound},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			*calls = 0

			anonymous := serve(api.Handler(), tc.method, tc.target, nil)
			assert.Equal(t, http.StatusUnauthorized, anonymous.Code, "Middleware must run")

			req := httptest.NewRequest(tc.method, tc.target, nil)
			req.Header.Set("Authorization", "Bearer token")
			rw := httptest.NewRecorder()
			api.Handler().ServeHTTP(rw, req)

			assert.Equal(t, tc.status, rw.Code)
			assert.Empty(t, rw.Header().Get("Location"))
			if tc.status == http.StatusOK {
				assert.Equal(t, tc.body, rw.Body.String())
				assert.Equal(t, 1, *calls, "the handler runs once, whether or not the router was asked first")
			}
		})
	}
}

// A redirected request is traced as one the router never matched.
func TestCanonicalPaths_AreTracedAsUnrouted(t *testing.T) {
	api, rec := newTestREST(t, Config{})
	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {})

	require.Equal(t, http.StatusPermanentRedirect, serve(api.Handler(), http.MethodGet, "/things/42/", nil).Code)

	span := spanNamed(t, rec, "REST: GET")
	assert.Equal(t, int64(http.StatusPermanentRedirect), attributesOf(span)["rest.status_code"].AsInt64())
}
