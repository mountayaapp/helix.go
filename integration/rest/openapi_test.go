package rest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsValidUrl(t *testing.T) {
	testcases := []struct {
		name  string
		input string
		valid bool
	}{
		{
			name:  "valid HTTPS URL",
			input: "https://example.com/openapi.yaml",
			valid: true,
		},
		{
			name:  "valid HTTP localhost URL",
			input: "http://localhost:8080/openapi.yaml",
			valid: true,
		},
		{
			name:  "valid HTTPS URL with path segments",
			input: "https://api.example.com/v1/openapi.json",
			valid: true,
		},
		{
			name:  "relative path",
			input: "./descriptions/openapi.yaml",
			valid: false,
		},
		{
			name:  "absolute path",
			input: "/absolute/path/openapi.yaml",
			valid: false,
		},
		{
			name:  "filename only",
			input: "openapi.yaml",
			valid: false,
		},
		{
			name:  "empty string",
			input: "",
			valid: false,
		},
		{
			name:  "not a URL",
			input: "not a url at all",
			valid: false,
		},
		{
			name:  "valid FTP URL",
			input: "ftp://files.example.com/openapi.yaml",
			valid: true,
		},
		{
			name:  "scheme only",
			input: "https://",
			valid: false,
		},
		{
			name:  "URL with query params",
			input: "https://example.com/openapi.yaml?version=3",
			valid: true,
		},
		{
			name:  "URL with fragment",
			input: "https://example.com/openapi.yaml#section",
			valid: true,
		},
		{
			name:  "URL with port",
			input: "https://example.com:443/openapi.yaml",
			valid: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			u, ok := isValidUrl(tc.input)

			assert.Equal(t, tc.valid, ok)
			if tc.valid {
				assert.NotNil(t, u)
			} else {
				assert.Nil(t, u)
			}
		})
	}
}

// newTestResponseWriter builds a responseWriter wrapping rec, mirroring how
// middlewareValidation constructs it.
func newTestResponseWriter(rec http.ResponseWriter) *responseWriter {
	return &responseWriter{
		status:         200,
		ResponseWriter: rec,
		buf:            &bytes.Buffer{},
	}
}

func TestResponseWriter_DetectStreaming(t *testing.T) {
	testcases := []struct {
		name        string
		contentType string
		streaming   bool
	}{
		{name: "event stream", contentType: "text/event-stream", streaming: true},
		{name: "event stream with charset", contentType: "text/event-stream; charset=utf-8", streaming: true},
		{name: "json", contentType: "application/json", streaming: false},
		{name: "plain text", contentType: "text/plain", streaming: false},
		{name: "unset", contentType: "", streaming: false},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			rw := newTestResponseWriter(httptest.NewRecorder())
			if tc.contentType != "" {
				rw.Header().Set("Content-Type", tc.contentType)
			}

			rw.detectStreaming()
			assert.Equal(t, tc.streaming, rw.streaming)
		})
	}
}

func TestResponseWriter_Unwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newTestResponseWriter(rec)

	// Unwrap must expose the wrapped writer so http.ResponseController can reach it.
	assert.Same(t, rec, rw.Unwrap())
}

func TestResponseWriter_WriteBuffersNonStreaming(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newTestResponseWriter(rec)
	rw.Header().Set("Content-Type", "application/json")

	payload := `{"data":null}`
	n, err := rw.Write([]byte(payload))

	require.NoError(t, err)
	assert.Equal(t, len(payload), n)
	assert.False(t, rw.streaming)

	// A non-streamed body is mirrored to both the client and the validation buffer.
	assert.Equal(t, payload, rec.Body.String())
	assert.Equal(t, payload, rw.buf.String())
}

func TestResponseWriter_WriteBypassesBufferWhenStreaming(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newTestResponseWriter(rec)
	rw.Header().Set("Content-Type", "text/event-stream")

	event := "data: hello\n\n"
	n, err := rw.Write([]byte(event))

	require.NoError(t, err)
	assert.Equal(t, len(event), n)
	assert.True(t, rw.streaming)

	// A streamed body goes straight to the client and is never buffered, so it is
	// not validated against the finite OpenAPI schema.
	assert.Equal(t, event, rec.Body.String())
	assert.Equal(t, 0, rw.buf.Len())
}

func TestResponseWriter_WriteHeaderDetectsStreaming(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newTestResponseWriter(rec)
	rw.Header().Set("Content-Type", "text/event-stream")

	rw.WriteHeader(http.StatusOK)

	assert.True(t, rw.streaming)
	assert.Equal(t, http.StatusOK, rw.status)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestResponseWriter_StreamingSupportsFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := newTestResponseWriter(rec)
	rw.Header().Set("Content-Type", "text/event-stream")

	_, _ = rw.Write([]byte("data: tick\n\n"))

	// Unwrap lets http.ResponseController reach the underlying Flusher, so an SSE
	// handler can flush each event as it is produced.
	err := http.NewResponseController(rw).Flush()

	require.NoError(t, err)
	assert.True(t, rec.Flushed)
}
