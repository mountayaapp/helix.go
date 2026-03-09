package trace

import (
	"errors"
	"testing"

	internaltrace "github.com/mountayaapp/helix.go/internal/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	otelTrace "go.opentelemetry.io/otel/trace"
)

func TestSpanKindConstants(t *testing.T) {
	assert.Equal(t, otelTrace.SpanKindInternal, SpanKindInternal)
	assert.Equal(t, otelTrace.SpanKindServer, SpanKindServer)
	assert.Equal(t, otelTrace.SpanKindClient, SpanKindClient)
	assert.Equal(t, otelTrace.SpanKindProducer, SpanKindProducer)
	assert.Equal(t, otelTrace.SpanKindConsumer, SpanKindConsumer)
}

func TestSpanKindValues_AreDistinct(t *testing.T) {
	kinds := []SpanKind{
		SpanKindInternal,
		SpanKindServer,
		SpanKindClient,
		SpanKindProducer,
		SpanKindConsumer,
	}

	seen := make(map[SpanKind]bool)
	for _, k := range kinds {
		assert.False(t, seen[k], "SpanKind value %d is duplicated", k)
		seen[k] = true
	}
}

func TestStart_ReturnsNonNilSpan_WithoutTracer(t *testing.T) {
	ctx, s := Start(t.Context(), SpanKindInternal, "test-operation")

	assert.NotNil(t, ctx)
	assert.NotNil(t, s)

	assert.NotPanics(t, func() {
		s.SetAttributes(attribute.String("key", "value"))
		s.AddEvent("test")
		s.End()
	})
}

func TestStart_DifferentSpanKinds(t *testing.T) {
	kinds := []SpanKind{
		SpanKindInternal,
		SpanKindServer,
		SpanKindClient,
		SpanKindProducer,
		SpanKindConsumer,
	}

	for _, kind := range kinds {
		ctx, s := Start(t.Context(), kind, "test")

		assert.NotNil(t, ctx)
		assert.NotNil(t, s)
		s.End()
	}
}

func TestSpan_RecordError(t *testing.T) {
	_, s := Start(t.Context(), SpanKindInternal, "test")

	assert.NotPanics(t, func() {
		s.RecordError("something went wrong", errors.New("test error"))
		s.End()
	})
}

func TestSpan_FullLifecycle(t *testing.T) {
	_, s := Start(t.Context(), SpanKindInternal, "test")

	assert.NotPanics(t, func() {
		s.SetAttributes(attribute.String("operation", "test"))
		s.SetAttributes(attribute.Int64("attempt", 1))
		s.SetAttributes(attribute.Bool("retry", false))
		s.AddEvent("started")
		s.SetAttributes(attribute.Float64("duration", 1.5))
		s.AddEvent("completed")
		s.End()
	})
}

func TestSpan_NilInternalSpan(t *testing.T) {
	s := &Span{}

	t.Run("SetAttributes", func(t *testing.T) {
		assert.NotPanics(t, func() {
			s.SetAttributes(attribute.String("key", "value"))
		})
	})

	t.Run("AddEvent", func(t *testing.T) {
		assert.NotPanics(t, func() {
			s.AddEvent("test-event")
		})
	})

	t.Run("RecordError", func(t *testing.T) {
		assert.NotPanics(t, func() {
			s.RecordError("something went wrong", errors.New("test error"))
		})
	})

	t.Run("End", func(t *testing.T) {
		assert.NotPanics(t, func() {
			s.End()
		})
	})
}

func TestSpan_RecordError_NilError(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	ctx := internaltrace.ContextWithTracer(t.Context(), tr)
	_, s := Start(ctx, SpanKindInternal, "test")

	assert.NotPanics(t, func() {
		s.RecordError("should be no-op", nil)
	})

	assert.False(t, s.hasError)
}

func TestSpan_End_CalledTwice(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	ctx := internaltrace.ContextWithTracer(t.Context(), tr)
	_, s := Start(ctx, SpanKindInternal, "test")

	assert.NotPanics(t, func() {
		s.End()
		s.End()
	})
}

func TestSpan_FullLifecycle_WithError(t *testing.T) {
	tr := internaltrace.NewNopTracer()
	ctx := internaltrace.ContextWithTracer(t.Context(), tr)
	_, s := Start(ctx, SpanKindInternal, "test")

	assert.NotPanics(t, func() {
		s.SetAttributes(attribute.String("operation", "test"))
		s.SetAttributes(attribute.Int64("attempt", 1))
		s.RecordError("something went wrong", errors.New("test error"))
		s.End()
	})

	assert.True(t, s.hasError)
}
