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

func TestNewResponseError_DefaultsToInternalServerError(t *testing.T) {
	// A caller that forgets SetStatus must still produce a valid HTTP error
	// response — never a status-0 response with an error envelope.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseError[NoMetadata](req).Write(rw)

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.JSONEq(t,
		`{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_ERROR"}}]}`,
		rw.Body.String(),
	)
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
		RequestID string `json:"request_id"`
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseError[metadata](req).
		SetStatus(http.StatusBadRequest).
		SetMetadata(metadata{RequestID: "abc-123"})

	b, err := json.Marshal(res)

	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(b, &result))
	extensions := result["extensions"].(map[string]any)
	meta := extensions["metadata"].(map[string]any)
	assert.Equal(t, "abc-123", meta["request_id"])
}

func TestResponseError_SetValidations(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseError[NoMetadata](req).
		SetStatus(http.StatusBadRequest).
		SetValidations(
			errorstack.Entry{
				Message: "Must be a valid email address",
				Path:    []any{"request", "body", "email"},
			},
			errorstack.Entry{
				Message: "Must be set",
				Path:    []any{"request", "body", "name"},
			},
		)

	b, err := json.Marshal(res)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(b, &result))
	errs := result["errors"].([]any)
	require.Len(t, errs, 2, "validations supersede the seeded fallback entry")

	for _, e := range errs {
		entry := e.(map[string]any)
		ext := entry["extensions"].(map[string]any)
		assert.Equal(t, errorstack.CodeValidationFailed, ext["code"])
	}
}

func TestResponseError_Write(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseError[NoMetadata](req).
		SetStatus(http.StatusNotFound).
		Write(rw)

	assert.Equal(t, http.StatusNotFound, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t,
		`{"errors":[{"message":"Resource does not exist","extensions":{"code":"NOT_FOUND"}}]}`,
		rw.Body.String(),
	)
}

func TestResponseError_Write_InternalServerError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseError[NoMetadata](req).
		SetStatus(http.StatusInternalServerError).
		Write(rw)

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.JSONEq(t,
		`{"errors":[{"message":"Internal server error","extensions":{"code":"INTERNAL_ERROR"}}]}`,
		rw.Body.String(),
	)
}

func TestResponseError_ChainedCalls(t *testing.T) {
	type metadata struct {
		TraceID string `json:"trace_id"`
	}

	req := httptest.NewRequest(http.MethodPost, "/users", nil)
	res := NewResponseError[metadata](req).
		SetStatus(http.StatusBadRequest).
		SetMetadata(metadata{TraceID: "trace_123"}).
		SetValidations(
			errorstack.Entry{
				Message: "Must be set",
				Path:    []any{"request", "body", "email"},
			},
		)

	b, err := json.Marshal(res)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(b, &result))

	errs := result["errors"].([]any)
	require.Len(t, errs, 1)

	extensions := result["extensions"].(map[string]any)
	meta := extensions["metadata"].(map[string]any)
	assert.Equal(t, "trace_123", meta["trace_id"])
}

func TestResponseError_MarshalJSON_NormalizesAppendedEntryWithoutCode(t *testing.T) {
	// Schema invariant: every errors[] entry must carry extensions.code (per
	// Error.yaml). ResponseError.MarshalJSON delegates to errorstack so the
	// invariant holds even when callers append a bare Entry that bypasses the
	// public constructors.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseError[NoMetadata](req).
		SetStatus(http.StatusBadRequest)
	res.err.Append(errorstack.Entry{Message: "Manually appended without code"})

	b, err := json.Marshal(res)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(b, &result))

	errs := result["errors"].([]any)
	require.Len(t, errs, 2)
	appended := errs[1].(map[string]any)
	ext := appended["extensions"].(map[string]any)
	assert.Equal(t, errorstack.CodeInternalError, ext["code"], "appended entries without a code default to INTERNAL_ERROR")
}

func TestResponseError_SetValidations_Empty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := NewResponseError[NoMetadata](req).
		SetStatus(http.StatusBadRequest).
		SetValidations()

	b, err := json.Marshal(res)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(b, &result))

	errs := result["errors"].([]any)
	require.Len(t, errs, 1, "empty SetValidations falls back to a single internal error entry per errorstack guarantee")
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

func TestResponseError_Write_SetsHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	NewResponseError[NoMetadata](req).
		SetStatus(http.StatusBadRequest).
		Write(rw)

	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusBadRequest, rw.Code)
}

