package log

import (
	"errors"
	"testing"
	"time"

	internallog "github.com/mountayaapp/helix.go/internal/telemetry/log"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zapcore"
)

func TestLogFunctions_NoLoggerInContext(t *testing.T) {
	ctx := t.Context()

	t.Run("Debug", func(t *testing.T) {
		Debug(ctx, "test message")
	})

	t.Run("Info", func(t *testing.T) {
		Info(ctx, "test message")
	})

	t.Run("Warn", func(t *testing.T) {
		Warn(ctx, "test message")
	})

	t.Run("Error", func(t *testing.T) {
		Error(ctx, "test message")
	})
}

func TestLogFunctions_WithLogger(t *testing.T) {
	l := internallog.NewNopLogger()
	ctx := internallog.ContextWithLogger(t.Context(), l)

	t.Run("Debug", func(t *testing.T) {
		Debug(ctx, "test message", String("key", "val"))
	})

	t.Run("Info", func(t *testing.T) {
		Info(ctx, "test message", Int("count", 42))
	})

	t.Run("Warn", func(t *testing.T) {
		Warn(ctx, "test message", Bool("flag", true))
	})

	t.Run("Error", func(t *testing.T) {
		Error(ctx, "test message", Any("data", map[string]int{"a": 1}))
	})
}

func TestLogLevelConstants(t *testing.T) {
	assert.Equal(t, LogLevel(zapcore.DebugLevel), LogLevelDebug)
	assert.Equal(t, LogLevel(zapcore.InfoLevel), LogLevelInfo)
	assert.Equal(t, LogLevel(zapcore.WarnLevel), LogLevelWarn)
	assert.Equal(t, LogLevel(zapcore.ErrorLevel), LogLevelError)

	assert.Less(t, LogLevelDebug, LogLevelInfo)
	assert.Less(t, LogLevelInfo, LogLevelWarn)
	assert.Less(t, LogLevelWarn, LogLevelError)
}

func TestFieldConstructors(t *testing.T) {
	tests := []struct {
		name     string
		field    Field
		wantKey  string
		wantType zapcore.FieldType
	}{
		{
			name:     "String",
			field:    String("str_key", "str_val"),
			wantKey:  "str_key",
			wantType: zapcore.StringType,
		},
		{
			name:     "Int",
			field:    Int("int_key", 42),
			wantKey:  "int_key",
			wantType: zapcore.Int64Type,
		},
		{
			name:     "Int64",
			field:    Int64("int64_key", 99),
			wantKey:  "int64_key",
			wantType: zapcore.Int64Type,
		},
		{
			name:     "Float64",
			field:    Float64("float_key", 3.14),
			wantKey:  "float_key",
			wantType: zapcore.Float64Type,
		},
		{
			name:     "Bool",
			field:    Bool("bool_key", true),
			wantKey:  "bool_key",
			wantType: zapcore.BoolType,
		},
		{
			name:     "Err",
			field:    Err(errors.New("boom")),
			wantKey:  "error",
			wantType: zapcore.ErrorType,
		},
		{
			name:     "Any",
			field:    Any("any_key", map[string]int{"a": 1}),
			wantKey:  "any_key",
			wantType: zapcore.ReflectType,
		},
		{
			name:     "Duration",
			field:    Duration("dur_key", 5*time.Second),
			wantKey:  "dur_key",
			wantType: zapcore.DurationType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantKey, tc.field.Key)
			assert.Equal(t, tc.wantType, tc.field.Type)
		})
	}
}

func TestPackageLevelFunctions_WithFields(t *testing.T) {
	l := internallog.NewNopLogger()
	ctx := internallog.ContextWithLogger(t.Context(), l)

	fields := []Field{
		String("key", "val"),
		Int("count", 1),
		Int64("big", 1<<40),
		Float64("ratio", 0.5),
		Bool("ok", true),
		Err(errors.New("test error")),
		Any("payload", struct{ X int }{X: 7}),
		Duration("elapsed", 200*time.Millisecond),
	}

	Debug(ctx, "debug msg", fields...)
	Info(ctx, "info msg", fields...)
	Warn(ctx, "warn msg", fields...)
	Error(ctx, "error msg", fields...)
}

func TestPackageLevelFunctions_NoLoggerInContext(t *testing.T) {
	ctx := t.Context()

	fields := []Field{
		String("key", "val"),
		Int("count", 1),
		Bool("flag", false),
		Err(errors.New("no logger")),
	}

	Debug(ctx, "debug msg", fields...)
	Info(ctx, "info msg", fields...)
	Warn(ctx, "warn msg", fields...)
	Error(ctx, "error msg", fields...)
}
