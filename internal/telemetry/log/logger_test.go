package log

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestNewLogger_Success(t *testing.T) {
	levels := []struct {
		name  string
		level zapcore.Level
	}{
		{"debug", zapcore.DebugLevel},
		{"info", zapcore.InfoLevel},
		{"warn", zapcore.WarnLevel},
		{"error", zapcore.ErrorLevel},
	}

	for _, tc := range levels {
		t.Run(tc.name, func(t *testing.T) {
			l, err := NewLogger(tc.level, nil)
			require.NoError(t, err)
			assert.NotNil(t, l)
		})
	}
}

func TestNewLogger_WithResource(t *testing.T) {
	res, err := resource.New(t.Context(),
		resource.WithAttributes(
			attribute.String("service.name", "test-svc"),
			attribute.Int64("service.version", 1),
		),
	)
	require.NoError(t, err)

	l, err := NewLogger(zapcore.InfoLevel, res)
	require.NoError(t, err)
	assert.NotNil(t, l)
}

func TestNewNopLogger(t *testing.T) {
	l := NewNopLogger()
	assert.NotNil(t, l)

	t.Run("Shutdown", func(t *testing.T) {
		assert.NoError(t, l.Shutdown(t.Context()))
	})

	t.Run("Provider", func(t *testing.T) {
		assert.Nil(t, l.Provider())
	})

	t.Run("Sync", func(t *testing.T) {
		assert.NoError(t, l.Sync())
	})
}

func TestDefaultLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected zapcore.Level
	}{
		{"debug", "debug", zapcore.DebugLevel},
		{"info", "info", zapcore.InfoLevel},
		{"warn", "warn", zapcore.WarnLevel},
		{"error", "error", zapcore.ErrorLevel},
		{"DEBUG uppercase", "DEBUG", zapcore.DebugLevel},
		{"Info mixed case", "Info", zapcore.InfoLevel},
		{"empty defaults to info", "", zapcore.InfoLevel},
		{"invalid defaults to info", "invalid", zapcore.InfoLevel},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_LOG_LEVEL", tc.envValue)
			assert.Equal(t, tc.expected, DefaultLogLevel())
		})
	}
}

func TestLogger_With(t *testing.T) {
	t.Run("ReturnsNewLogger", func(t *testing.T) {
		parent := NewNopLogger()
		child := parent.With(zap.String("component", "auth"))

		assert.NotNil(t, child)
		assert.NotSame(t, parent, child)
	})

	t.Run("PreservesParent", func(t *testing.T) {
		parent := NewNopLogger()
		_ = parent.With(zap.String("extra", "field"))

		ctx := t.Context()
		parent.Debug(ctx, "still works")
		parent.Info(ctx, "still works")
		parent.Warn(ctx, "still works")
		parent.Error(ctx, "still works")
	})
}

func newTestResource(t *testing.T) *resource.Resource {
	t.Helper()
	res, err := resource.New(t.Context())
	require.NoError(t, err)
	return res
}

func TestNewLogger_Exporter(t *testing.T) {
	t.Run("None", func(t *testing.T) {
		t.Setenv("OTEL_LOGS_EXPORTER", "none")

		l, err := NewLogger(zapcore.InfoLevel, newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, l)
		assert.NotNil(t, l.Provider())

		l.Info(t.Context(), "test message")

		assert.NoError(t, l.Shutdown(t.Context()))
	})

	t.Run("Console", func(t *testing.T) {
		t.Setenv("OTEL_LOGS_EXPORTER", "console")

		l, err := NewLogger(zapcore.InfoLevel, newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, l)
		assert.NoError(t, l.Shutdown(t.Context()))
	})

	t.Run("OTLP", func(t *testing.T) {
		t.Setenv("OTEL_LOGS_EXPORTER", "otlp")

		l, err := NewLogger(zapcore.InfoLevel, newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, l)
		assert.NoError(t, l.Shutdown(t.Context()))
	})

	t.Run("Default", func(t *testing.T) {
		t.Setenv("OTEL_LOGS_EXPORTER", "")

		l, err := NewLogger(zapcore.InfoLevel, newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, l)
		assert.NoError(t, l.Shutdown(t.Context()))
	})

	t.Run("Invalid", func(t *testing.T) {
		t.Setenv("OTEL_LOGS_EXPORTER", "invalid-exporter")

		_, err := NewLogger(zapcore.InfoLevel, newTestResource(t))
		assert.Error(t, err)
	})
}

func TestNewLogger_Protocol(t *testing.T) {
	t.Run("GRPC", func(t *testing.T) {
		t.Setenv("OTEL_LOGS_EXPORTER", "otlp")
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")

		l, err := NewLogger(zapcore.InfoLevel, newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, l)
		assert.NoError(t, l.Shutdown(t.Context()))
	})

	t.Run("HTTP", func(t *testing.T) {
		t.Setenv("OTEL_LOGS_EXPORTER", "otlp")
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")

		l, err := NewLogger(zapcore.InfoLevel, newTestResource(t))
		require.NoError(t, err)
		assert.NotNil(t, l)
		assert.NoError(t, l.Shutdown(t.Context()))
	})
}

func TestNewLogger_CustomEndpoint(t *testing.T) {
	t.Setenv("OTEL_LOGS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:9999")

	l, err := NewLogger(zapcore.InfoLevel, newTestResource(t))
	require.NoError(t, err)
	assert.NotNil(t, l)
	assert.NoError(t, l.Shutdown(t.Context()))
}

func TestNewLogger_ExporterNone_AllLevelsWork(t *testing.T) {
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	levels := []zapcore.Level{zapcore.DebugLevel, zapcore.InfoLevel, zapcore.WarnLevel, zapcore.ErrorLevel}

	for _, level := range levels {
		l, err := NewLogger(level, newTestResource(t))
		require.NoError(t, err)

		ctx := ContextWithLogger(t.Context(), l)
		if ll := LoggerFromContext(ctx); ll != nil {
			ll.Debug(ctx, "debug")
			ll.Info(ctx, "info")
			ll.Warn(ctx, "warn")
			ll.Error(ctx, "error")
		}

		assert.NoError(t, l.Shutdown(t.Context()))
	}
}

func TestNewLogger_ExporterNone_WithResource(t *testing.T) {
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	res, err := resource.New(t.Context(),
		resource.WithAttributes(
			attribute.String("service.name", "test-svc"),
			attribute.Int64("service.version", 1),
		),
	)
	require.NoError(t, err)

	l, err := NewLogger(zapcore.InfoLevel, res)
	require.NoError(t, err)
	assert.NotNil(t, l)
	assert.NoError(t, l.Shutdown(t.Context()))
}
