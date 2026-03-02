package graphql

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestMux() *graphql {
	g := &graphql{
		config: &Config{
			Path: "/graphql",
		},
	}

	h := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})

	g.mux = http.NewServeMux()
	g.mux.HandleFunc("GET /health", g.handlerHealthcheck)
	g.mux.Handle("POST "+g.config.Path, h)
	g.mux.Handle("OPTIONS "+g.config.Path, h)
	g.mux.HandleFunc(g.config.Path, g.handlerMethodNotAllowed)
	g.mux.HandleFunc("/", g.handlerNotFound)

	return g
}

func TestMux_PostGraphQL_ReturnsOK(t *testing.T) {
	g := newTestMux()

	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	rw := httptest.NewRecorder()
	g.mux.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusOK, rw.Code)
}

func TestMux_OptionsGraphQL_ReturnsOK(t *testing.T) {
	g := newTestMux()

	req := httptest.NewRequest(http.MethodOptions, "/graphql", nil)
	rw := httptest.NewRecorder()
	g.mux.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusOK, rw.Code)
}

func TestMux_NonAllowedMethodGraphQL_ReturnsMethodNotAllowed(t *testing.T) {
	methods := []string{
		http.MethodGet,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	}

	g := newTestMux()

	for _, method := range methods {
		t.Run(method+" /graphql", func(t *testing.T) {
			req := httptest.NewRequest(method, "/graphql", nil)
			rw := httptest.NewRecorder()
			g.mux.ServeHTTP(rw, req)

			assert.Equal(t, http.StatusMethodNotAllowed, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"status":"Method Not Allowed","error":{"message":"Resource does not support this method"}}`, rw.Body.String())
		})
	}
}

func TestMux_Healthcheck_WithCustomHealthcheck(t *testing.T) {
	g := &graphql{
		config: &Config{
			Path: "/graphql",
			Healthcheck: func(req *http.Request) int {
				return http.StatusServiceUnavailable
			},
		},
	}

	h := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})

	g.mux = http.NewServeMux()
	g.mux.HandleFunc("GET /health", g.handlerHealthcheck)
	g.mux.Handle("POST "+g.config.Path, h)
	g.mux.Handle("OPTIONS "+g.config.Path, h)
	g.mux.HandleFunc(g.config.Path, g.handlerMethodNotAllowed)
	g.mux.HandleFunc("/", g.handlerNotFound)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rw := httptest.NewRecorder()
	g.mux.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusServiceUnavailable, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"status":"Service Unavailable","error":{"message":"Please try again in a few moments"}}`, rw.Body.String())
}

func TestMux_UnknownRoute_ReturnsNotFound(t *testing.T) {
	routes := []string{
		"/",
		"/unknown",
		"/api/graphql",
	}

	g := newTestMux()

	for _, route := range routes {
		t.Run("GET "+route, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, route, nil)
			rw := httptest.NewRecorder()
			g.mux.ServeHTTP(rw, req)

			assert.Equal(t, http.StatusNotFound, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"status":"Not Found","error":{"message":"Resource does not exist"}}`, rw.Body.String())
		})
	}
}
