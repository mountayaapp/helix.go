package temporal

import (
	"context"
	"fmt"

	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/telemetry/log"
)

/*
customlogger implements the Temporal's log.Logger interface, using the Service's
logger instance via context propagation.

The context is cached at creation time since it is derived from context.Background()
and does not carry request-scoped values.
*/
type customlogger struct {
	cachedCtx context.Context
}

/*
newCustomLogger creates a customlogger with a pre-computed context from the Service,
avoiding context creation overhead on every log call.
*/
func newCustomLogger(svc *service.Service) *customlogger {
	return &customlogger{
		cachedCtx: service.Context(svc, context.Background()),
	}
}

/*
fields converts Temporal's key-value pairs to structured log fields. Temporal
passes keyvals as alternating key/value pairs (e.g., "key1", val1, "key2", val2).
*/
func (l *customlogger) fields(keyvals []any) []log.Field {
	if len(keyvals) == 0 {
		return nil
	}

	fields := make([]log.Field, 0, len(keyvals)/2)
	for i := 0; i+1 < len(keyvals); i += 2 {
		key, ok := keyvals[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", keyvals[i])
		}

		fields = append(fields, log.Any(key, keyvals[i+1]))
	}

	return fields
}

/*
Debug logs a message at the debug level.
*/
func (l *customlogger) Debug(msg string, keyvals ...any) {
	log.Debug(l.cachedCtx, msg, l.fields(keyvals)...)
}

/*
Info logs a message at the info level.
*/
func (l *customlogger) Info(msg string, keyvals ...any) {
	log.Info(l.cachedCtx, msg, l.fields(keyvals)...)
}

/*
Warn logs a message at the warn level.
*/
func (l *customlogger) Warn(msg string, keyvals ...any) {
	log.Warn(l.cachedCtx, msg, l.fields(keyvals)...)
}

/*
Error logs a message at the error level.
*/
func (l *customlogger) Error(msg string, keyvals ...any) {
	log.Error(l.cachedCtx, msg, l.fields(keyvals)...)
}
