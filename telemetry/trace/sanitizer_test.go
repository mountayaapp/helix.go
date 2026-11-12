package trace

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// withoutQuery is a sanitizer an application could register: the error of a
// request that failed keeps the URL it names without its query string, and every
// other error is recorded as it is.
func withoutQuery(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}

	u, parseErr := url.Parse(urlErr.URL)
	if parseErr != nil {
		return errors.New("request failed")
	}

	u.RawQuery = ""

	return fmt.Errorf("%s %s: request failed", urlErr.Op, u)
}

// registerSanitizer registers sanitize for the length of the test.
func registerSanitizer(t *testing.T, sanitize func(error) error) {
	t.Helper()

	previous := SetErrorSanitizer(sanitize)
	t.Cleanup(func() { SetErrorSanitizer(previous) })
}

// recordOn records err on a span of its own, under the status message "failed to
// fetch the page", and returns the span once ended.
func recordOn(t *testing.T, err error) sdktrace.ReadOnlySpan {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	_, raw := provider.Tracer("test").Start(t.Context(), "fetch")
	span := NewSpan(raw)
	span.RecordError("failed to fetch the page", err)
	span.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)

	return ended[0]
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

// failedRequest is the error of a request whose URL carries a token in its
// query, as an HTTP client returns it.
func failedRequest() error {
	return &url.Error{Op: "Get", URL: "https://api.tld/pages?token=secret-token-0123", Err: errors.New("connection reset by peer")}
}

func TestSpan_RecordError_WithoutASanitizer(t *testing.T) {
	registerSanitizer(t, nil)

	span := recordOn(t, failedRequest())

	assert.Equal(t, []string{failedRequest().Error()}, exceptionMessages(span), "an error is recorded as it is when nothing is registered")
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, "failed to fetch the page", span.Status().Description)
}

func TestSpan_RecordError_RecordsWhatTheSanitizerReturns(t *testing.T) {
	registerSanitizer(t, withoutQuery)

	t.Run("an error the sanitizer restates", func(t *testing.T) {
		span := recordOn(t, fmt.Errorf("refresh the cache: %w", failedRequest()))

		assert.Equal(t, []string{"Get https://api.tld/pages: request failed"}, exceptionMessages(span))
		assert.Equal(t, codes.Error, span.Status().Code)
		assert.Equal(t, "failed to fetch the page", span.Status().Description)
	})

	t.Run("an error it leaves alone", func(t *testing.T) {
		span := recordOn(t, errors.New("page not rendered"))

		assert.Equal(t, []string{"page not rendered"}, exceptionMessages(span))
	})
}

// An error the sanitizer turns into nil is recorded as no exception, and the span
// is still the span of a failure.
func TestSpan_RecordError_ASanitizerMayRecordNoException(t *testing.T) {
	registerSanitizer(t, func(error) error { return nil })

	span := recordOn(t, failedRequest())

	assert.Empty(t, exceptionMessages(span))
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, "failed to fetch the page", span.Status().Description)
}

// A sanitizer that panics costs the span the error's text and nothing else: a
// fixed error is recorded, never the error it had not finished with, and
// recording an error does not panic.
func TestSpan_RecordError_ASanitizerThatPanicsRecordsAFixedError(t *testing.T) {
	registerSanitizer(t, func(error) error { panic("sanitizer bug") })

	var span sdktrace.ReadOnlySpan
	require.NotPanics(t, func() { span = recordOn(t, failedRequest()) })

	assert.Equal(t, []string{"the error sanitizer panicked"}, exceptionMessages(span))
	assert.Equal(t, codes.Error, span.Status().Code)
}

func TestSetErrorSanitizer(t *testing.T) {
	registerSanitizer(t, nil)

	assert.Nil(t, SetErrorSanitizer(withoutQuery), "nothing was registered before")

	previous := SetErrorSanitizer(nil)
	require.NotNil(t, previous, "the function registered before is returned")
	assert.Equal(t, "Get https://api.tld/pages: request failed", previous(failedRequest()).Error())

	assert.Nil(t, SetErrorSanitizer(nil), "registering nil leaves nothing registered")
	assert.Equal(t, failedRequest().Error(), SanitizeError(failedRequest()).Error())
}

func TestSanitizeError(t *testing.T) {
	var calls int
	registerSanitizer(t, func(err error) error {
		calls++
		return withoutQuery(err)
	})

	assert.NoError(t, SanitizeError(nil))
	assert.Zero(t, calls, "the sanitizer is never handed nil")

	assert.Equal(t, "Get https://api.tld/pages: request failed", SanitizeError(failedRequest()).Error())
	assert.Equal(t, 1, calls)
}

