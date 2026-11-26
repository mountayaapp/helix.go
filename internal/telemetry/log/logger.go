package log

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/mountaya/helix.go/internal/telemetry/sanitize"
	internaltrace "github.com/mountaya/helix.go/internal/telemetry/trace"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger wraps zap with structured field support, automatic trace correlation,
// and OpenTelemetry log export. Created by service.New, stored in context, and
// used by the public telemetry/log package-level functions.
type Logger struct {
	zap      *zap.Logger
	provider otellog.LoggerProvider
}

// Debug logs a message at the debug level with optional structured fields. The
// record is correlated with the span carried by ctx, if any.
func (l *Logger) Debug(ctx context.Context, msg string, fields ...zap.Field) {
	l.zap.Debug(msg, withContext(ctx, sanitizeErrors(fields))...)
}

// Info logs a message at the info level with optional structured fields. The
// record is correlated with the span carried by ctx, if any.
func (l *Logger) Info(ctx context.Context, msg string, fields ...zap.Field) {
	l.zap.Info(msg, withContext(ctx, sanitizeErrors(fields))...)
}

// Warn logs a message at the warn level with optional structured fields. The
// record is correlated with the span carried by ctx, if any.
func (l *Logger) Warn(ctx context.Context, msg string, fields ...zap.Field) {
	l.zap.Warn(msg, withContext(ctx, sanitizeErrors(fields))...)
}

// Error logs a message at the error level with optional structured fields. The
// record is correlated with the span carried by ctx, if any.
func (l *Logger) Error(ctx context.Context, msg string, fields ...zap.Field) {
	l.zap.Error(msg, withContext(ctx, sanitizeErrors(fields))...)
}

// With returns a child Logger that always includes the given fields.
func (l *Logger) With(fields ...zap.Field) *Logger {
	return &Logger{zap: l.zap.With(sanitizeErrors(fields)...), provider: l.provider}
}

// sanitizeErrors returns fields with every error among them, a field built by
// zap.Error or zap.Any from an error, made what the function registered with
// telemetry/trace.SetErrorSanitizer makes of it (sanitize.Error), under the same
// key. An error it turns into nil is not written at all.
//
// Every record goes through it on its way to both cores, standard error and the
// export alike, so no call site can write an error unrestated by forgetting to.
// Only an error is handed over: a value that is no error, a string holding an
// error's text included, is written as it is.
//
// With nothing registered, or no error among them, fields is returned as it is,
// so the record is the one it always was. Otherwise the result never shares a
// backing array with fields, which belongs to the caller.
func sanitizeErrors(fields []zap.Field) []zap.Field {
	if !sanitize.Registered() {
		return fields
	}

	var sanitized []zap.Field
	for i, field := range fields {
		if field.Type != zapcore.ErrorType {
			continue
		}

		err, ok := field.Interface.(error)
		if !ok || err == nil {
			continue
		}

		if sanitized == nil {
			sanitized = slices.Clone(fields)
		}

		sanitized[i] = zap.NamedError(field.Key, sanitize.Error(err))
	}

	if sanitized == nil {
		return fields
	}

	return sanitized
}

// Sync flushes any buffered log entries.
func (l *Logger) Sync() error {
	return l.zap.Sync()
}

// Provider returns the underlying OpenTelemetry LoggerProvider. Integrations that
// need to wire OpenTelemetry-native log bridges use this.
//
// The return type is the API interface, not the SDK implementation, so that the
// public surface does not commit to an SDK type.
func (l *Logger) Provider() otellog.LoggerProvider {
	return l.provider
}

// withContext returns fields with ctx appended as a field the cores can read and
// the encoders skip, when ctx carries a span worth correlating with.
//
// The context travels as a field because that is the only channel a zap core has.
// The OpenTelemetry bridge reads a field holding a context.Context as the record's
// context, which is what gives an exported record its native trace and span IDs;
// traceCore reads the same field to put them on the stderr line. SkipType keeps
// the JSON encoder from trying to serialize the context itself.
//
// A context without a valid span adds nothing, so an uncorrelated log line pays
// for no extra field. The result never shares a backing array with fields, which
// belongs to the caller.
func withContext(ctx context.Context, fields []zap.Field) []zap.Field {
	if ctx == nil || !trace.SpanContextFromContext(ctx).IsValid() {
		return fields
	}

	return append(slices.Clip(fields), zap.Field{
		Key:       "context",
		Type:      zapcore.SkipType,
		Interface: ctx,
	})
}

// traceCore is the stderr core, extended to write the trace_id and span_id of the
// context a call carried. The export path does not need it: the bridge correlates
// a record natively, from the same context.
type traceCore struct {
	zapcore.Core
}

// With returns a traceCore over the wrapped core's With.
func (c traceCore) With(fields []zapcore.Field) zapcore.Core {
	return traceCore{Core: c.Core.With(fields)}
}

