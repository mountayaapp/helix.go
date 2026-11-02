package trace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
)

func TestDefault(t *testing.T) {
	t.Run("FallsBackToTheGlobalProvider", func(t *testing.T) {
		withoutDefault(t)

		assert.Same(t, otel.GetTracerProvider(), Default().Provider())
	})

	t.Run("ReturnsTheInstalledTracer", func(t *testing.T) {
		withoutDefault(t)

		tr := NewNopTracer()
		SetDefault(tr)

		assert.Same(t, tr, Default())
	})
}

// withoutDefault uninstalls the default Tracer for the duration of a test, and
// restores whichever was installed before it.
func withoutDefault(t *testing.T) {
	t.Helper()

	previous := defaultTracer.Swap(nil)
	t.Cleanup(func() { defaultTracer.Store(previous) })
}
