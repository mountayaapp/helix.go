package valkey

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestPreComputedAttributeKeys(t *testing.T) {
	assert.Equal(t, attribute.Key("valkey.key"), attrKeyKey)
	assert.Equal(t, attribute.Key("valkey.keys"), attrKeyKeys)
	assert.Equal(t, attribute.Key("valkey.pattern"), attrKeyPattern)
	assert.Equal(t, attribute.Key("valkey.channel"), attrKeyChannel)
	assert.Equal(t, attribute.Key("valkey.command"), attrKeyCommand)
}

// TestMilliseconds pins the rounding every TTL goes through. It used to be sent
// in whole seconds, truncated, so that anything under a second became EX 0 — which
// the server rejects — and 1.9s became 1s.
func TestMilliseconds(t *testing.T) {
	testcases := []struct {
		name     string
		ttl      time.Duration
		expected int64
	}{
		{
			name:     "zero stays zero",
			ttl:      0,
			expected: 0,
		},
		{
			name:     "below a millisecond rounds up to one",
			ttl:      time.Microsecond,
			expected: 1,
		},
		{
			name:     "below a second is kept",
			ttl:      500 * time.Millisecond,
			expected: 500,
		},
		{
			name:     "a fraction of a millisecond rounds up",
			ttl:      1500*time.Millisecond + time.Nanosecond,
			expected: 1501,
		},
		{
			name:     "whole milliseconds are exact",
			ttl:      time.Minute,
			expected: 60_000,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, milliseconds(tc.ttl))
		})
	}
}
