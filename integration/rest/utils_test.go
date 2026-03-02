package rest

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsValidUrl(t *testing.T) {
	testcases := []struct {
		name  string
		input string
		valid bool
	}{
		{
			name:  "valid HTTPS URL",
			input: "https://example.com/openapi.yaml",
			valid: true,
		},
		{
			name:  "valid HTTP localhost URL",
			input: "http://localhost:8080/openapi.yaml",
			valid: true,
		},
		{
			name:  "valid HTTPS URL with path segments",
			input: "https://api.example.com/v1/openapi.json",
			valid: true,
		},
		{
			name:  "relative path",
			input: "./descriptions/openapi.yaml",
			valid: false,
		},
		{
			name:  "absolute path",
			input: "/absolute/path/openapi.yaml",
			valid: false,
		},
		{
			name:  "filename only",
			input: "openapi.yaml",
			valid: false,
		},
		{
			name:  "empty string",
			input: "",
			valid: false,
		},
		{
			name:  "not a URL",
			input: "not a url at all",
			valid: false,
		},
		{
			name:  "valid FTP URL",
			input: "ftp://files.example.com/openapi.yaml",
			valid: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			u, ok := isValidUrl(tc.input)

			assert.Equal(t, tc.valid, ok)
			if tc.valid {
				assert.NotNil(t, u)
			} else {
				assert.Nil(t, u)
			}
		})
	}
}
