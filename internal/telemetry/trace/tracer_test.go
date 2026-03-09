package trace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/resource"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var (
	SpanKindInternal = oteltrace.SpanKindInternal
	SpanKindServer   = oteltrace.SpanKindServer
)

func TestNewNopTracer(t *testing.T) {
	tr := NewNopTracer()

	t.Run("Provider", func(t *testing.T) {
		assert.NotNil(t, tr.Provider())
	})

	t.Run("Shutdown", func(t *testing.T) {
		assert.NoError(t, tr.Shutdown(t.Context()))
	})
}

func newTestResource(t *testing.T) *resource.Resource {
	t.Helper()
	res, err := resource.New(t.Context())
	require.NoError(t, err)
	return res
}

func TestNewTracer_Exporter(t *testing.T) {
	t.Run("None", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "none")

		tr, err := NewTracer(newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, tr.Provider())

		ctx, span := tr.Start(t.Context(), SpanKindInternal, "noop-span")
		assert.NotNil(t, ctx)
		assert.NotNil(t, span)
		span.End()

		assert.NoError(t, tr.Shutdown(t.Context()))
	})

	t.Run("Console", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "console")

		tr, err := NewTracer(newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, tr.Provider())
		assert.NoError(t, tr.Shutdown(t.Context()))
	})

	t.Run("OTLP", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "otlp")

		tr, err := NewTracer(newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, tr.Provider())
		assert.NoError(t, tr.Shutdown(t.Context()))
	})

	t.Run("Default", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "")

		tr, err := NewTracer(newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, tr.Provider())
		assert.NoError(t, tr.Shutdown(t.Context()))
	})

	t.Run("Invalid", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "invalid-exporter")

		_, err := NewTracer(newTestResource(t))
		assert.Error(t, err)
	})
}

func TestNewTracer_Protocol(t *testing.T) {
	t.Run("GRPC", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")

		tr, err := NewTracer(newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, tr.Provider())
		assert.NoError(t, tr.Shutdown(t.Context()))
	})

	t.Run("HTTP", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")

		tr, err := NewTracer(newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, tr.Provider())
		assert.NoError(t, tr.Shutdown(t.Context()))
	})
}

func TestNewTracer_CustomEndpoint(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:9999")

	tr, err := NewTracer(newTestResource(t))
	require.NoError(t, err)
	assert.NotNil(t, tr.Provider())
	assert.NoError(t, tr.Shutdown(t.Context()))
}

func TestNewTracer_ExporterNone_SpansAreNoop(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")

	tr, err := NewTracer(newTestResource(t))
	require.NoError(t, err)

	ctx := ContextWithTracer(t.Context(), tr)
	ctx, span := TracerFromContext(ctx).Start(ctx, SpanKindServer, "test-op")
	assert.NotNil(t, ctx)
	assert.NotNil(t, span)

	assert.NotPanics(t, func() {
		span.End()
	})

	assert.NoError(t, tr.Shutdown(t.Context()))
}
