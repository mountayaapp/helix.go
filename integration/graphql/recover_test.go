package graphql

import (
	"errors"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoverFunc(t *testing.T) {
	err := recoverFunc(t.Context(), "boom")

	var stack *errorstack.Error
	require.ErrorAs(t, err, &stack)
	assert.Equal(t, "Internal server error", stack.Entries[0].Message)
	assert.Equal(t, errorstack.CodeInternalError, stack.Entries[0].Extensions["code"])

	var panicked panicError
	require.ErrorAs(t, err, &panicked, "the panic is the cause, for the operation span to record")
	assert.Equal(t, "panic: boom", panicked.Error())
}

func TestPanicError_UnwrapsAnErrorValue(t *testing.T) {
	cause := errors.New("boom")

	assert.ErrorIs(t, panicError{value: cause}, cause)
	assert.NoError(t, panicError{value: "boom"}.Unwrap())
}
