package temporal

import (
	"context"
	"fmt"

	internallog "github.com/mountayaapp/helix.go/internal/telemetry/log"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/telemetry/log"

	oteltrace "go.opentelemetry.io/otel/trace"
	sdklog "go.temporal.io/sdk/log"
)

// Ensure *customLogger complies to the Temporal Logger types it implements.
var (
	_ sdklog.Logger     = (*customLogger)(nil)
	_ sdklog.WithLogger = (*customLogger)(nil)
)

// spanContextKey is the key under which customTracer.GetLogger hands a span's
// SpanContext to the logger, as one of the key-value pairs Temporal's logging
// interface carries. It is a type of its own so that no key the SDK or an
// application logs can be mistaken for it.
type spanContextKey struct{}

// customLogger implements Temporal's log.Logger, writing through the Service's
// logger.
//
// It holds the logger rather than a context. Temporal's logging interface has no
// context parameter, and a context captured once — when the client was built —
// carried whatever span was current then onto every line the SDK logged for the
// life of the process.
//
// What correlates a line with a trace instead is the span of the workflow or
// activity it was logged for, which customTracer.GetLogger passes along. The line
// is then written with that span's context, which is how the Service's logger
// gives it the same trace_id and span_id as everything else helix logs.
type customLogger struct {
	logger *internallog.Logger

	// spanContext is the span the lines of this logger belong to, when With was
	// given one.
	spanContext oteltrace.SpanContext
}

// newCustomLogger creates a customLogger writing through the Service's logger.
func newCustomLogger(svc *service.Service) *customLogger {
	logger := internallog.FromContext(service.Context(context.Background(), svc))
	if logger == nil {
		logger = internallog.Default()
	}

	return &customLogger{
		logger: logger,
	}
}

// parse converts Temporal's key-value pairs to structured log fields, and returns
// the span context the pairs carry, falling back to the logger's own. Temporal
// passes keyvals as alternating key/value pairs (e.g., "key1", val1, "key2",
// val2); a trailing key without a value is dropped.
func (l *customLogger) parse(keyvals []any) (oteltrace.SpanContext, []log.Field) {
	sc := l.spanContext
	if len(keyvals) == 0 {
		return sc, nil
	}

	fields := make([]log.Field, 0, len(keyvals)/2)
	for i := 0; i+1 < len(keyvals); i += 2 {
		switch key := keyvals[i].(type) {
		case spanContextKey:
			if v, ok := keyvals[i+1].(oteltrace.SpanContext); ok {
				sc = v
			}
		case string:
			fields = append(fields, log.Any(key, keyvals[i+1]))
		default:
			fields = append(fields, log.Any(fmt.Sprintf("%v", key), keyvals[i+1]))
		}
	}

	return sc, fields
}

// lineContext returns the context a line is written with: one carrying sc when it
// is valid, so that the line is correlated with that span, and a bare one
// otherwise.
func lineContext(sc oteltrace.SpanContext) context.Context {
	if !sc.IsValid() {
		return context.Background()
	}

	return oteltrace.ContextWithSpanContext(context.Background(), sc)
}

// Debug logs a message at the debug level.
func (l *customLogger) Debug(msg string, keyvals ...any) {
	sc, fields := l.parse(keyvals)
	l.logger.Debug(lineContext(sc), msg, fields...)
}

// Info logs a message at the info level.
func (l *customLogger) Info(msg string, keyvals ...any) {
	sc, fields := l.parse(keyvals)
	l.logger.Info(lineContext(sc), msg, fields...)
}

// Warn logs a message at the warn level.
func (l *customLogger) Warn(msg string, keyvals ...any) {
	sc, fields := l.parse(keyvals)
	l.logger.Warn(lineContext(sc), msg, fields...)
}

// Error logs a message at the error level, but for the SDK's line for an activity
// that returned the failure the worker's interceptor returned for a panic, which
// was logged already as the panic it is (reportsRecoveredFailure).
func (l *customLogger) Error(msg string, keyvals ...any) {
	if reportsRecoveredFailure(msg, keyvals) {
		return
	}

	sc, fields := l.parse(keyvals)
	l.logger.Error(lineContext(sc), msg, fields...)
}

// With returns a child logger whose lines carry keyvals, converted once rather
// than on every line as the SDK's own fallback would.
func (l *customLogger) With(keyvals ...any) sdklog.Logger {
	sc, fields := l.parse(keyvals)

	return &customLogger{
		logger:      l.logger.With(fields...),
		spanContext: sc,
	}
}
