package log

import (
	"context"
	"time"

	"github.com/mountayaapp/helix.go/internal/telemetry/log"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

/*
LogLevel controls the minimum log level.
*/
type LogLevel int8

const (
	LogLevelDebug LogLevel = LogLevel(zapcore.DebugLevel)
	LogLevelInfo  LogLevel = LogLevel(zapcore.InfoLevel)
	LogLevelWarn  LogLevel = LogLevel(zapcore.WarnLevel)
	LogLevelError LogLevel = LogLevel(zapcore.ErrorLevel)
)

/*
Field is a structured log field. Type alias for zap.Field — no wrapper overhead.
*/
type Field = zap.Field

/*
String constructs a Field with the given key and string value.
*/
func String(key, val string) Field {
	return zap.String(key, val)
}

/*
Int constructs a Field with the given key and int value.
*/
func Int(key string, val int) Field {
	return zap.Int(key, val)
}

/*
Int64 constructs a Field with the given key and int64 value.
*/
func Int64(key string, val int64) Field {
	return zap.Int64(key, val)
}

/*
Float64 constructs a Field with the given key and float64 value.
*/
func Float64(key string, val float64) Field {
	return zap.Float64(key, val)
}

/*
Bool constructs a Field with the given key and bool value.
*/
func Bool(key string, val bool) Field {
	return zap.Bool(key, val)
}

/*
Err constructs a Field that lazily stores the error's message under the key
"error".
*/
func Err(err error) Field {
	return zap.Error(err)
}

/*
Any constructs a Field with the given key and an arbitrary value.
*/
func Any(key string, val any) Field {
	return zap.Any(key, val)
}

/*
Duration constructs a Field with the given key and time.Duration value.
*/
func Duration(key string, val time.Duration) Field {
	return zap.Duration(key, val)
}

/*
Debug logs a message at the debug level with optional structured fields. It
extracts the Logger from the context. If no Logger is found, the call is a no-op.
*/
func Debug(ctx context.Context, msg string, fields ...Field) {
	if l := log.LoggerFromContext(ctx); l != nil {
		l.Debug(ctx, msg, fields...)
	}
}

/*
Info logs a message at the info level with optional structured fields. It
extracts the Logger from the context. If no Logger is found, the call is a no-op.
*/
func Info(ctx context.Context, msg string, fields ...Field) {
	if l := log.LoggerFromContext(ctx); l != nil {
		l.Info(ctx, msg, fields...)
	}
}

/*
Warn logs a message at the warn level with optional structured fields. It
extracts the Logger from the context. If no Logger is found, the call is a no-op.
*/
func Warn(ctx context.Context, msg string, fields ...Field) {
	if l := log.LoggerFromContext(ctx); l != nil {
		l.Warn(ctx, msg, fields...)
	}
}

/*
Error logs a message at the error level with optional structured fields. It
extracts the Logger from the context. If no Logger is found, the call is a no-op.
*/
func Error(ctx context.Context, msg string, fields ...Field) {
	if l := log.LoggerFromContext(ctx); l != nil {
		l.Error(ctx, msg, fields...)
	}
}
