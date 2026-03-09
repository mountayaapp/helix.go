package valkey

import (
	"unsafe"

	"github.com/mountayaapp/helix.go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
)

/*
Pre-computed span names to avoid allocations on every call.
*/
var attrKeyKey = attribute.Key(identifier + ".key")

/*
setKeyAttributes sets key attributes to a trace span.
*/
func setKeyAttributes(span *trace.Span, key string) {
	span.SetAttributes(attrKeyKey.String(key))
}

/*
bytesToString converts a byte slice to a string without memory allocation. The
caller must ensure the byte slice is not modified after conversion.
*/
func bytesToString(b []byte) string {
	if len(b) == 0 {
		return ""
	}

	return unsafe.String(unsafe.SliceData(b), len(b))
}