// finishLikeAnInterceptor records err on span and ends it the way a tracing
// interceptor that does not know Span does: the error as an exception, then the
// status, with the error's own text as its description. It returns the span once
// ended.
func finishLikeAnInterceptor(t *testing.T, err error) sdktrace.ReadOnlySpan {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	_, raw := provider.Tracer("test").Start(t.Context(), "Run Activity")
	span := SanitizingSpan(raw)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	span.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)

	return ended[0]
}

// With nothing registered, a sanitizing span records exactly what the span it
// wraps would: the error and the status as they were handed over.
func TestSanitizingSpan_WithoutASanitizer(t *testing.T) {
	registerSanitizer(t, nil)

	span := finishLikeAnInterceptor(t, failedRequest())

	assert.Equal(t, []string{failedRequest().Error()}, exceptionMessages(span))
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Equal(t, failedRequest().Error(), span.Status().Description)
}

func TestSanitizingSpan_RecordsWhatTheSanitizerReturns(t *testing.T) {
	registerSanitizer(t, withoutQuery)

	t.Run("the exception and the description repeating it", func(t *testing.T) {
		span := finishLikeAnInterceptor(t, failedRequest())

		assert.Equal(t, []string{"Get https://api.tld/pages: request failed"}, exceptionMessages(span))
		assert.Equal(t, codes.Error, span.Status().Code)
		assert.Equal(t, "Get https://api.tld/pages: request failed", span.Status().Description)
	})

	t.Run("an error it leaves alone", func(t *testing.T) {
		span := finishLikeAnInterceptor(t, errors.New("page not rendered"))

		assert.Equal(t, []string{"page not rendered"}, exceptionMessages(span))
		assert.Equal(t, "page not rendered", span.Status().Description)
	})

	t.Run("a description of its own", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

		_, raw := provider.Tracer("test").Start(t.Context(), "Run Activity")
		span := SanitizingSpan(raw)
		span.RecordError(failedRequest())
		span.SetStatus(codes.Error, "failed to fetch the page")
		span.End()

		ended := recorder.Ended()
		require.Len(t, ended, 1)
		assert.Equal(t, "failed to fetch the page", ended[0].Status().Description)
	})
}

// An error the sanitizer turns into nil is recorded as no exception, and the
// description that would have repeated it is left empty: the span is still the
// span of a failure.
func TestSanitizingSpan_ASanitizerMayRecordNoException(t *testing.T) {
	registerSanitizer(t, func(error) error { return nil })

	span := finishLikeAnInterceptor(t, failedRequest())

	assert.Empty(t, exceptionMessages(span))
	assert.Equal(t, codes.Error, span.Status().Code)
	assert.Empty(t, span.Status().Description)
}

// unreadableError is an error whose text cannot be read: its Error method panics.
type unreadableError struct{}

// Error panics.
func (unreadableError) Error() string {
	panic("unreadable")
}

// An error whose text cannot be read costs the span that text and nothing else:
// recording it does not panic, and what the sanitizer made of it is recorded.
func TestSanitizingSpan_AnUnreadableErrorDoesNotPanic(t *testing.T) {
	registerSanitizer(t, func(err error) error {
		_ = err.Error()
		return err
	})

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	_, raw := provider.Tracer("test").Start(t.Context(), "Run Activity")
	span := SanitizingSpan(raw)
	require.NotPanics(t, func() { span.RecordError(unreadableError{}) })
	span.SetStatus(codes.Error, "failed to run the activity")
	span.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	assert.Equal(t, []string{"the error sanitizer panicked"}, exceptionMessages(ended[0]))
	assert.Equal(t, "failed to run the activity", ended[0].Status().Description)
}

// A Span built over a sanitizing span hands the sanitizer an error once, as it
// was returned, and records once what the sanitizer made of it.
func TestSanitizingSpan_ASpanOverItSanitizesOnce(t *testing.T) {
	var handed []error
	registerSanitizer(t, func(err error) error {
		handed = append(handed, err)
		return withoutQuery(err)
	})

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	_, raw := provider.Tracer("test").Start(t.Context(), "Run Activity")
	span := NewSpan(SanitizingSpan(raw))
	span.RecordError("failed to fetch the page", failedRequest())
	span.End()

	require.Len(t, handed, 1, "the sanitizer is handed the error once")
	assert.Equal(t, failedRequest().Error(), handed[0].Error(), "and as it was returned")

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	assert.Equal(t, []string{"Get https://api.tld/pages: request failed"}, exceptionMessages(ended[0]))
	assert.Equal(t, "failed to fetch the page", ended[0].Status().Description)
}

func TestSanitizingSpan_Wraps(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	_, raw := provider.Tracer("test").Start(t.Context(), "Run Activity")

	span := SanitizingSpan(raw)
	assert.Same(t, span, SanitizingSpan(span), "a sanitizing span is not wrapped twice")
	assert.Equal(t, raw.SpanContext(), span.SpanContext(), "it is the span it wraps in everything but its errors")
	assert.Nil(t, SanitizingSpan(nil))
}
