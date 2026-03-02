package clickhouse

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
		span.SetStringAttribute(fmt.Sprintf("%s.database", identifier), cfg.Database)
	}
}

/*
normalizeErrorMessage normalizes an error returned by the ClickHouse client to
match the format of helix.go. This is only used inside Connect for a better
readability in the terminal. Otherwise, functions return native ClickHouse errors.

Example:

	"failed to connect to `host=localhost user=default database=default`: ..."

Becomes:

	"Failed to connect to `host=localhost user=default database=default`: ..."
*/
func normalizeErrorMessage(err error) string {
	var msg string = err.Error()
	runes := []rune(msg)
	runes[0] = unicode.ToUpper(runes[0])

	return string(runes)
}
