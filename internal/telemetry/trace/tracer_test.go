package trace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var (
	SpanKindInternal = oteltrace.SpanKindInternal
	SpanKindServer   = oteltrace.SpanKindServer
)

// newRecordingTracer builds a Tracer over a recording SDK provider. Exporter
// selection is no longer this package's concern — service owns it, along with the
// provider's lifecycle — so the SDK appears here only as a test double.
func newRecordingTracer(t *testing.T) (*Tracer, *tracetest.InMemoryExporter) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	// t.Context() is already cancelled by the time cleanup runs.
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	return NewTracerWithProvider(provider), exporter
}

func TestNewNopTracer(t *testing.T) {
	tr := NewNopTracer()

	assert.NotNil(t, tr.Provider())

	ctx, span := tr.Start(t.Context(), SpanKindInternal, "noop-span")
	assert.NotNil(t, ctx)
	assert.NotNil(t, span)
	assert.NotPanics(t, func() { span.End() })
}

func TestNewTracerWithProvider(t *testing.T) {
	tr, _ := newRecordingTracer(t)

	assert.NotNil(t, tr.Provider())
}

func TestTracer_StartRecordsSpan(t *testing.T) {
	tr, exporter := newRecordingTracer(t)

	ctx, span := tr.Start(t.Context(), SpanKindServer, "test-op")
	require.NotNil(t, ctx)
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "test-op", spans[0].Name)
	assert.Equal(t, oteltrace.SpanKindServer, spans[0].SpanKind)
}

func TestTracer_StartViaContext(t *testing.T) {
	tr, exporter := newRecordingTracer(t)

	ctx := NewContext(t.Context(), tr)
	require.Same(t, tr, FromContext(ctx))

	ctx, span := FromContext(ctx).Start(ctx, SpanKindServer, "test-op")
	assert.NotNil(t, ctx)
	assert.NotPanics(t, func() { span.End() })

	assert.Len(t, exporter.GetSpans(), 1)
}

func TestTracer_ChildSpanInheritsTrace(t *testing.T) {
	tr, exporter := newRecordingTracer(t)

	ctx, parent := tr.Start(t.Context(), SpanKindServer, "parent")
	_, child := tr.Start(ctx, SpanKindInternal, "child")
	child.End()
	parent.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 2)
	assert.Equal(t, spans[0].SpanContext.TraceID(), spans[1].SpanContext.TraceID(),
		"a child span must share its parent's trace")
}
