package valkey

import (
	"fmt"
	"unicode"

	"github.com/mountayaapp/helix.go/telemetry/trace"
)

/*
setKeyAttributes sets key attributes to a trace span.
*/
func setKeyAttributes(span *trace.Span, key string) {
	span.SetStringAttribute(fmt.Sprintf("%s.key", identifier), key)
}

/*
normalizeErrorMessage normalizes an error returned by the Valkey client to match
the format of helix.go. This is only used inside Connect for a better readability
in the terminal. Otherwise, functions return native Valkey errors.

Example:

	"dial tcp 127.0.0.1:6379: connect: connection refused"

Becomes:

	"Dial tcp 127.0.0.1:6379: connect: connection refused"
*/
func normalizeErrorMessage(err error) string {
	var msg string = err.Error()
	runes := []rune(msg)
	runes[0] = unicode.ToUpper(runes[0])

	return string(runes)
}
