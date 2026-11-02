package integration

import (
	"bytes"
	"unicode"
	"unicode/utf8"
)

// normalizePEM replaces literal escape sequences commonly found in environment
// variables with actual newline characters. Order matters: longest sequences first
// to avoid partial matches.
func normalizePEM(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	data = bytes.ReplaceAll(data, []byte(`\r\n`), []byte("\n"))
	data = bytes.ReplaceAll(data, []byte(`\\n`), []byte("\n"))
	data = bytes.ReplaceAll(data, []byte(`\n`), []byte("\n"))
	return data
}

// normalizePEMs returns a new slice holding each of pems normalized, or nil when
// pems is empty.
func normalizePEMs(pems [][]byte) [][]byte {
	if len(pems) == 0 {
		return nil
	}

	out := make([][]byte, len(pems))
	for i, pem := range pems {
		out[i] = normalizePEM(pem)
	}

	return out
}

// NormalizeErrorMessage capitalizes the first letter of an error message to match
// the helix.go formatting convention. Safe on empty error strings.
func NormalizeErrorMessage(err error) string {
	msg := err.Error()
	if msg == "" {
		return msg
	}

	r, size := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(r)) + msg[size:]
}
