package sanitize

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// register registers fn for the length of the test.
func register(t *testing.T, fn func(error) error) {
	t.Helper()

	previous := Set(fn)
	t.Cleanup(func() { Set(previous) })
}

func TestSet(t *testing.T) {
	register(t, nil)

	assert.False(t, Registered())

	restate := func(error) error { return errors.New("restated") }
	assert.Nil(t, Set(restate), "nothing was registered before")
	assert.True(t, Registered())

	previous := Set(nil)
	require.NotNil(t, previous, "the function registered before is returned")
	assert.EqualError(t, previous(errors.New("failed")), "restated")
	assert.False(t, Registered())
}

func TestError(t *testing.T) {
	failed := errors.New("failed for token tok_0123")

	t.Run("nothing registered: the error itself", func(t *testing.T) {
		register(t, nil)

		assert.Same(t, failed, Error(failed))
		assert.NoError(t, Error(nil))
	})

	t.Run("what the registered function returns, never handed nil", func(t *testing.T) {
		var handed []error
		register(t, func(err error) error {
			handed = append(handed, err)
			return errors.New("failed")
		})

		assert.EqualError(t, Error(failed), "failed")
		assert.NoError(t, Error(nil))
		assert.Equal(t, []error{failed}, handed)
	})

	t.Run("a function that panics is recovered", func(t *testing.T) {
		register(t, func(error) error { panic("sanitizer bug") })

		var got error
		require.NotPanics(t, func() { got = Error(failed) })
		assert.Same(t, ErrPanicked, got)
	})
}
