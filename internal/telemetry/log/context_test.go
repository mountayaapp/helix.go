package log

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoggerContext(t *testing.T) {
	t.Run("RoundTrip", func(t *testing.T) {
		l := NewNopLogger()
		ctx := NewContext(t.Context(), l)

		got := FromContext(ctx)
		assert.Same(t, l, got)
	})

	t.Run("NoLogger", func(t *testing.T) {
		got := FromContext(t.Context())
		assert.Nil(t, got)
	})

	t.Run("OverwritesPrevious", func(t *testing.T) {
		l1 := NewNopLogger()
		l2 := NewNopLogger()

		ctx := NewContext(t.Context(), l1)
		ctx = NewContext(ctx, l2)

		got := FromContext(ctx)
		assert.Same(t, l2, got)
		assert.NotSame(t, l1, got)
	})
}
