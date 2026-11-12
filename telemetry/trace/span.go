package trace

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// SpanKind is the role a Span plays in a Trace. Type alias for OTEL's SpanKind.
type SpanKind = oteltrace.SpanKind

// The roles a Span can play. They are the OpenTelemetry kinds: a server or client
// span is one side of a remote call, a producer or consumer one side of an
// asynchronous one, and an internal span is neither.
const (
	SpanKindInternal = oteltrace.SpanKindInternal
	SpanKindServer   = oteltrace.SpanKindServer
	SpanKindClient   = oteltrace.SpanKindClient
	SpanKindProducer = oteltrace.SpanKindProducer
	SpanKindConsumer = oteltrace.SpanKindConsumer
)

// Span is the individual component of a Trace. It represents a single named and
// timed operation of a workflow that is traced. Always safe to call methods on —
// never nil.
type Span struct {
	span oteltrace.Span
}

// NewSpan wraps a raw OpenTelemetry Span. This bridges the internal tracer
// (which returns oteltrace.Span) with the public Span type.
func NewSpan(span oteltrace.Span) *Span {
	return &Span{span: span}
}

// SpanFromContext returns the Span the context carries: the innermost one started
// on it, which under a server integration is the span of the work in progress, or
// a child of it. It serves code that records on a span it did not start, such as
// the error behind a failed request.
//
//	trace.SpanFromContext(ctx).RecordError("failed to render the report", err)
//
// A context that carries no span yields a Span that records nothing, so the
// result is always safe to call methods on. The caller does not own the Span and
// must not End it.
func SpanFromContext(ctx context.Context) *Span {
	return NewSpan(oteltrace.SpanFromContext(ctx))
}

// SetAttributes sets one or more OTEL attributes on the span.
func (s *Span) SetAttributes(attrs ...attribute.KeyValue) {
	if s.span == nil {
		return
	}

	s.span.SetAttributes(attrs...)
}

// IsRecording reports whether the Span records what is set on it. A Span that is
// not recording discards everything, so instrumentation gates the construction of
// its attributes on this rather than building KeyValues nothing will read.
//
//	if span.IsRecording() {
//	  span.SetAttributes(expensiveAttrs()...)
//	}
func (s *Span) IsRecording() bool {
	if s.span == nil {
		return false
	}

	return s.span.IsRecording()
}

// RecordError records the error as an exception span event and sets the span
// status to error.
//
// The error recorded is the one the function registered with SetErrorSanitizer
// makes of err, when one is registered. An error it turns into nil is recorded as
// no exception, and the status is still set.
func (s *Span) RecordError(msg string, err error) {
	if s.span == nil || err == nil {
		return
	}

	if err = SanitizeError(err); err != nil {
		recordException(s.span, err)
	}

	s.span.SetStatus(codes.Error, msg)
}

// AddEvent adds a named event to the Span, with optional attributes describing
// it.
func (s *Span) AddEvent(name string, attrs ...attribute.KeyValue) {
	if s.span == nil {
		return
	}

	if len(attrs) == 0 {
		s.span.AddEvent(name)
		return
	}

	s.span.AddEvent(name, oteltrace.WithAttributes(attrs...))
}

// End completes the Span. The Span is considered complete and ready to be
// delivered through the rest of the telemetry pipeline after this method is
// called.
//
// The status is left as the instrumentation set it: RecordError marks a span
// codes.Error, and a span that saw no error stays Unset. The OpenTelemetry
// specification reserves codes.Ok for an explicit assertion by application code
// and directs instrumentation to leave the status alone, so that backends can
// classify on Unset together with the recorded response attributes.
func (s *Span) End() {
	if s.span == nil {
		return
	}

	s.span.End()
}
