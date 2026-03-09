package trace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newInMemoryTracer creates a Tracer backed by an in-memory exporter so tests
// can inspect completed spans.
func newInMemoryTracer(t *testing.T) (*Tracer, *tracetest.InMemoryExporter) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)

	wrapped := &statusTracerProvider{TracerProvider: provider}

	return &Tracer{
		provider:    wrapped,
		tracer:      wrapped.Tracer("github.com/mountayaapp/helix.go"),
		sdkProvider: provider,
	}, exporter
}

func TestStatusSpan_SetsOkOnEnd(t *testing.T) {
	tr, exporter := newInMemoryTracer(t)

	ctx := ContextWithTracer(t.Context(), tr)
	_, span := tr.Start(ctx, SpanKindInternal, "success-op")
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Ok, spans[0].Status.Code)
}

func TestStatusSpan_PreservesErrorStatus(t *testing.T) {
	tr, exporter := newInMemoryTracer(t)

	ctx := ContextWithTracer(t.Context(), tr)
	_, span := tr.Start(ctx, SpanKindInternal, "error-op")
	span.SetStatus(codes.Error, "something went wrong")
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Error, spans[0].Status.Code)
	assert.Equal(t, "something went wrong", spans[0].Status.Description)
}

func TestStatusSpan_ErrorThenEnd_DoesNotOverrideWithOk(t *testing.T) {
	tr, exporter := newInMemoryTracer(t)

	ctx := ContextWithTracer(t.Context(), tr)
	_, span := tr.Start(ctx, SpanKindInternal, "error-then-end")
	span.SetStatus(codes.Error, "failed")
	span.SetStatus(codes.Error, "failed again")
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Error, spans[0].Status.Code)
}

func TestStatusSpan_ProviderSpans_HaveAutoStatus(t *testing.T) {
	tr, exporter := newInMemoryTracer(t)

	// Simulate what otelhttp or Temporal would do: get a tracer from the
	// provider and create spans directly.
	tracer := tr.Provider().Tracer("third-party-lib")
	_, span := tracer.Start(t.Context(), "http-request")
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Ok, spans[0].Status.Code)
}

func TestStatusSpan_ProviderSpans_PreserveError(t *testing.T) {
	tr, exporter := newInMemoryTracer(t)

	tracer := tr.Provider().Tracer("third-party-lib")
	_, span := tracer.Start(t.Context(), "failed-request")
	span.SetStatus(codes.Error, "500 Internal Server Error")
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Error, spans[0].Status.Code)
	assert.Equal(t, "500 Internal Server Error", spans[0].Status.Description)
}

func TestStatusSpan_ChildSpansInheritBehavior(t *testing.T) {
	tr, exporter := newInMemoryTracer(t)

	tracer := tr.Provider().Tracer("test")
	ctx, parent := tracer.Start(t.Context(), "parent")
	_, child := tracer.Start(ctx, "child")
	child.SetStatus(codes.Error, "child failed")
	child.End()
	parent.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 2)

	// Child ended first, so it's at index 0.
	assert.Equal(t, codes.Error, spans[0].Status.Code)
	assert.Equal(t, codes.Ok, spans[1].Status.Code)
}
