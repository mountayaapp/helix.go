package trace

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTracerContext(t *testing.T) {
	t.Run("RoundTrip", func(t *testing.T) {
		tr := NewNopTracer()
		ctx := ContextWithTracer(t.Context(), tr)

		got := TracerFromContext(ctx)
		assert.Equal(t, tr, got)
	})

	t.Run("NoTracer", func(t *testing.T) {
		got := TracerFromContext(t.Context())
		assert.Nil(t, got)
	})

	t.Run("OverwritesPrevious", func(t *testing.T) {
		tr1 := NewNopTracer()
		tr2 := NewNopTracer()

		ctx := ContextWithTracer(t.Context(), tr1)
		ctx = ContextWithTracer(ctx, tr2)

		got := TracerFromContext(ctx)
		assert.Same(t, tr2, got)
		assert.NotSame(t, tr1, got)
	})
}
