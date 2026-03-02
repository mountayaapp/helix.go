package temporal

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeErrorMessage(t *testing.T) {
	testcases := []struct {
		name     string
		input    error
		expected string
	}{
		{
			name:     "lowercase error message",
			input:    errors.New("dial tcp 127.0.0.1:7233: connect: connection refused"),
			expected: "Dial tcp 127.0.0.1:7233: connect: connection refused",
		},
		{
			name:     "already capitalized",
			input:    errors.New("already capitalized"),
			expected: "Already capitalized",
		},
		{
			name:     "single character",
			input:    errors.New("a"),
			expected: "A",
		},
		{
			name:     "connection timeout",
			input:    errors.New("connection timeout"),
			expected: "Connection timeout",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := normalizeErrorMessage(tc.input)

			assert.Equal(t, tc.expected, actual)
		})
	}
}
