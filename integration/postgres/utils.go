package postgres

import (
	"github.com/mountayaapp/helix.go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
)

/*
Pre-computed span names to avoid allocations on every call.
*/
var (
	attrKeyDatabase = attribute.Key(identifier + ".database")
	attrKeyQuery    = attribute.Key(identifier + ".query")
	attrKeyTxQuery  = attribute.Key(identifier + ".transaction.query")
)

/*
setDefaultAttributes sets integration attributes to a trace span.
*/
func setDefaultAttributes(span *trace.Span, cfg *Config) {
	if cfg != nil {
		span.SetAttributes(attrKeyDatabase.String(cfg.Database))
	}
}

/*
setQueryAttributes sets SQL query attributes to a trace span.
*/
func setQueryAttributes(span *trace.Span, query string) {
	span.SetAttributes(attrKeyQuery.String(query))
}

/*
setTransactionQueryAttributes sets SQL query attributes of a transaction to trace
span.
*/
func setTransactionQueryAttributes(span *trace.Span, query string) {
	span.SetAttributes(attrKeyTxQuery.String(query))
}
