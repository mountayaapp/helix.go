package rest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newQueryREST returns an API validated against testdata/openapi.query.yaml,
// refusing a query parameter an operation does not declare when refuse is set.
func newQueryREST(t *testing.T, refuse bool) *rest {
	t.Helper()

	api, _ := newTestREST(t, Config{
		OpenAPI: ConfigOpenAPI{
			Enabled:               true,
			Descriptions:          []string{"./testdata/openapi.query.yaml"},
			RefuseUndeclaredQuery: refuse,
		},
	})

	return api
}

// listPages answers GET /pages with an empty list, recording that it ran.
func listPages(called *int) http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		*called++
		NewResponseSuccess[NoMetadata, []map[string]string](req).
			SetData([]map[string]string{}).
			Write(rw)
	}
}

// refusedPaths returns the path of every entry a refusal carries, in order.
func refusedPaths(t *testing.T, body []byte) [][]any {
	t.Helper()

	var envelope struct {
		Errors []struct {
			Path       []any          `json:"path"`
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))

	paths := make([][]any, 0, len(envelope.Errors))
	for _, entry := range envelope.Errors {
		assert.Equal(t, "VALIDATION_FAILED", entry.Extensions["code"])
		paths = append(paths, entry.Path)
	}

	return paths
}

// Off, which is the default, a query parameter no operation declares is not looked
// at: the request reaches its handler as before.
func TestUndeclaredQuery_IsLeftAloneByDefault(t *testing.T) {
	api := newQueryREST(t, false)

	var called int
	api.GET("/pages", listPages(&called))

	rw := serve(api.Handler(), http.MethodGet, "/pages?limit=2&color=blue", nil)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, 1, called)
}

// On, each query parameter the operation does not declare is one entry at
// ["request", "query", name], in the order of the names, and the handler is not
// reached. What the operation declares passes: its own parameters, its path item's,
// and the name of the apiKey scheme carried in the query that its security names.
func TestUndeclaredQuery_RefusesWhatTheOperationDoesNotDeclare(t *testing.T) {
	api := newQueryREST(t, true)

	var called int
	api.GET("/pages", listPages(&called))
	api.GET("/pages/:id", func(rw http.ResponseWriter, req *http.Request) {
		called++
		NewResponseSuccess[NoMetadata, map[string]string](req).
			SetData(map[string]string{}).
			Write(rw)
	})

	t.Run("declared parameters pass", func(t *testing.T) {
		called = 0

		rw := serve(api.Handler(), http.MethodGet, "/pages?limit=2&locale=fr&key=secret", nil)

		assert.Equal(t, http.StatusOK, rw.Code)
		assert.Equal(t, 1, called)
	})

	t.Run("undeclared parameters are refused, each by name", func(t *testing.T) {
		called = 0

		rw := serve(api.Handler(), http.MethodGet, "/pages?zeta=1&limit=2&alpha=1&alpha=2", nil)

		require.Equal(t, http.StatusBadRequest, rw.Code)
		assert.Equal(t, [][]any{{"request", "query", "alpha"}, {"request", "query", "zeta"}}, refusedPaths(t, rw.Body.Bytes()))
		assert.Zero(t, called)
	})

	t.Run("a query credential is declared only where the operation's security names it", func(t *testing.T) {
		called = 0

		rw := serve(api.Handler(), http.MethodGet, "/pages/42?key=secret", nil)

		require.Equal(t, http.StatusBadRequest, rw.Code)
		assert.Equal(t, [][]any{{"request", "query", "key"}}, refusedPaths(t, rw.Body.Bytes()))
		assert.Zero(t, called)
	})

	t.Run("a HEAD is held to its GET's parameters", func(t *testing.T) {
		called = 0

		assert.Equal(t, http.StatusBadRequest, serve(api.Handler(), http.MethodHead, "/pages?color=blue", nil).Code)
		assert.Equal(t, http.StatusOK, serve(api.Handler(), http.MethodHead, "/pages?limit=2", nil).Code)
	})
}

// An undeclared parameter is refused in the same answer as every other failure of
// the request, so a caller fixing it learns about all of them at once.
func TestUndeclaredQuery_IsRefusedBesideTheOtherFailures(t *testing.T) {
	api := newQueryREST(t, true)

	var called int
	api.GET("/pages", listPages(&called))

	rw := serve(api.Handler(), http.MethodGet, "/pages?limit=99&color=blue", nil)

	require.Equal(t, http.StatusBadRequest, rw.Code)
	assert.Equal(t, [][]any{{"request", "query", "color"}, {"request", "query", "limit"}}, refusedPaths(t, rw.Body.Bytes()))
	assert.Zero(t, called)
}

// A route admits the names it is registered with, accumulated from its group and
// from itself, and only on that route.
func TestUndeclaredQuery_ARouteAdmitsTheNamesItIsRegisteredWith(t *testing.T) {
	api := newQueryREST(t, true)

	var called int
	api.Group("", WithUndeclaredQuery("token")).GET("/pages", listPages(&called), WithUndeclaredQuery("signature"))
	api.GET("/pages/:id", func(rw http.ResponseWriter, req *http.Request) {
		called++
	})

	rw := serve(api.Handler(), http.MethodGet, "/pages?token=t&signature=s", nil)
	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, 1, called)

	refused := serve(api.Handler(), http.MethodGet, "/pages/42?token=t", nil)
	require.Equal(t, http.StatusBadRequest, refused.Code)
	assert.Equal(t, [][]any{{"request", "query", "token"}}, refusedPaths(t, refused.Body.Bytes()))
}

// The check is the validation's, so a pre-validation middleware refuses first: a
// caller that may not call the route learns nothing of what it accepts.
func TestUndeclaredQuery_ComesAfterThePreValidation(t *testing.T) {
	api := newQueryREST(t, true)

	var called int
	api.GET("/pages", listPages(&called), WithPreValidation(requireAuthorization))

	rw := serve(api.Handler(), http.MethodGet, "/pages?color=blue", nil)

	assert.Equal(t, http.StatusUnauthorized, rw.Code)
	assert.Zero(t, called)
}

// A route no description declares is not validated, its query included.
func TestUndeclaredQuery_LeavesAnUndescribedRouteAlone(t *testing.T) {
	api := newQueryREST(t, true)

	var called int
	api.GET("/drafts", func(rw http.ResponseWriter, req *http.Request) {
		called++
	})

	rw := serve(api.Handler(), http.MethodGet, "/drafts?color=blue", nil)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, 1, called)
}
