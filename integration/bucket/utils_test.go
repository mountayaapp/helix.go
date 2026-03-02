package bucket

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
			input:    errors.New("open blob bucket: bucket not found"),
			expected: "Open blob bucket: bucket not found",
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
			name:     "connection refused",
			input:    errors.New("connection refused"),
			expected: "Connection refused",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := normalizeErrorMessage(tc.input)

			assert.Equal(t, tc.expected, actual)
		})
	}
}
