package log

import (
	"context"
)

// loggerKeyIdentifier is the unique internal type for storing a Logger in context.
type loggerKeyIdentifier struct{}

var loggerKey loggerKeyIdentifier

// NewContext returns a copy of the context with the Logger associated to it.
func NewContext(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// FromContext returns the Logger stored in the context, if any. Returns nil
// if no Logger is found.
func FromContext(ctx context.Context) *Logger {
	l, _ := ctx.Value(loggerKey).(*Logger)
	return l
}
