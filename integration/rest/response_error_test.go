package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResponseError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseError[NoMetadata](req)

	assert.NotNil(t, res)
}

func TestResponseError_SetStatus(t *testing.T) {
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
			res := NewResponseError[NoMetadata](req).
				SetStatus(tc.status)

			b, err := json.Marshal(res)

			require.NoError(t, err)
			assert.JSONEq(t, tc.expected, string(b))
		})
	}
}

func TestResponseError_SetMetadata(t *testing.T) {
	type metadata struct {
		RequestId string `json:"request_id"`
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseError[metadata](req).
		SetStatus(http.StatusBadRequest).
		SetMetadata(metadata{RequestId: "abc-123"})

	b, err := json.Marshal(res)

	require.NoError(t, err)

	var result map[string]any
	json.Unmarshal(b, &result)
	meta := result["metadata"].(map[string]any)
	assert.Equal(t, "abc-123", meta["request_id"])
}

func TestResponseError_SetErrorValidations(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseError[NoMetadata](req).
		SetStatus(http.StatusBadRequest).
		SetErrorValidations([]errorstack.Validation{
			{
				Message: "Email is required",
				Path:    []string{"body", "email"},
			},
			{
				Message: "Name must not be empty",
				Path:    []string{"body", "name"},
			},
		})

	b, err := json.Marshal(res)

	require.NoError(t, err)

	var result map[string]any
	json.Unmarshal(b, &result)
	errObj := result["error"].(map[string]any)
	validations := errObj["validations"].([]any)
	assert.Len(t, validations, 2)
}

func TestResponseError_Write(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseError[NoMetadata](req).
		SetStatus(http.StatusNotFound).
		Write(rw)

	assert.Equal(t, http.StatusNotFound, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"status":"Not Found","error":{"message":"Resource does not exist"}}`, rw.Body.String())
}

func TestResponseError_Write_InternalServerError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseError[NoMetadata](req).
		SetStatus(http.StatusInternalServerError).
		Write(rw)

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"status":"Internal Server Error","error":{"message":"We have been notified of this unexpected internal error"}}`, rw.Body.String())
}
