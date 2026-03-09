package bucket

import (
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
)

/*
Pre-computed span names to avoid allocations on every call.
*/
var (
	attrKeyDriver    = attribute.Key(identifier + ".driver")
	attrKeyBucket    = attribute.Key(identifier + ".bucket")
	attrKeySubfolder = attribute.Key(identifier + ".subfolder")
	attrKeyKey       = attribute.Key(identifier + ".key")
)

/*
setAttributes sets integration and key attributes to a trace span in a single
call, reducing per-operation overhead.
*/
func setAttributes(span *trace.Span, cfg *Config, key string) {
	if cfg == nil {
		span.SetAttributes(attrKeyKey.String(key))
		return
	}

	if cfg.Subfolder != "" {
		span.SetAttributes(
			attrKeyDriver.String(cfg.Driver.string()),
			attrKeyBucket.String(cfg.Bucket),
			attrKeySubfolder.String(cfg.Subfolder),
			attrKeyKey.String(key),
		)
	} else {
		span.SetAttributes(
			attrKeyDriver.String(cfg.Driver.string()),
			attrKeyBucket.String(cfg.Bucket),
			attrKeyKey.String(key),
		)
	}
}
