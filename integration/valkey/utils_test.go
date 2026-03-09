package valkey

import (
	"testing"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestSetKeyAttributes(t *testing.T) {
	span := trace.NewSpan(nil)

	// Should not panic with nil span internals.
	setKeyAttributes(span, "my-key")
}

func TestPreComputedAttributeKeys(t *testing.T) {
	assert.Equal(t, attribute.Key("valkey.key"), attrKeyKey)
}

func TestPreComputedSpanNames(t *testing.T) {
	assert.Equal(t, "Valkey: Exists", spanExists)
	assert.Equal(t, "Valkey: Get", spanGet)
	assert.Equal(t, "Valkey: Set", spanSet)
	assert.Equal(t, "Valkey: Increment", spanIncrement)
	assert.Equal(t, "Valkey: Decrement", spanDecrement)
	assert.Equal(t, "Valkey: Scan", spanScan)
	assert.Equal(t, "Valkey: MGet", spanMGet)
	assert.Equal(t, "Valkey: Delete", spanDelete)
}

func TestBytesToString(t *testing.T) {
	testcases := []struct {
		name     string
		input    []byte
		expected string
	}{
		{
			name:     "nil bytes returns empty string",
			input:    nil,
			expected: "",
		},
		{
			name:     "empty bytes returns empty string",
			input:    []byte{},
			expected: "",
		},
		{
			name:     "converts bytes to string",
			input:    []byte("hello world"),
			expected: "hello world",
		},
		{
			name:     "handles binary data",
			input:    []byte{0x00, 0x01, 0x02},
			expected: "\x00\x01\x02",
		},
		{
			name:     "handles unicode",
			input:    []byte("héllo wörld"),
			expected: "héllo wörld",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			result := bytesToString(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func BenchmarkSetKeyAttributes(b *testing.B) {
	span := trace.NewSpan(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		setKeyAttributes(span, "some-key")
	}
}

func BenchmarkBytesToString(b *testing.B) {
	data := []byte("some value that needs to be converted to a string for valkey")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bytesToString(data)
	}
}

func BenchmarkStringConversion(b *testing.B) {
	data := []byte("some value that needs to be converted to a string for valkey")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = string(data)
	}
}
