package temporal

import (
	"errors"
	"strings"
	"testing"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.temporal.io/sdk/interceptor"
)

// errChargeRefused is the error the operations below fail with: a sentence quoting
// a value shaped like a credential, as a driver's or a client's may.
var errChargeRefused = errors.New("charge refused for card tok_0123456789abcdef")

// withoutCard is a sanitizer an application could register: an error quoting a
// card token is recorded as a fixed sentence, and every other error as it is.
func withoutCard(err error) error {
	if strings.Contains(err.Error(), "tok_") {
		return errors.New("charge refused")
	}

	return err
}

// registerSanitizer registers sanitize for the length of the test.
func registerSanitizer(t *testing.T, sanitize func(error) error) {
	t.Helper()

	previous := trace.SetErrorSanitizer(sanitize)
	t.Cleanup(func() { trace.SetErrorSanitizer(previous) })
}

// exceptionMessages returns the message of every exception span recorded.
func exceptionMessages(span sdktrace.ReadOnlySpan) []string {
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

// finishWith starts a span of an activity's run through the tracer dial builds
// and has the SDK's own tracing code finish it with err, as it finishes every span
// of a failed operation. It returns the span as recorded.
func finishWith(t *testing.T, err error) sdktrace.ReadOnlySpan {
	t.Helper()

	tracer, rec := newTestTracer(t)

	span, startErr := tracer.StartSpan(&interceptor.TracerStartSpanOptions{
		Operation: "RunActivity",
		Name:      "charge-user",
	})
	require.NoError(t, startErr)
	span.Finish(&interceptor.TracerFinishSpanOptions{Error: err})

	spans := rec.Spans()
	require.NotEmpty(t, spans)

	return spans[len(spans)-1]
}

// TestCustomTracer_AFailedOperationGoesThroughTheSanitizer pins what the span of
// a failed workflow, activity or client call holds of its error. The SDK's
// tracing code finishes that span by its own hand: it records the error as an
// exception and sets the status with the error's text as its description. Both go
// through the sanitizer the application registered, as on the span of every other
// integration, and with none registered they are what the SDK records.
func TestCustomTracer_AFailedOperationGoesThroughTheSanitizer(t *testing.T) {
	t.Run("with nothing registered, as the SDK records it", func(t *testing.T) {
		registerSanitizer(t, nil)

		span := finishWith(t, errChargeRefused)

		assert.Equal(t, []string{errChargeRefused.Error()}, exceptionMessages(span))
		assert.Equal(t, codes.Error, span.Status().Code)
		assert.Equal(t, errChargeRefused.Error(), span.Status().Description)
	})

	t.Run("with a sanitizer, the exception and the status hold what it returns", func(t *testing.T) {
		registerSanitizer(t, withoutCard)

		span := finishWith(t, errChargeRefused)

		assert.Equal(t, []string{"charge refused"}, exceptionMessages(span))
		assert.Equal(t, codes.Error, span.Status().Code)
		assert.Equal(t, "charge refused", span.Status().Description)
	})

	t.Run("an operation that succeeds records nothing", func(t *testing.T) {
		registerSanitizer(t, withoutCard)

		span := finishWith(t, nil)

		assert.Empty(t, exceptionMessages(span))
		assert.Equal(t, codes.Unset, span.Status().Code)
	})
}

// TestActivityPanic_GoesThroughTheSanitizerOnce pins that an activity that
// panics, run under the span the worker's tracing opens, has each error it
// writes through the sanitizer once, as it was raised, and never what the
// sanitizer already made of it: the panic, logged and recorded on the span by the
// worker's interceptors, then the fixed failure the activity ends with, recorded
// by the SDK's tracing code when it finishes the span. The span records each
// once, restated.
func TestActivityPanic_GoesThroughTheSanitizerOnce(t *testing.T) {
	const restated = "restated: "

	var handed []string
	registerSanitizer(t, func(err error) error {
		handed = append(handed, err.Error())
		return errors.New(restated + err.Error())
	})

	tracer, rec := newTestTracer(t)

	span, err := tracer.StartSpan(&interceptor.TracerStartSpanOptions{
		Operation: "RunActivity",
		Name:      "charge-user",
	})
	require.NoError(t, err)

	failed := executePanickingActivity(t, tracer.ContextWithSpan(t.Context(), span), WorkerInterceptors())
	span.Finish(&interceptor.TracerFinishSpanOptions{Error: failed})

	// The panic twice, for the log line and for the span, and the failure once.
	assert.Equal(t, []string{"panic: " + panicValue, "panic: " + panicValue, failed.Error()}, handed)

	spans := rec.Spans()
	require.NotEmpty(t, spans)
	assert.Equal(t, []string{restated + "panic: " + panicValue, restated + failed.Error()}, exceptionMessages(spans[len(spans)-1]))
}
