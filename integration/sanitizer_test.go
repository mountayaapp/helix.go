package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// errRedacted is what the sanitizer below records in place of the error of a
// request that failed.
var errRedacted = errors.New("request to api.tld failed")

// registerSanitizer registers, for the length of the test, a sanitizer that
// records errRedacted in place of a *url.Error and every other error as it is.
func registerSanitizer(t *testing.T) {
	t.Helper()

	previous := trace.SetErrorSanitizer(func(err error) error {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return errRedacted
		}

		return err
	})
	t.Cleanup(func() { trace.SetErrorSanitizer(previous) })
}

// exceptions returns the message of every exception span recorded.
func exceptions(span sdktrace.ReadOnlySpan) []string {
	var messages []string
	for _, event := range span.Events() {
		if event.Name != "exception" {
			continue
		}

		for _, attr := range event.Attributes {
			if attr.Key == "exception.message" {
				messages = append(messages, attr.Value.AsString())
			}
		}
	}

	return messages
}

// The error of a failed operation, which every integration records through its
// Operation, is recorded as the registered sanitizer returns it.
func TestOperation_RecordError_GoesThroughTheSanitizer(t *testing.T) {
	registerSanitizer(t)

	inst, recorder := newRecordingInstrument(t, InstrumentConfig{Identifier: "pages", Humanized: "Pages"})

	ctx, op := inst.StartOperation(t.Context(), trace.SpanKindClient, "Fetch")
	op.RecordError("failed to fetch", &url.Error{Op: "Get", URL: "https://api.tld/pages?token=secret-token-0123", Err: errors.New("connection reset by peer")})
	op.End(ctx)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, []string{errRedacted.Error()}, exceptions(spans[0]))
	assert.Equal(t, codes.Error, spans[0].Status().Code)
	assert.Equal(t, "failed to fetch", spans[0].Status().Description)
}

// The reason a readiness probe fails is recorded on the request's span, through
// the sanitizer too, and without setting the span's status as before.
func TestHTTPProbes_Readiness_RecordsTheFailureThroughTheSanitizer(t *testing.T) {
	registerSanitizer(t)

	server := newRecordingHTTPServer(t, HTTPServerOptions{})
	probes := HTTPProbes{CheckHealth: func(context.Context) error {
		return &url.Error{Op: "Get", URL: "https://api.tld/health?token=secret-token-0123", Err: errors.New("connection refused")}
	}}

	rw := httptest.NewRecorder()
	server.Middleware(http.HandlerFunc(probes.HandleReadiness)).ServeHTTP(rw, httptest.NewRequest(http.MethodGet, PathReadiness, nil))

	assert.Equal(t, http.StatusServiceUnavailable, rw.Code)

	spans := server.spans.Ended()
	require.Len(t, spans, 1)
	assert.Contains(t, exceptions(spans[0]), errRedacted.Error())
	for _, message := range exceptions(spans[0]) {
		assert.NotContains(t, message, "secret-token-0123")
	}
}
