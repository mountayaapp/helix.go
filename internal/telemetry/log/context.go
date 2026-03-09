package log

import (
	"context"
)

/*
loggerKeyIdentifier is the unique internal type for storing a Logger in context.
*/
type loggerKeyIdentifier struct{}

var loggerKey loggerKeyIdentifier

/*
ContextWithLogger returns a copy of the context with the Logger associated to it.
*/
func ContextWithLogger(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

/*
LoggerFromContext returns the Logger stored in the context, if any. Returns nil
if no Logger is found.
*/
func LoggerFromContext(ctx context.Context) *Logger {
	l, _ := ctx.Value(loggerKey).(*Logger)
	return l
}
