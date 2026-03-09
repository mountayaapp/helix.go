package integration

import (
	"unicode"
	"unicode/utf8"
)

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
