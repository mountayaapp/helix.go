package log

import (
	"context"
	"time"

	"github.com/mountayaapp/helix.go/internal/telemetry/log"

	"go.uber.org/zap"
)

// Level is the minimum level a Service logs at, set through
// service.Config.LogLevel. Its zero value means unset.
type Level int8

// The levels a Service can log at, from the most verbose to the least.
const (
	LevelDebug Level = iota + 1
	LevelInfo
	LevelWarn
	LevelError
)

// Field is a structured log field. Type alias for zap.Field — no wrapper overhead.
type Field = zap.Field

// String constructs a Field with the given key and string value.
func String(key, val string) Field {
	return zap.String(key, val)
}

// Int constructs a Field with the given key and int value.
func Int(key string, val int) Field {
	return zap.Int(key, val)
}

// Int64 constructs a Field with the given key and int64 value.
func Int64(key string, val int64) Field {
	return zap.Int64(key, val)
}

// Float64 constructs a Field with the given key and float64 value.
func Float64(key string, val float64) Field {
	return zap.Float64(key, val)
}

// Bool constructs a Field with the given key and bool value.
func Bool(key string, val bool) Field {
	return zap.Bool(key, val)
}

// Err constructs a Field that lazily stores the error's message under the key
// "error". The error goes through the function registered with
// telemetry/trace.SetErrorSanitizer before it is written.
func Err(err error) Field {
	return zap.Error(err)
}

// Any constructs a Field with the given key and an arbitrary value. A value that
// is an error is written as Err writes one, through the function registered with
// telemetry/trace.SetErrorSanitizer; any other is written as it is.
func Any(key string, val any) Field {
	return zap.Any(key, val)
}

// Duration constructs a Field with the given key and time.Duration value.
func Duration(key string, val time.Duration) Field {
	return zap.Duration(key, val)
}

// Debug logs a message at the debug level with optional structured fields, through
// the context's Logger, or the process-wide one when the context carries none.
func Debug(ctx context.Context, msg string, fields ...Field) {
	loggerFrom(ctx).Debug(ctx, msg, fields...)
}

// Info logs a message at the info level with optional structured fields, through
// the context's Logger, or the process-wide one when the context carries none.
func Info(ctx context.Context, msg string, fields ...Field) {
	loggerFrom(ctx).Info(ctx, msg, fields...)
}

// Warn logs a message at the warn level with optional structured fields, through
// the context's Logger, or the process-wide one when the context carries none.
func Warn(ctx context.Context, msg string, fields ...Field) {
	loggerFrom(ctx).Warn(ctx, msg, fields...)
}

// Error logs a message at the error level with optional structured fields, through
// the context's Logger, or the process-wide one when the context carries none.
func Error(ctx context.Context, msg string, fields ...Field) {
	loggerFrom(ctx).Error(ctx, msg, fields...)
}

// loggerFrom returns the Logger carried by ctx or, when it carries none, the
// process-wide one.
func loggerFrom(ctx context.Context) *log.Logger {
	if l := log.FromContext(ctx); l != nil {
		return l
	}

	return log.Default()
}
