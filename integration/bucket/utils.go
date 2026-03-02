package bucket

import (
	"fmt"
	"unicode"

	"github.com/mountayaapp/helix.go/telemetry/trace"
)

/*
setDefaultAttributes sets integration attributes to a trace span.
*/
func setDefaultAttributes(span *trace.Span, cfg *Config) {
	if cfg != nil {
		span.SetStringAttribute(fmt.Sprintf("%s.driver", identifier), cfg.Driver.string())
		span.SetStringAttribute(fmt.Sprintf("%s.bucket", identifier), cfg.Bucket)

		if cfg.Subfolder != "" {
			span.SetStringAttribute(fmt.Sprintf("%s.subfolder", identifier), cfg.Subfolder)
		}
	}
}

/*
setKeyAttributes sets blob's key attributes to a trace span.
*/
func setKeyAttributes(span *trace.Span, key string) {
	span.SetStringAttribute(fmt.Sprintf("%s.key", identifier), key)
}

/*
normalizeErrorMessage normalizes an error returned by the Bucket client to match
the format of helix.go. This is only used inside Connect for a better readability
in the terminal. Otherwise, functions return native Bucket errors.

Example:

	"open blob bucket: ..."

Becomes:

	"Open blob bucket: ..."
*/
func normalizeErrorMessage(err error) string {
	var msg string = err.Error()
	runes := []rune(msg)
	runes[0] = unicode.ToUpper(runes[0])

	return string(runes)
}
