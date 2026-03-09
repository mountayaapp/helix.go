package log

import (
	"context"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

/*
Logger wraps zap with structured field support, automatic trace correlation,
and OpenTelemetry log export. Created by service.New, stored in context, and
used by the public telemetry/log package-level functions.
*/
type Logger struct {
	zap      *zap.Logger
	provider *log.LoggerProvider
}

/*
Debug logs a message at the debug level with optional structured fields. It
automatically extracts trace_id and span_id from the context.
*/
func (l *Logger) Debug(ctx context.Context, msg string, fields ...zap.Field) {
	l.zap.Debug(msg, l.fieldsWithTrace(ctx, fields)...)
}

/*
Info logs a message at the info level with optional structured fields. It
automatically extracts trace_id and span_id from the context.
*/
func (l *Logger) Info(ctx context.Context, msg string, fields ...zap.Field) {
	l.zap.Info(msg, l.fieldsWithTrace(ctx, fields)...)
}

/*
Warn logs a message at the warn level with optional structured fields. It
automatically extracts trace_id and span_id from the context.
*/
func (l *Logger) Warn(ctx context.Context, msg string, fields ...zap.Field) {
	l.zap.Warn(msg, l.fieldsWithTrace(ctx, fields)...)
}

/*
Error logs a message at the error level with optional structured fields. It
automatically extracts trace_id and span_id from the context.
*/
func (l *Logger) Error(ctx context.Context, msg string, fields ...zap.Field) {
	l.zap.Error(msg, l.fieldsWithTrace(ctx, fields)...)
}

/*
With returns a child Logger that always includes the given fields.
*/
func (l *Logger) With(fields ...zap.Field) *Logger {
	return &Logger{zap: l.zap.With(fields...), provider: l.provider}
}

/*
Sync flushes any buffered log entries.
*/
func (l *Logger) Sync() error {
	return l.zap.Sync()
}

/*
Provider returns the underlying OpenTelemetry LoggerProvider. Integrations that
need to wire OpenTelemetry-native log processors use this.
*/
func (l *Logger) Provider() *log.LoggerProvider {
	return l.provider
}

/*
Shutdown gracefully shuts down the logger's OpenTelemetry provider, flushing any
pending log records.
*/
func (l *Logger) Shutdown(ctx context.Context) error {
	if l.provider == nil {
		return nil
	}

	return l.provider.Shutdown(ctx)
}

/*
fieldsWithTrace appends trace_id and span_id from the context to the given fields
slice. This enriches stderr JSON output with trace correlation. The otelzap bridge
handles correlation automatically on the OpenTelemetry export path.
*/
func (l *Logger) fieldsWithTrace(ctx context.Context, fields []zap.Field) []zap.Field {
	sc := trace.SpanContextFromContext(ctx)
	if sc.HasTraceID() {
		fields = append(fields, zap.String("trace_id", sc.TraceID().String()))
		if sc.HasSpanID() {
			fields = append(fields, zap.String("span_id", sc.SpanID().String()))
		}
	}

	return fields
}

/*
NewLogger creates a new Logger with the given level and OpenTelemetry resource.
It sets up two output paths:

  - A production Zap core that writes JSON to stderr (with resource attributes
    converted to underscore-separated field keys for log aggregator compatibility).
  - An otelzap core that exports log records to an OTLP gRPC endpoint via the
    OpenTelemetry Log SDK.
*/
func NewLogger(level zapcore.Level, res *resource.Resource) (*Logger, error) {
	ctx := context.Background()

	// Build the production core (stderr JSON output).
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.MessageKey = "message"
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder

	prodCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(os.Stderr),
		level,
	)

	// Build the OpenTelemetry log exporter via autoexport, which respects
	// OTEL_LOGS_EXPORTER and all standard OTEL_EXPORTER_OTLP_* env vars.
	exporter, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, err
	}

	provider := log.NewLoggerProvider(
		log.WithResource(res),
		log.WithProcessor(log.NewBatchProcessor(exporter)),
	)

	otelCore := otelzap.NewCore("github.com/mountayaapp/helix.go",
		otelzap.WithLoggerProvider(provider),
	)

	// Tee both cores and apply resource attributes as initial fields.
	core := zapcore.NewTee(prodCore, otelCore)
	z := zap.New(core, zap.Fields(attributesToFields(res)...))

	return &Logger{zap: z, provider: provider}, nil
}

/*
NewNopLogger creates a Logger that discards all output.
*/
func NewNopLogger() *Logger {
	return &Logger{zap: zap.NewNop()}
}

/*
DefaultLogLevel determines the log level from the OTEL_LOG_LEVEL environment
variable (debug, info, warn, error). Defaults to info when unset or invalid.
*/
func DefaultLogLevel() zapcore.Level {
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

/*
attributesToFields converts a resource's attributes to Zap fields for the stderr
production core. Dots in keys are replaced with underscores for compatibility
with log aggregators like Elasticsearch that interpret dots as nested paths.
*/
func attributesToFields(res *resource.Resource) []zap.Field {
	if res == nil {
		return nil
	}

	iter := res.Iter()
	fields := make([]zap.Field, 0, res.Len())
	for iter.Next() {
		kv := iter.Attribute()
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
			fields = append(fields, zap.String(key, kv.Value.Emit()))
		}
	}

	return fields
}
