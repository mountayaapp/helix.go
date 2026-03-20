package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/uptrace/bunrouter"
)

func newTestRouter() *rest {
	r := &rest{
		config: &Config{},
	}

	router := bunrouter.New(
		bunrouter.WithNotFoundHandler(r.handlerNotFound),
		bunrouter.WithMethodNotAllowedHandler(r.handlerMethodNotAllowed),
	).Compat()

	router.Router.GET("/health", r.handlerLiveness)
	router.Router.GET("/ready", r.handlerReadiness)

	r.bun = router

	return r
}

func TestRouter_Liveness_ReturnsOK(t *testing.T) {
	r := newTestRouter()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rw := httptest.NewRecorder()
	r.bun.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"status":"OK"}`, rw.Body.String())
}

func TestRouter_Readiness_CustomReady(t *testing.T) {
	r := newTestRouter()
	r.config.Readiness = func(req *http.Request) int {
		return http.StatusOK
	}

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rw := httptest.NewRecorder()
	r.bun.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"status":"OK"}`, rw.Body.String())
}

func TestRouter_Readiness_WithCustomReadiness(t *testing.T) {
	r := newTestRouter()
	r.config.Readiness = func(req *http.Request) int {
		return http.StatusServiceUnavailable
	}

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rw := httptest.NewRecorder()
	r.bun.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusServiceUnavailable, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"status":"Service Unavailable","error":{"message":"Please try again in a few moments"}}`, rw.Body.String())
}

func TestRouter_UnknownRoute_ReturnsNotFound(t *testing.T) {
	routes := []string{
		"/",
		"/unknown",
		"/api/test",
	}

	r := newTestRouter()

	for _, route := range routes {
		t.Run("GET "+route, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, route, nil)
			rw := httptest.NewRecorder()
			r.bun.ServeHTTP(rw, req)

			assert.Equal(t, http.StatusNotFound, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"status":"Not Found","error":{"message":"Resource does not exist"}}`, rw.Body.String())
		})
	}
}

func TestRouter_NonAllowedMethod_ReturnsMethodNotAllowed(t *testing.T) {
	methods := []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	}

	r := newTestRouter()

	for _, method := range methods {
		t.Run(method+" /health", func(t *testing.T) {
			req := httptest.NewRequest(method, "/health", nil)
			rw := httptest.NewRecorder()
			r.bun.ServeHTTP(rw, req)

			assert.Equal(t, http.StatusMethodNotAllowed, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"status":"Method Not Allowed","error":{"message":"Resource does not support this method"}}`, rw.Body.String())
		})
	}
}
