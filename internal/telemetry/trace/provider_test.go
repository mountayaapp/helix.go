package trace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestWithDroppedScopes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := WithDroppedScopes(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))

	_, dropped := provider.Tracer("gocloud.dev/blob").Start(t.Context(), "gocloud.dev/blob.NewWriter")
	dropped.End()

	_, kept := provider.Tracer(ScopeName).Start(t.Context(), "Bucket: Write")
	kept.End()

	ended := recorder.Ended()
	require.Len(t, ended, 1, "only the dropped scope's span must be suppressed")
	assert.Equal(t, "Bucket: Write", ended[0].Name())
}

// TestWithDroppedScopesLeavesSpanContextUsable pins that suppression does not
// break propagation: a no-op span still carries a valid context, so a child that
// an un-dropped scope creates underneath it is not orphaned into a new trace.
func TestWithDroppedScopesLeavesSpanContextUsable(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := WithDroppedScopes(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))

	ctx, parent := provider.Tracer(ScopeName).Start(t.Context(), "Bucket: Write")
	ctx, dropped := provider.Tracer("gocloud.dev/blob").Start(ctx, "gocloud.dev/blob.NewWriter")
	_, child := provider.Tracer(ScopeName).Start(ctx, "Bucket: Read")

	child.End()
	dropped.End()
	parent.End()

	ended := recorder.Ended()
	require.Len(t, ended, 2)
	assert.Equal(t, ended[1].SpanContext().TraceID(), ended[0].SpanContext().TraceID())
}
