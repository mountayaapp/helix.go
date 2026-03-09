package integration

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
			input:    errors.New("failed to connect"),
			expected: "Failed to connect",
		},
		{
			name:     "already capitalized",
			input:    errors.New("Already capitalized"),
			expected: "Already capitalized",
		},
		{
			name:     "single character",
			input:    errors.New("a"),
			expected: "A",
		},
		{
			name:     "empty string",
			input:    errors.New(""),
			expected: "",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := NormalizeErrorMessage(tc.input)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestNormalizeErrorMessage_Unicode(t *testing.T) {
	testcases := []struct {
		name     string
		input    error
		expected string
	}{
		{
			name:     "German umlaut lowercase",
			input:    errors.New("über cool"),
			expected: "Über cool",
		},
		{
			name:     "Japanese characters",
			input:    errors.New("日本語のエラー"),
			expected: "日本語のエラー",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := NormalizeErrorMessage(tc.input)

			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestNormalizeErrorMessage_MultibyteFirstRune(t *testing.T) {
	testcases := []struct {
		name     string
		input    error
		expected string
	}{
		{
			name:     "multi-byte lowercase first rune",
			input:    errors.New("ñoño"),
			expected: "Ñoño",
		},
		{
			name:     "Greek lowercase first rune",
			input:    errors.New("ελληνικά"),
			expected: "Ελληνικά",
		},
		{
			name:     "Cyrillic lowercase first rune",
			input:    errors.New("ошибка"),
			expected: "Ошибка",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			actual := NormalizeErrorMessage(tc.input)

			assert.Equal(t, tc.expected, actual)
		})
	}
}
