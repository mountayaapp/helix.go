package graphql

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteSuccess(t *testing.T) {
	// All 2xx HTTP-layer success responses use the same envelope as REST
	// ({"data":null}) so the wire shape stays consistent across transports.
	// The HTTP status code carries the OK/Created/etc. distinction.
	testcases := []struct {
		name   string
		status int
	}{
		{name: "200 OK", status: http.StatusOK},
		{name: "201 Created", status: http.StatusCreated},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			rw := httptest.NewRecorder()

			writeSuccess(rw, tc.status)

			assert.Equal(t, tc.status, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"data":null}`, rw.Body.String())
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
			expected: `{"errors":[{"message":"Request is invalid","extensions":{"code":"BAD_REQUEST"}}]}`,
		},
		{
			name:     "401 Unauthorized",
			status:   http.StatusUnauthorized,
			expected: `{"errors":[{"message":"Authentication is required","extensions":{"code":"UNAUTHORIZED"}}]}`,
		},
		{
			name:     "402 Payment Required",
			status:   http.StatusPaymentRequired,
			expected: `{"errors":[{"message":"Payment is required","extensions":{"code":"PAYMENT_REQUIRED"}}]}`,
		},
		{
			name:     "403 Forbidden",
			status:   http.StatusForbidden,
			expected: `{"errors":[{"message":"Access is forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
		},
		{
			name:     "404 Not Found",
			status:   http.StatusNotFound,
			expected: `{"errors":[{"message":"Resource does not exist","extensions":{"code":"NOT_FOUND"}}]}`,
		},
		{
			name:     "405 Method Not Allowed",
			status:   http.StatusMethodNotAllowed,
			expected: `{"errors":[{"message":"Method is not allowed for this resource","extensions":{"code":"METHOD_NOT_ALLOWED"}}]}`,
		},
		{
			name:     "409 Conflict",
			status:   http.StatusConflict,
			expected: `{"errors":[{"message":"Resource conflicts with current state","extensions":{"code":"CONFLICT"}}]}`,
		},
		{
			name:     "413 Payload Too Large",
			status:   http.StatusRequestEntityTooLarge,
			expected: `{"errors":[{"message":"Payload exceeds size limit","extensions":{"code":"PAYLOAD_TOO_LARGE"}}]}`,
		},
		{
			name:     "429 Too Many Requests",
			status:   http.StatusTooManyRequests,
			expected: `{"errors":[{"message":"Rate limit has been exceeded","extensions":{"code":"TOO_MANY_REQUESTS"}}]}`,
		},
		{
			name:     "500 Internal Server Error",
			status:   http.StatusInternalServerError,
			expected: `{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_ERROR"}}]}`,
		},
		{
			name:     "501 Not Implemented",
			status:   http.StatusNotImplemented,
			expected: `{"errors":[{"message":"Endpoint is not implemented","extensions":{"code":"NOT_IMPLEMENTED"}}]}`,
		},
		{
			name:     "502 Bad Gateway",
			status:   http.StatusBadGateway,
			expected: `{"errors":[{"message":"Upstream gateway is unavailable","extensions":{"code":"BAD_GATEWAY"}}]}`,
		},
		{
			name:     "503 Service Unavailable",
			status:   http.StatusServiceUnavailable,
			expected: `{"errors":[{"message":"Service is temporarily unavailable","extensions":{"code":"SERVICE_UNAVAILABLE"}}]}`,
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
	assert.JSONEq(t,
		`{"errors":[{"message":"Resource does not exist","extensions":{"code":"NOT_FOUND"}}]}`,
		rw.Body.String(),
	)
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
		http.StatusNotImplemented,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	}

	for _, code := range codes {
		t.Run(http.StatusText(code), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rw := httptest.NewRecorder()

			writeError(rw, req, code)

			assert.Equal(t, code, rw.Code)
			assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
			assert.Contains(t, rw.Body.String(), `"errors"`)
			assert.Contains(t, rw.Body.String(), `"code"`)
		})
	}
}

func TestFallbackErrorResponse_ValidJSON(t *testing.T) {
	var result map[string]any
	err := json.Unmarshal(fallbackErrorResponse, &result)

	require.NoError(t, err)
	errs := result["errors"].([]any)
	require.Len(t, errs, 1)
	entry := errs[0].(map[string]any)
	assert.NotEmpty(t, entry["message"])
	ext := entry["extensions"].(map[string]any)
	assert.Equal(t, errorstack.CodeInternalError, ext["code"])
}

func TestWriteSuccess_StatusText(t *testing.T) {
	rw := httptest.NewRecorder()

	writeSuccess(rw, http.StatusNoContent)

	assert.Equal(t, http.StatusNoContent, rw.Code)
	assert.JSONEq(t, `{"data":null}`, rw.Body.String())
}
