package trace

import (
	"sync"

	internalsanitize "github.com/mountayaapp/helix.go/internal/telemetry/sanitize"

	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// SetErrorSanitizer registers sanitize as the function every error goes through
// before telemetry writes it: before a Span records it, and before a log record
// holds it. The text and the type written are those of the error sanitize
// returns. It returns the function registered before, or nil.
//
// An error's text is written by whatever failed, and a driver's or a client's
// often quotes what it was handed: a statement with the values bound to it, the
// input a type refused, the query of a URL. Every integration records the error of
// a failed operation on its span, some log it too, and only the application knows
// which of those texts may leave it, so the rule is the application's to supply:
//
//	trace.SetErrorSanitizer(func(err error) error {
//		var urlErr *url.Error
//		if !errors.As(err, &urlErr) {
//			return err
//		}
//
//		return fmt.Errorf("%s %s: request failed", urlErr.Op, withoutQuery(urlErr.URL))
//	})
//
// It applies to every Span of the process, whoever records on it: the
// integrations, on the span of each operation, and the application's own calls to
// RecordError. It applies as well to every error a record of the Service's logger
// holds as an error, written to standard error and exported alike: one handed
// over with log.Err, or with log.Any when the value is an error, the integrations'
// own and those of the libraries they log for, such as the Temporal SDK's. A value
// that is no error is not handed to it, an error's text already formatted into a
// message or a string field among them. Register it once, at start, before any
// integration connects. With none registered, or nil, an error is written as it
// is.
//
// sanitize is handed the error as it was returned, with its chain, and is never
// handed nil. It returns the error to write in its place, itself to write it
// unchanged, or nil to write none: a span records no exception, its status being
// set to error all the same, and a log record holds no error field. It runs
// wherever an error is written, so it must be safe to call from several
// goroutines at once. One that panics is recovered, and a fixed error is written
// in place of the one it was sanitizing.
func SetErrorSanitizer(sanitize func(err error) error) func(err error) error {
	return internalsanitize.Set(sanitize)
}

// SanitizeError returns err as telemetry writes it: what the function registered
// with SetErrorSanitizer makes of it, and err itself when none is registered. A
// nil err stays nil.
//
// Span.RecordError and the Service's logger apply it already. It is exported for
// instrumentation that writes an error onto a span by another hand, an
// OpenTelemetry span's own RecordError or a status description, so that the same
// rule holds there.
func SanitizeError(err error) error {
	return internalsanitize.Error(err)
}

// SanitizingSpan returns span with the errors recorded on it held to the
// function registered with SetErrorSanitizer, for instrumentation that records an
// error on a span by its own hand rather than through a Span.
//
// Its RecordError records what SanitizeError makes of the error, and nothing
// when that is nil. Instrumentation often sets the span's status right after,
// with the error's own text as the description: a description that repeats the
// text of the error last recorded on the span is replaced by the text recorded in
// its place, or left empty when nothing was. Every other method is span's own,
// and with no function registered the span records exactly what span would.
//
// An integration whose spans are finished by code it does not own, such as an
// SDK's tracing interceptor, hands that code the span this returns. A Span built
// over it sanitizes an error once, not twice. A span that is already one is
// returned as it is.
func SanitizingSpan(span oteltrace.Span) oteltrace.Span {
	if _, ok := span.(*sanitizingSpan); ok || span == nil {
		return span
	}

	return &sanitizingSpan{Span: span}
}

// sanitizingSpan is the span SanitizingSpan returns.
type sanitizingSpan struct {
	oteltrace.Span

	// mu guards the texts below, since a span may be recorded on from several
	// goroutines at once.
	mu sync.Mutex

	// handed is the text of the error last recorded on the span, as it was handed
	// over, and recorded the text recorded in its place, empty when nothing was.
	// handed is empty when that error went through no sanitizer.
	handed   string
	recorded string
}

// RecordError records what SanitizeError makes of err, and remembers its text
// beside err's own for the status that may follow (SetStatus).
func (s *sanitizingSpan) RecordError(err error, options ...oteltrace.EventOption) {
	if err == nil || !internalsanitize.Registered() {
		s.remember("", "")
		s.Span.RecordError(err, options...)

		return
	}

	sanitized := SanitizeError(err)

	recorded := ""
	if sanitized != nil {
		recorded = errorText(sanitized)
		s.Span.RecordError(sanitized, options...)
	}

	s.remember(errorText(err), recorded)
}

// SetStatus sets the span's status, with a description repeating the text of the
// error last recorded replaced by the text recorded in its place.
func (s *sanitizingSpan) SetStatus(code codes.Code, description string) {
	if code == codes.Error {
		s.mu.Lock()
		if s.handed != "" && description == s.handed {
			description = s.recorded
		}
		s.mu.Unlock()
	}

	s.Span.SetStatus(code, description)
}

// remember keeps the texts of the error last recorded, for SetStatus.
func (s *sanitizingSpan) remember(handed, recorded string) {
	s.mu.Lock()
	s.handed, s.recorded = handed, recorded
	s.mu.Unlock()
}

// errorText returns err's text, and the empty string when its Error method
// panics: recording an error never panics for the error's sake.
func errorText(err error) (text string) {
	defer func() {
		if recover() != nil {
			text = ""
		}
	}()

	return err.Error()
}

// recordException records err, already sanitized, as an exception on span: on
// the span underneath, when span is a sanitizing one, so that err does not go
// through the sanitizer a second time.
func recordException(span oteltrace.Span, err error) {
	if sanitizing, ok := span.(*sanitizingSpan); ok {
		span = sanitizing.Span
	}

	span.RecordError(err)
}
