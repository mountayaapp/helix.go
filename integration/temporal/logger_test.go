package temporal

import (
	"testing"

	"github.com/mountayaapp/helix.go/telemetry/log"

	"github.com/stretchr/testify/assert"
)

func TestCustomLogger_Fields_EvenKeyvals(t *testing.T) {
	l := &customlogger{}

	fields := l.fields([]any{"key1", "value1", "key2", 42})

	assert.Len(t, fields, 2)
	assert.Equal(t, log.Any("key1", "value1"), fields[0])
	assert.Equal(t, log.Any("key2", 42), fields[1])
}

func TestCustomLogger_Fields_OddKeyvals(t *testing.T) {
	l := &customlogger{}

	// Odd number of keyvals: the trailing element without a pair is ignored.
	fields := l.fields([]any{"key1", "value1", "orphan"})

	assert.Len(t, fields, 1)
	assert.Equal(t, log.Any("key1", "value1"), fields[0])
}

func TestCustomLogger_Fields_Empty(t *testing.T) {
	l := &customlogger{}

	fields := l.fields([]any{})

	assert.Nil(t, fields)
}

func TestCustomLogger_Fields_Nil(t *testing.T) {
	l := &customlogger{}

	fields := l.fields(nil)

	assert.Nil(t, fields)
}

func TestCustomLogger_Fields_NonStringKey(t *testing.T) {
	l := &customlogger{}

	// Non-string keys are formatted via fmt.Sprintf.
	fields := l.fields([]any{123, "value"})

	assert.Len(t, fields, 1)
	assert.Equal(t, log.Any("123", "value"), fields[0])
}

func TestCustomLogger_Fields_MixedTypes(t *testing.T) {
	l := &customlogger{}

	fields := l.fields([]any{
		"string_key", "string_value",
		"int_key", 42,
		"bool_key", true,
		"nil_key", nil,
	})

	assert.Len(t, fields, 4)
	assert.Equal(t, log.Any("string_key", "string_value"), fields[0])
	assert.Equal(t, log.Any("int_key", 42), fields[1])
	assert.Equal(t, log.Any("bool_key", true), fields[2])
	assert.Equal(t, log.Any("nil_key", nil), fields[3])
}

func TestCustomLogger_Fields_SinglePair(t *testing.T) {
	l := &customlogger{}

	fields := l.fields([]any{"only_key", "only_value"})

	assert.Len(t, fields, 1)
	assert.Equal(t, log.Any("only_key", "only_value"), fields[0])
}

func TestCustomLogger_LogMethods_DoNotPanic(t *testing.T) {
	// customlogger with a nil cachedCtx will cause a no-op in the log functions
	// since no logger is attached. This verifies the methods don't panic.
	l := &customlogger{
		cachedCtx: t.Context(),
	}

	assert.NotPanics(t, func() {
		l.Debug("debug message", "key", "value")
	})

	assert.NotPanics(t, func() {
		l.Info("info message", "key", "value")
	})

	assert.NotPanics(t, func() {
		l.Warn("warn message", "key", "value")
	})

	assert.NotPanics(t, func() {
		l.Error("error message", "key", "value")
	})
}

func TestCustomLogger_LogMethods_NoKeyvals(t *testing.T) {
	l := &customlogger{
		cachedCtx: t.Context(),
	}

	assert.NotPanics(t, func() {
		l.Debug("no keyvals")
		l.Info("no keyvals")
		l.Warn("no keyvals")
		l.Error("no keyvals")
	})
}
