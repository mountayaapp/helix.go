package log

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
)

func TestDefault(t *testing.T) {
	t.Run("FallsBackToTheGlobalProvider", func(t *testing.T) {
		withoutDefault(t)

		l := Default()
		assert.Same(t, otel.GetLoggerProvider(), l.Provider())
		assert.NotPanics(t, func() { l.Info(t.Context(), "nothing is installed") })
	})

	t.Run("ReturnsTheInstalledLogger", func(t *testing.T) {
		withoutDefault(t)

		l := NewNopLogger()
		SetDefault(l)

		assert.Same(t, l, Default())
	})
}

// withoutDefault uninstalls the default Logger for the duration of a test, and
// restores whichever was installed before it.
func withoutDefault(t *testing.T) {
	t.Helper()

	previous := defaultLogger.Swap(nil)
	t.Cleanup(func() { defaultLogger.Store(previous) })
}
