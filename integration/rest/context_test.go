package rest

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/uptrace/bunrouter"
)

func TestParamsFromContext(t *testing.T) {
	testcases := []struct {
		name       string
		registered string
		requested  string
		expected   map[string]string
		found      bool
	}{
		{
			name:       "no params",
			registered: "/hello",
			requested:  "/hello",
			expected:   map[string]string{},
			found:      false,
		},
		{
			name:       "with URL params",
			registered: "/users/:username",
			requested:  "/users/someone",
			expected: map[string]string{
				"username": "someone",
			},
			found: true,
		},
		{
			name:       "multiple URL params",
			registered: "/orgs/:org_id/users/:user_id",
			requested:  "/orgs/org_123/users/usr_456",
			expected: map[string]string{
				"org_id":  "org_123",
				"user_id": "usr_456",
			},
			found: true,
		},
		{
			name:       "wildcard param",
			registered: "/files/*path",
			requested:  "/files/a/b.txt",
			expected: map[string]string{
				"path": "a/b.txt",
			},
			found: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := newTestREST(t, Config{})

			var params map[string]string
			var found, called bool
			api.GET(tc.registered, func(rw http.ResponseWriter, req *http.Request) {
				called = true
				params, found = ParamsFromContext(req.Context())
			})

			serve(api.Handler(), http.MethodGet, tc.requested, nil)

			assert.True(t, called)
			assert.Equal(t, tc.expected, params)
			assert.Equal(t, tc.found, found)
		})
	}
}

func TestParamsFromContext_NoRouterContext(t *testing.T) {
	params, found := ParamsFromContext(t.Context())

	assert.Empty(t, params)
	assert.False(t, found)
}

func TestParamFromContext(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	var id, missing string
	var idFound, missingFound bool
	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {
		id, idFound = ParamFromContext(req.Context(), "id")
		missing, missingFound = ParamFromContext(req.Context(), "other")
	})

	serve(api.Handler(), http.MethodGet, "/things/42", nil)

	assert.Equal(t, "42", id)
	assert.True(t, idFound)
	assert.Empty(t, missing)
	assert.False(t, missingFound, "the route declares no such parameter")
}

func TestParamFromContext_NoRouterContext(t *testing.T) {
	value, found := ParamFromContext(t.Context(), "id")

	assert.Empty(t, value)
	assert.False(t, found)
}

// TestParamsFromContext_BareRouter covers a handler routed by the underlying router
// directly rather than by this integration, as a test of a handler often is: the
// parameters that router stored are read all the same.
func TestParamsFromContext_BareRouter(t *testing.T) {
	router := bunrouter.New().Compat()

	var params map[string]string
	var found, called bool
	router.GET("/users/:username", func(rw http.ResponseWriter, req *http.Request) {
		called = true
		params, found = ParamsFromContext(req.Context())
	})

	serve(router, http.MethodGet, "/users/someone", nil)

	assert.True(t, called)
	assert.Equal(t, map[string]string{"username": "someone"}, params)
	assert.True(t, found)

	value, declared := ParamFromContext(context.Background(), "username")
	assert.Empty(t, value)
	assert.False(t, declared)
}
