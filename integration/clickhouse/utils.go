package clickhouse

import (
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
)

/*
Pre-computed span names to avoid allocations on every call.
*/
var attrKeyDatabase = attribute.Key(identifier + ".database")

/*
setDefaultAttributes sets integration attributes to a trace span.
*/
func setDefaultAttributes(span *trace.Span, cfg *Config) {
	if cfg != nil {
		span.SetAttributes(attrKeyDatabase.String(cfg.Database))
	}
}