// Check adds this core, rather than the wrapped one, so that Write is the one
// below.
func (c traceCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}

	return ce
}

// Write appends trace_id and span_id from the context field, if there is one, and
// writes the entry through the wrapped core.
func (c traceCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	for _, field := range fields {
		ctx, ok := field.Interface.(context.Context)
		if !ok {
			continue
		}

		sc := trace.SpanContextFromContext(ctx)
		if !sc.IsValid() {
			break
		}

		fields = append(slices.Clip(fields),
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)

		break
	}

	return c.Core.Write(ent, fields)
}

// levelCore drops entries below a level before they reach the core it wraps.
//
// The OpenTelemetry bridge has no level of its own: it asks the provider, and an
// SDK provider accepts everything. Without this, a debug record nobody configured
// stderr to show would still be exported.
type levelCore struct {
	zapcore.Core

	level zapcore.LevelEnabler
}

// Enabled reports whether both the level and the wrapped core accept l.
func (c levelCore) Enabled(l zapcore.Level) bool {
	return c.level.Enabled(l) && c.Core.Enabled(l)
}

// With returns a levelCore over the wrapped core's With.
func (c levelCore) With(fields []zapcore.Field) zapcore.Core {
	return levelCore{Core: c.Core.With(fields), level: c.level}
}

// Check defers to the wrapped core for an entry at or above the level.
func (c levelCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if !c.level.Enabled(ent.Level) {
		return ce
	}

	return c.Core.Check(ent, ce)
}

// NewLoggerWithProvider creates a Logger around a caller-supplied provider, which
// also writes stderr JSON with attrs applied as initial fields. level applies to
// both: a record below it is neither written nor exported.
//
// The provider is an API type and the caller keeps ownership of its lifecycle.
// That is deliberate: it is what keeps the OpenTelemetry SDK — and with it every
// OTLP exporter — out of this module's build graph. The service package builds the
// SDK provider and is responsible for shutting it down. attrs is likewise the
// resource's attributes rather than the resource itself, which is an SDK type.
func NewLoggerWithProvider(provider otellog.LoggerProvider, level zapcore.Level, attrs []attribute.KeyValue) *Logger {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.MessageKey = "message"
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder

	stderrCore := traceCore{Core: zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(os.Stderr),
		level,
	)}

	exportCore := levelCore{Core: newExportCore(provider), level: level}

	core := zapcore.NewTee(stderrCore, exportCore)
	z := zap.New(core, zap.Fields(attributesToFields(attrs)...))

	return &Logger{zap: z, provider: provider}
}

// newExportCore returns the zap core that bridges entries to provider as
// OpenTelemetry log records, under the same instrumentation scope as helix's spans
// and metrics.
func newExportCore(provider otellog.LoggerProvider) zapcore.Core {
	return otelzap.NewCore(internaltrace.ScopeName, otelzap.WithLoggerProvider(provider))
}

// NewNopLogger creates a Logger that discards all output. The provider is the
// OpenTelemetry no-op implementation rather than nil, so that Provider() never
// hands a caller a nil interface to dereference.
func NewNopLogger() *Logger {
	return &Logger{zap: zap.NewNop(), provider: lognoop.NewLoggerProvider()}
}

// DefaultLevel determines the log level from the OTEL_LOG_LEVEL environment
// variable (debug, info, warn, error). Defaults to info when unset or invalid.
//
// It is the fallback for a Service whose Config does not set a level.
// OTEL_LOG_LEVEL is, strictly, the OpenTelemetry SDK's own diagnostic level; it
// is read here for compatibility with services already relying on it.
func DefaultLevel() zapcore.Level {
	switch strings.ToLower(os.Getenv("OTEL_LOG_LEVEL")) {
	case "debug":
		return zapcore.DebugLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// attributesToFields converts a resource's attributes to Zap fields for the stderr
// production core. Dots in keys are replaced with underscores for compatibility
// with log aggregators like Elasticsearch that interpret dots as nested paths.
func attributesToFields(attrs []attribute.KeyValue) []zap.Field {
	fields := make([]zap.Field, 0, len(attrs))
	for _, kv := range attrs {
		key := strings.ReplaceAll(string(kv.Key), ".", "_")
		switch kv.Value.Type() {
		case attribute.STRING:
			fields = append(fields, zap.String(key, kv.Value.AsString()))
		case attribute.INT64:
			fields = append(fields, zap.Int64(key, kv.Value.AsInt64()))
		case attribute.FLOAT64:
			fields = append(fields, zap.Float64(key, kv.Value.AsFloat64()))
		case attribute.BOOL:
			fields = append(fields, zap.Bool(key, kv.Value.AsBool()))
		default:
			fields = append(fields, zap.String(key, kv.Value.String()))
		}
	}

	return fields
}
