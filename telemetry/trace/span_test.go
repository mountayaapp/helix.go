package trace

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	otelTrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestSpanKindConstants(t *testing.T) {
	assert.Equal(t, SpanKind(1), SpanKindInternal)
	assert.Equal(t, SpanKind(2), SpanKindServer)
	assert.Equal(t, SpanKind(3), SpanKindClient)
	assert.Equal(t, SpanKind(4), SpanKindProducer)
	assert.Equal(t, SpanKind(5), SpanKindConsumer)
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

func TestSpanKindMapsToOTelSpanKind(t *testing.T) {
	testcases := []struct {
		name     string
		kind     SpanKind
		expected otelTrace.SpanKind
	}{
		{
			name:     "internal maps to OTel internal",
			kind:     SpanKindInternal,
			expected: otelTrace.SpanKindInternal,
		},
		{
			name:     "server maps to OTel server",
			kind:     SpanKindServer,
			expected: otelTrace.SpanKindServer,
		},
		{
			name:     "client maps to OTel client",
			kind:     SpanKindClient,
			expected: otelTrace.SpanKindClient,
		},
		{
			name:     "producer maps to OTel producer",
			kind:     SpanKindProducer,
			expected: otelTrace.SpanKindProducer,
		},
		{
			name:     "consumer maps to OTel consumer",
			kind:     SpanKindConsumer,
			expected: otelTrace.SpanKindConsumer,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, otelTrace.SpanKind(tc.kind))
		})
	}
}

func newNoopSpan() *Span {
	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")
	return &Span{client: span}
}

func TestSpan_SetStringAttribute(t *testing.T) {
	s := newNoopSpan()

	assert.NotPanics(t, func() {
		s.SetStringAttribute("key", "value")
	})
}

func TestSpan_SetSliceStringAttribute(t *testing.T) {
	s := newNoopSpan()

	assert.NotPanics(t, func() {
		s.SetSliceStringAttribute("tags", []string{"a", "b", "c"})
	})
}

func TestSpan_SetSliceStringAttribute_Empty(t *testing.T) {
	s := newNoopSpan()

	assert.NotPanics(t, func() {
		s.SetSliceStringAttribute("tags", []string{})
	})
}

func TestSpan_SetBoolAttribute(t *testing.T) {
	s := newNoopSpan()

	assert.NotPanics(t, func() {
		s.SetBoolAttribute("enabled", true)
		s.SetBoolAttribute("disabled", false)
	})
}

func TestSpan_SetIntAttribute(t *testing.T) {
	s := newNoopSpan()

	assert.NotPanics(t, func() {
		s.SetIntAttribute("count", 42)
		s.SetIntAttribute("negative", -1)
		s.SetIntAttribute("zero", 0)
	})
}

func TestSpan_SetFloatAttribute(t *testing.T) {
	s := newNoopSpan()

	assert.NotPanics(t, func() {
		s.SetFloatAttribute("ratio", 3.14)
		s.SetFloatAttribute("zero", 0.0)
		s.SetFloatAttribute("negative", -1.5)
	})
}

func TestSpan_RecordError(t *testing.T) {
	s := newNoopSpan()

	assert.False(t, s.hasError)

	s.RecordError("something went wrong", errors.New("test error"))

	assert.True(t, s.hasError)
}

func TestSpan_RecordError_SetsHasError(t *testing.T) {
	s := newNoopSpan()

	s.RecordError("first error", errors.New("err 1"))
	s.RecordError("second error", errors.New("err 2"))

	assert.True(t, s.hasError)
}

func TestSpan_AddEvent(t *testing.T) {
	s := newNoopSpan()

	assert.NotPanics(t, func() {
		s.AddEvent("processing_started")
		s.AddEvent("processing_completed")
	})
}

func TestSpan_Context(t *testing.T) {
	s := newNoopSpan()

	sc := s.Context()

	assert.False(t, sc.HasTraceID())
	assert.False(t, sc.HasSpanID())
}

func TestSpan_End(t *testing.T) {
	s := newNoopSpan()

	assert.NotPanics(t, func() {
		s.End()
	})
}

func TestSpan_End_WithoutError(t *testing.T) {
	s := newNoopSpan()

	assert.False(t, s.hasError)

	assert.NotPanics(t, func() {
		s.End()
	})
}

func TestSpan_End_WithError(t *testing.T) {
	s := newNoopSpan()
	s.RecordError("failure", errors.New("test"))

	assert.True(t, s.hasError)

	assert.NotPanics(t, func() {
		s.End()
	})
}

func TestSpan_FullLifecycle(t *testing.T) {
	s := newNoopSpan()

	s.SetStringAttribute("operation", "test")
	s.SetIntAttribute("attempt", 1)
	s.SetBoolAttribute("retry", false)
	s.AddEvent("started")
	s.SetFloatAttribute("duration", 1.5)
	s.AddEvent("completed")
	s.End()
}

func TestSpan_FullLifecycleWithError(t *testing.T) {
	s := newNoopSpan()

	s.SetStringAttribute("operation", "failing_test")
	s.AddEvent("started")
	s.RecordError("operation failed", errors.New("timeout"))

	assert.True(t, s.hasError)

	s.End()
}

func TestStart_ReturnsSpan(t *testing.T) {
	ctx, s := Start(t.Context(), SpanKindInternal, "test-operation")

	assert.NotNil(t, ctx)
	if s != nil {
		assert.NotNil(t, s.client)
		s.End()
	}
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
		if s != nil {
			s.End()
		}
	}
}
