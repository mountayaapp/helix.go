package log

import (
	"reflect"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	otellog "go.opentelemetry.io/otel/log"
	"go.uber.org/zap"
)

var (

	// defaultLogger is the process-wide Logger installed by SetDefault, if any.
	defaultLogger atomic.Pointer[Logger]

	// fallbackLogger is the Logger Default last built over the global provider. It
	// is reused for as long as the global provider stays the same one, so that a
	// process running with no default installed does not build a zap logger and a
	// bridge core on every log call.
	fallbackLogger atomic.Pointer[Logger]
)

// SetDefault installs l as the process-wide Logger: the one the public
// telemetry/log package falls back to when a context carries none. service.New
// installs its own alongside the OpenTelemetry globals, unless Config.SkipGlobals
// says not to.
//
// It is the whole Logger that is installed, not only its provider. Unlike a
// Tracer, a Logger is more than its provider: it also writes stderr JSON at the
// configured level with the resource's attributes, and none of that can be
// rebuilt from the global LoggerProvider.
//
// It returns the Logger installed before, or nil.
func SetDefault(l *Logger) *Logger {
	return defaultLogger.Swap(l)
}

// Default returns the process-wide Logger: the one SetDefault installed or, when
// none was, a Logger over the global OpenTelemetry LoggerProvider.
//
// The fallback reads the global provider at each call rather than once, so that
// it always follows the provider installed last. Until one is, the global
// provider discards everything. It writes to that provider only: stderr output
// belongs to a Logger built by service.New.
func Default() *Logger {
	if l := defaultLogger.Load(); l != nil {
		return l
	}

	provider := otel.GetLoggerProvider()
	if l := fallbackLogger.Load(); l != nil && sameProvider(l.provider, provider) {
		return l
	}

	l := &Logger{zap: zap.New(newExportCore(provider)), provider: provider}
	fallbackLogger.Store(l)

	return l
}

// sameProvider reports whether a and b are the same provider. A provider whose
// dynamic type cannot be compared is never considered the same, rather than
// letting the comparison panic: the cost is a rebuilt fallback, not a crash.
func sameProvider(a, b otellog.LoggerProvider) bool {
	return a != nil && reflect.TypeOf(a).Comparable() && a == b
}
