package trace

/*
This file is dedicated to status-setting wrappers.

By default, OpenTelemetry leaves span status as "Unset" unless explicitly set.
This is problematic because third-party libraries (otelhttp, Temporal SDK, etc.)
create and end raw OpenTelemetry spans without ever calling SetStatus(codes.Ok).

Instead of patching every integration individually, these wrappers intercept span
creation at the TracerProvider level. Every span produced through the provider
automatically gets codes.Ok on End() — unless codes.Error was already recorded.
This guarantees correct status across the entire stack with a single point of
control.
*/

import (
	"context"

	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

/*
statusTracerProvider wraps an oteltrace.TracerProvider so that every Tracer it
returns produces status-aware spans.
*/
type statusTracerProvider struct {
	oteltrace.TracerProvider
}

func (p *statusTracerProvider) Tracer(name string, opts ...oteltrace.TracerOption) oteltrace.Tracer {
	return &statusTracer{Tracer: p.TracerProvider.Tracer(name, opts...)}
}

/*
statusTracer wraps an oteltrace.Tracer so that every span it starts is a
statusSpan.
*/
type statusTracer struct {
	oteltrace.Tracer
}

func (t *statusTracer) Start(ctx context.Context, name string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	ctx, span := t.Tracer.Start(ctx, name, opts...)
	wrapped := &statusSpan{Span: span}

	return oteltrace.ContextWithSpan(ctx, wrapped), wrapped
}

/*
statusSpan wraps an oteltrace.Span and automatically sets codes.Ok when End is
called, unless codes.Error was previously recorded via SetStatus.
*/
type statusSpan struct {
	oteltrace.Span
	hasError bool
}

func (s *statusSpan) SetStatus(code codes.Code, msg string) {
	if code == codes.Error {
		s.hasError = true
	}

	s.Span.SetStatus(code, msg)
}

func (s *statusSpan) End(opts ...oteltrace.SpanEndOption) {
	if !s.hasError {
		s.Span.SetStatus(codes.Ok, "")
	}

	s.Span.End(opts...)
}
