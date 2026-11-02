package log

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestNewLoggerWithProvider_Levels(t *testing.T) {
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
			assert.NotNil(t, newTestLogger(t, tc.level))
		})
	}
}

// TestNewLoggerWithProvider_Attributes covers the resource attributes being applied
// as initial zap fields. They arrive as []attribute.KeyValue rather than a
// *resource.Resource, which is what keeps the SDK out of this package.
func TestNewLoggerWithProvider_Attributes(t *testing.T) {
	l := NewLoggerWithProvider(lognoop.NewLoggerProvider(), zapcore.InfoLevel, []attribute.KeyValue{
		attribute.String("service.name", "test-svc"),
		attribute.Int64("service.version", 1),
	})

	assert.NotNil(t, l)
	assert.NotPanics(t, func() { l.Info(t.Context(), "with attributes") })
}

func TestNewNopLogger(t *testing.T) {
	l := NewNopLogger()
	assert.NotNil(t, l)

	t.Run("Provider", func(t *testing.T) {
		// The no-op logger still exposes a provider, so that callers never get a
		// nil interface back from Provider().
		assert.NotNil(t, l.Provider())
	})

	t.Run("Sync", func(t *testing.T) {
		assert.NoError(t, l.Sync())
	})
}

func TestDefaultLevel(t *testing.T) {
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
			assert.Equal(t, tc.expected, DefaultLevel())
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

func newTestLogger(t *testing.T, level zapcore.Level) *Logger {
	t.Helper()

	return NewLoggerWithProvider(lognoop.NewLoggerProvider(), level, nil)
}
