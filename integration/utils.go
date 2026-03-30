package integration

import (
	"bytes"
	"unicode"
	"unicode/utf8"
)

/*
normalizePEM replaces literal escape sequences commonly found in environment
variables with actual newline characters. Order matters: longest sequences first
to avoid partial matches.
*/
func normalizePEM(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	data = bytes.ReplaceAll(data, []byte(`\r\n`), []byte("\n"))
	data = bytes.ReplaceAll(data, []byte(`\\n`), []byte("\n"))
	data = bytes.ReplaceAll(data, []byte(`\n`), []byte("\n"))
	return data
}

/*
NormalizeErrorMessage capitalizes the first letter of an error message to match
the helix.go formatting convention. Safe on empty error strings.
*/
func NormalizeErrorMessage(err error) string {
	msg := err.Error()
	if msg == "" {
		return msg
	}

	r, size := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(r)) + msg[size:]
}
