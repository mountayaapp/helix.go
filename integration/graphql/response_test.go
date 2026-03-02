package graphql

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWriteSuccess(t *testing.T) {
	testcases := []struct {
		name     string
		status   int
		expected string
	}{
		{
			name:     "200 OK",
			status:   http.StatusOK,
			expected: `{"status":"OK"}`,
		},
		{
			name:     "201 Created",
			status:   http.StatusCreated,
			expected: `{"status":"Created"}`,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			rw := httptest.NewRecorder()

			writeSuccess(rw, tc.status)

			assert.Equal(t, tc.status, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.JSONEq(t, tc.expected, rw.Body.String())
		})
	}
}

func TestWriteError(t *testing.T) {
	testcases := []struct {
		name     string
		status   int
		expected string
	}{
		{
			name:     "404 Not Found",
			status:   http.StatusNotFound,
			expected: `{"status":"Not Found","error":{"message":"Resource does not exist"}}`,
		},
		{
			name:     "405 Method Not Allowed",
			status:   http.StatusMethodNotAllowed,
			expected: `{"status":"Method Not Allowed","error":{"message":"Resource does not support this method"}}`,
		},
		{
			name:     "500 Internal Server Error",
			status:   http.StatusInternalServerError,
			expected: `{"status":"Internal Server Error","error":{"message":"We have been notified of this unexpected internal error"}}`,
		},
		{
			name:     "503 Service Unavailable",
			status:   http.StatusServiceUnavailable,
			expected: `{"status":"Service Unavailable","error":{"message":"Please try again in a few moments"}}`,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rw := httptest.NewRecorder()

			writeError(rw, req, tc.status)

			assert.Equal(t, tc.status, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.JSONEq(t, tc.expected, rw.Body.String())
		})
	}
}

func TestWriteError_NilRequest(t *testing.T) {
	rw := httptest.NewRecorder()

	writeError(rw, nil, http.StatusNotFound)

	assert.Equal(t, http.StatusNotFound, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"status":"Not Found","error":{"message":"Resource does not exist"}}`, rw.Body.String())
}
