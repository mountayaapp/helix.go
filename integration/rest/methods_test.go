package rest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouteShape(t *testing.T) {
	testcases := []struct {
		template string
		shape    string
	}{
		{template: "/", shape: "/"},
		{template: "/things", shape: "/things"},
		{template: "/things/:id", shape: "/things/:"},
		{template: "/things/:name", shape: "/things/:"},
		{template: "/things/:id/parts/:part", shape: "/things/:/parts/:"},
		{template: "/files/*path", shape: "/files/"},
		{template: "/files/", shape: "/files/"},
	}

	for _, tc := range testcases {
		t.Run(tc.template, func(t *testing.T) {
			assert.Equal(t, tc.shape, routeShape(tc.template))
		})
	}
}

// A 405 names the methods the route takes, every template of the same route
// included, so a client can tell which method it should have sent.
func TestRouter_MethodNotAllowedListsTheAllowedMethods(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	handler := func(rw http.ResponseWriter, req *http.Request) {}
	api.GET("/things/:id", handler)
	api.POST("/things/:id", handler)
	api.DELETE("/things/:name", handler)
	api.Group("/v1").PATCH("/things/:id", handler)
	api.GET("/files/*path", handler)

	testcases := []struct {
		method string
		target string
		allow  string
	}{
		{method: http.MethodPut, target: "/things/42", allow: "GET, HEAD, POST, DELETE"},
		{method: http.MethodGet, target: "/v1/things/42", allow: "PATCH"},
		{method: http.MethodPost, target: "/files/a/b", allow: "GET, HEAD"},
		{method: http.MethodPost, target: "/health", allow: "GET, HEAD"},
	}

	for _, tc := range testcases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			rw := serve(api.Handler(), tc.method, tc.target, nil)

			assert.Equal(t, http.StatusMethodNotAllowed, rw.Code)
			assert.Equal(t, tc.allow, rw.Header().Get("Allow"))
			assert.JSONEq(t, `{"errors":[{"message":"Method is not allowed for this resource","extensions":{"code":"METHOD_NOT_ALLOWED"}}]}`, rw.Body.String())
		})
	}
}

// A GET route answers HEAD with its own handler, options included: net/http sends
// what the handler set and drops what it wrote.
func TestRouter_GETAnswersHEAD(t *testing.T) {
	api, rec := newTestREST(t, Config{})

	var methods []string
	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {
		methods = append(methods, req.Method)
		rw.Header().Set("X-Thing", "42")
		_, _ = rw.Write([]byte(`{"data":{"id":"42"}}`))
	}, WithPreValidation(requireAuthorization))

	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	head := func(authorized bool) (int, http.Header, []byte) {
		t.Helper()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodHead, server.URL+"/things/42", nil)
		require.NoError(t, err)
		if authorized {
			req.Header.Set("Authorization", "Bearer token")
		}

		res, err := server.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)

		return res.StatusCode, res.Header, body
	}

	status, header, body := head(true)

	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "42", header.Get("X-Thing"))
	assert.Empty(t, body)
	assert.Equal(t, []string{http.MethodHead}, methods)
	spanNamed(t, rec, "REST: HEAD /things/:id")

	status, _, _ = head(false)
	assert.Equal(t, http.StatusUnauthorized, status, "the HEAD runs under the GET's options")
}

func TestRouter_NoImplicitHead(t *testing.T) {
	api, _ := newTestREST(t, Config{NoImplicitHead: true})
	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {})

	rw := serve(api.Handler(), http.MethodHead, "/things/42", nil)

	assert.Equal(t, http.StatusMethodNotAllowed, rw.Code)
	assert.Equal(t, "GET", rw.Header().Get("Allow"))
	assert.Equal(t, "GET", serve(api.Handler(), http.MethodPost, "/health", nil).Header().Get("Allow"))
}

// A HEAD route registered before the GET of the same path is kept: the GET then
// implies none.
func TestRouter_HEADRegisteredBeforeGETIsKept(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	api.HEAD("/things/:id", func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
	})
	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {})

	assert.Equal(t, http.StatusNoContent, serve(api.Handler(), http.MethodHead, "/things/42", nil).Code)
	assert.Equal(t, http.StatusOK, serve(api.Handler(), http.MethodGet, "/things/42", nil).Code)
}

// A HEAD route registered after its GET would otherwise be refused by the router
// as a duplicate, without a word on where the first one came from.
func TestRouter_HEADRegisteredAfterGETPanics(t *testing.T) {
	api, _ := newTestREST(t, Config{})
	api.GET("/things/:id", func(rw http.ResponseWriter, req *http.Request) {})

	assert.PanicsWithValue(t, "rest: HEAD /things/:name is already answered by its GET route; register the HEAD route before the GET one, or set Config.NoImplicitHead", func() {
		api.HEAD("/things/:name", func(rw http.ResponseWriter, req *http.Request) {})
	})
}

// The probes take HEAD as every GET route does.
func TestRouter_ProbesAnswerHEAD(t *testing.T) {
	api, _ := newTestREST(t, Config{})

	assert.Equal(t, http.StatusOK, serve(api.Handler(), http.MethodHead, "/health", nil).Code)
	assert.Equal(t, http.StatusOK, serve(api.Handler(), http.MethodHead, "/ready", nil).Code)
}
