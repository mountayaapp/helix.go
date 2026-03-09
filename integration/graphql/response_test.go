package graphql

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			name:     "400 Bad Request",
			status:   http.StatusBadRequest,
			expected: `{"status":"Bad Request","error":{"message":"Failed to validate request"}}`,
		},
		{
			name:     "401 Unauthorized",
			status:   http.StatusUnauthorized,
			expected: `{"status":"Unauthorized","error":{"message":"You are not authorized to perform this action"}}`,
		},
		{
			name:     "403 Forbidden",
			status:   http.StatusForbidden,
			expected: `{"status":"Forbidden","error":{"message":"You don't have required permissions to perform this action"}}`,
		},
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
			name:     "409 Conflict",
			status:   http.StatusConflict,
			expected: `{"status":"Conflict","error":{"message":"Failed to process target resource because of conflict"}}`,
		},
		{
			name:     "429 Too Many Requests",
			status:   http.StatusTooManyRequests,
			expected: `{"status":"Too Many Requests","error":{"message":"Request-rate limit has been reached"}}`,
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

func TestWriteSuccess_SetsContentType(t *testing.T) {
	rw := httptest.NewRecorder()

	writeSuccess(rw, http.StatusOK)

	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
}

func TestWriteError_AllSupportedStatusCodes(t *testing.T) {
	codes := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusPaymentRequired,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusConflict,
		http.StatusRequestEntityTooLarge,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	}

	for _, code := range codes {
		t.Run(http.StatusText(code), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rw := httptest.NewRecorder()

			writeError(rw, req, code)

			assert.Equal(t, code, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.Contains(t, rw.Body.String(), `"status"`)
			assert.Contains(t, rw.Body.String(), `"error"`)
		})
	}
}

func TestFallbackErrorResponse_ValidJSON(t *testing.T) {
	var result map[string]any
	err := json.Unmarshal(fallbackErrorResponse, &result)

	require.NoError(t, err)
	assert.Equal(t, "Internal Server Error", result["status"])
	errObj := result["error"].(map[string]any)
	assert.NotEmpty(t, errObj["message"])
}

func TestWriteSuccess_StatusText(t *testing.T) {
	rw := httptest.NewRecorder()

	writeSuccess(rw, http.StatusNoContent)

	assert.Equal(t, http.StatusNoContent, rw.Code)
	assert.JSONEq(t, `{"status":"No Content"}`, rw.Body.String())
}
