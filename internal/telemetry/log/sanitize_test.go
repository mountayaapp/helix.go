package log

import (
	"errors"
	"strings"
	"testing"

	"github.com/mountaya/helix.go/internal/telemetry/sanitize"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// errRefused is an error whose text quotes a value shaped like a credential, as
// a driver's or a client's may.
var errRefused = errors.New("charge refused for card tok_0123456789abcdef")

// withoutCard restates an error quoting a card token as a fixed one.
func withoutCard(err error) error {
	if strings.Contains(err.Error(), "tok_") {
		return errors.New("charge refused")
	}

	return err
}

// register registers fn for the length of the test.
func register(t *testing.T, fn func(error) error) {
	t.Helper()

	previous := sanitize.Set(fn)
	t.Cleanup(func() { sanitize.Set(previous) })
}

// observedLogger returns a Logger writing to an observer, and the observer.
func observedLogger() (*Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)

	return &Logger{zap: zap.New(core)}, logs
}

func TestSanitizeErrors(t *testing.T) {
	fields := []zap.Field{
		zap.String("operation", "charge"),
		zap.Error(errRefused),
		zap.Any("cause", errRefused),
		zap.String("reason", errRefused.Error()),
	}

	t.Run("nothing registered: the fields themselves", func(t *testing.T) {
		register(t, nil)

		got := sanitizeErrors(fields)
		require.Len(t, got, len(fields))
		assert.Same(t, &fields[0], &got[0], "the fields were copied")
	})

	t.Run("every error restated, under its key, and nothing else", func(t *testing.T) {
		register(t, withoutCard)

		got := sanitizeErrors(fields)

		assert.Equal(t, zap.String("operation", "charge"), got[0])
		assert.Equal(t, zap.NamedError("error", errors.New("charge refused")).Key, got[1].Key)
		assert.EqualError(t, got[1].Interface.(error), "charge refused")
		assert.Equal(t, "cause", got[2].Key)
		assert.EqualError(t, got[2].Interface.(error), "charge refused")
		assert.Equal(t, fields[3], got[3], "a string holding an error's text is no error, and is not handed over")

		assert.Same(t, errRefused, fields[1].Interface, "the caller's fields were changed")
	})

	t.Run("an error restated to nil is not written", func(t *testing.T) {
		register(t, func(error) error { return nil })

		got := sanitizeErrors(fields)
		assert.Equal(t, zapcore.SkipType, got[1].Type)
		assert.Equal(t, zapcore.SkipType, got[2].Type)
	})
}

// TestLogger_SanitizesEveryErrorItWrites pins that every way into a Logger hands
// its errors to the registered function: each level, and the fields With binds.
func TestLogger_SanitizesEveryErrorItWrites(t *testing.T) {
	register(t, withoutCard)

	l, logs := observedLogger()

	l.Debug(t.Context(), "debug", zap.Error(errRefused))
	l.Info(t.Context(), "info", zap.Error(errRefused))
	l.Warn(t.Context(), "warn", zap.Error(errRefused))
	l.Error(t.Context(), "error", zap.Error(errRefused))
	l.With(zap.NamedError("bound", errRefused)).Info(t.Context(), "with")

	entries := logs.All()
	require.Len(t, entries, 5)

	for _, entry := range entries {
		for key, value := range entry.ContextMap() {
			assert.NotContains(t, value, "tok_", "%q holds %s under %s", entry.Message, value, key)
		}
	}

	assert.Equal(t, "charge refused", entries[4].ContextMap()["bound"])
}
