package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/trace"
)

func TestFromContextToZapFields_EmptyContext(t *testing.T) {
	ctx := t.Context()

	fields := FromContextToZapFields(ctx)

	assert.Empty(t, fields)
}

func TestFromContextToZapFields_WithTraceID(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})

	ctx := trace.ContextWithRemoteSpanContext(t.Context(), sc)
	fields := FromContextToZapFields(ctx)

	assert.Len(t, fields, 2)
	assert.Equal(t, "trace_id", fields[0].Key)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", fields[0].String)
	assert.Equal(t, "span_id", fields[1].Key)
	assert.Equal(t, "00f067aa0ba902b7", fields[1].String)
}

func TestFromContextToZapFields_WithTraceIDOnly(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
	})

	ctx := trace.ContextWithRemoteSpanContext(t.Context(), sc)
	fields := FromContextToZapFields(ctx)

	assert.Len(t, fields, 1)
	assert.Equal(t, "trace_id", fields[0].Key)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", fields[0].String)
}

func TestFromContextToZapFields_WithNoTraceID(t *testing.T) {

	// SpanContext without TraceID should produce no fields.
	sc := trace.NewSpanContext(trace.SpanContextConfig{})

	ctx := trace.ContextWithRemoteSpanContext(t.Context(), sc)
	fields := FromContextToZapFields(ctx)

	assert.Empty(t, fields)
}

func TestFromContextToZapFields_NilContext(t *testing.T) {

	// context.Background() with no span context.
	fields := FromContextToZapFields(t.Context())

	assert.Empty(t, fields)
}
