package integration

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mountaya/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteSuccess(t *testing.T) {
	// Every 2xx HTTP-layer success uses the same envelope ({"data":null}) so the
	// wire shape stays identical across transports. The status code carries the
	// OK/Created/etc. distinction.
	for _, status := range []int{http.StatusOK, http.StatusCreated} {
		rw := httptest.NewRecorder()

		WriteSuccess(rw, status)

		assert.Equal(t, status, rw.Code)
		assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"data":null}`, rw.Body.String())
	}
}

func TestWriteError(t *testing.T) {
	testcases := map[string]struct {
		status int
		code   string
	}{
		"404": {status: http.StatusNotFound, code: errorstack.CodeNotFound},
		"405": {status: http.StatusMethodNotAllowed, code: errorstack.CodeMethodNotAllowed},
		"503": {status: http.StatusServiceUnavailable, code: errorstack.CodeServiceUnavailable},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			rw := httptest.NewRecorder()

			WriteError(rw, httptest.NewRequest(http.MethodGet, "/", nil), tc.status)

			assert.Equal(t, tc.status, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))

			var body struct {
				Errors []errorstack.Entry `json:"errors"`
			}
			require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &body))
			require.Len(t, body.Errors, 1)
			assert.NotEmpty(t, body.Errors[0].Message)
			assert.Equal(t, tc.code, body.Errors[0].Extensions["code"])
		})
	}
}

func TestHTTPProbes_Liveness(t *testing.T) {
	rw := httptest.NewRecorder()

	// Liveness never consults health: restarting the container does not fix a
	// database that is down.
	HTTPProbes{CheckHealth: func(context.Context) error { return errors.New("down") }}.
		HandleLiveness(rw, httptest.NewRequest(http.MethodGet, PathLiveness, nil))

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.JSONEq(t, `{"data":null}`, rw.Body.String())
}

func TestHTTPProbes_Readiness(t *testing.T) {
	unhealthy := errorstack.New("Integration is not in a healthy state",
		errorstack.WithCode(errorstack.CodeServiceUnavailable),
		errorstack.WithPath("dependency", "primary"),
		errorstack.WithExtension("reason", "Dial tcp 10.0.0.12:5432: connection refused"),
	)

	testcases := map[string]struct {
		probes HTTPProbes
		status int
		body   string
	}{
		"no health check reports ready": {
			probes: HTTPProbes{},
			status: http.StatusOK,
		},
		"healthy": {
			probes: HTTPProbes{CheckHealth: func(context.Context) error { return nil }},
			status: http.StatusOK,
		},
		"unhealthy says nothing about why": {
			probes: HTTPProbes{CheckHealth: func(context.Context) error { return unhealthy }},
			status: http.StatusServiceUnavailable,
			body:   `{"errors":[{"message":"Service is temporarily unavailable","extensions":{"code":"SERVICE_UNAVAILABLE"}}]}`,
		},
		"custom readiness wins": {
			probes: HTTPProbes{
				CheckHealth: func(context.Context) error { return unhealthy },
				Readiness:   func(*http.Request) int { return http.StatusOK },
			},
			status: http.StatusOK,
		},
		"custom readiness may refuse": {
			probes: HTTPProbes{Readiness: func(*http.Request) int { return http.StatusServiceUnavailable }},
			status: http.StatusServiceUnavailable,
		},
		"custom readiness returning no status does not panic": {
			probes: HTTPProbes{Readiness: func(*http.Request) int { return 0 }},
			status: http.StatusInternalServerError,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			rw := httptest.NewRecorder()

			tc.probes.HandleReadiness(rw, httptest.NewRequest(http.MethodGet, PathReadiness, nil))

			assert.Equal(t, tc.status, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			if tc.body != "" {
				assert.JSONEq(t, tc.body, rw.Body.String())
			}
		})
	}
}
