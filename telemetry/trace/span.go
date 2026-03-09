package trace

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

/*
SpanKind is the role a Span plays in a Trace. Type alias for OTEL's SpanKind.
*/
type SpanKind = oteltrace.SpanKind

const (
	SpanKindInternal = oteltrace.SpanKindInternal
	SpanKindServer   = oteltrace.SpanKindServer
	SpanKindClient   = oteltrace.SpanKindClient
	SpanKindProducer = oteltrace.SpanKindProducer
	SpanKindConsumer = oteltrace.SpanKindConsumer
)

/*
Span is the individual component of a Trace. It represents a single named and
timed operation of a workflow that is traced. Always safe to call methods on —
never nil.
*/
type Span struct {
	span     oteltrace.Span
	hasError bool
}

/*
NewSpan wraps a raw OpenTelemetry Span. This bridges the internal tracer
(which returns oteltrace.Span) with the public Span type.
*/
func NewSpan(span oteltrace.Span) *Span {
	return &Span{span: span}
}

/*
SetAttributes sets one or more OTEL attributes on the span.
*/
func (s *Span) SetAttributes(attrs ...attribute.KeyValue) {
	if s.span == nil {
		return
	}

	s.span.SetAttributes(attrs...)
}

/*
RecordError records the error as an exception span event and sets the span
status to error.
*/
func (s *Span) RecordError(msg string, err error) {
	if s.span == nil || err == nil {
		return
	}

	s.hasError = true
	s.span.RecordError(err)
	s.span.SetStatus(codes.Error, msg)
}

/*
AddEvent adds a named event to the Span.
*/
func (s *Span) AddEvent(name string) {
	if s.span == nil {
		return
	}

	s.span.AddEvent(name)
}

/*
End sets the appropriate status and completes the Span. The Span is considered
complete and ready to be delivered through the rest of the telemetry pipeline
after this method is called.

The underlying span already auto-sets codes.Ok via the statusSpan wrapper in
the internal tracer. This explicit SetStatus call is defense-in-depth: it
ensures correct status even if the span was created outside the wrapped
TracerProvider.
*/
func (s *Span) End() {
	if s.span == nil {
		return
	}

	if !s.hasError {
		s.span.SetStatus(codes.Ok, "")
	}

	s.span.End()
}
