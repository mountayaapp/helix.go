package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mountaya/helix.go/service/servicetest"
	"github.com/mountaya/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/codes"
)

// The error a token validator failed with is recorded on the request's span as
// the registered sanitizer returns it, and the span's status stays what the
// answer makes it: a 401 leaves it unset.
func TestMCP_OAuth_ValidateTokenErrorGoesThroughTheSanitizer(t *testing.T) {
	redacted := errors.New("request to idp.internal failed")

	previous := trace.SetErrorSanitizer(func(err error) error {
		if _, ok := errors.AsType[*url.Error](err); ok {
			return redacted
		}

		return err
	})
	t.Cleanup(func() { trace.SetErrorSanitizer(previous) })

	_, rec := servicetest.New(t)
	m := newTestMCP(t, oauthConfig(func(_ context.Context, _ string) (*TokenInfo, error) {
		return nil, &url.Error{Op: "Get", URL: "https://idp.internal/introspect?token=secret-token-0123", Err: errors.New("connection refused")}
	}))

	rw := httptest.NewRecorder()
	m.handler.ServeHTTP(rw, bearer("any-token"))

	assert.Equal(t, http.StatusUnauthorized, rw.Code)

	span := findSpan(t, rec.Spans(), "MCP: POST")
	assert.Equal(t, codes.Unset, span.Status().Code)

	var messages []string
	for _, event := range span.Events() {
		for _, attr := range event.Attributes {
			if attr.Key == "exception.message" {
				messages = append(messages, attr.Value.AsString())
			}
		}
	}

	assert.Equal(t, []string{redacted.Error()}, messages)
}
