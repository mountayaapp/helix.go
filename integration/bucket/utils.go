package bucket

import (
	"github.com/mountayaapp/helix.go/integration"

	"go.opentelemetry.io/otel/attribute"
)

// Pre-computed attribute keys to avoid allocations on every call.
//
// Only the key varies per operation. The driver, bucket, subfolder, region and
// endpoint are fixed for the life of the connection, so they are handed to the
// Instrument once and stamped by it rather than rebuilt on every span.
var attrKeyKey = integration.Key(identifier, "key")

// instrumentAttributes returns the attributes every span of this connection
// carries, derived from the sanitized Config.
func instrumentAttributes(cfg *Config) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		integration.Key(identifier, "driver").String(string(cfg.Driver)),
		integration.Key(identifier, "bucket").String(cfg.Bucket),
	}

	if cfg.Subfolder != "" {
		attrs = append(attrs, integration.Key(identifier, "subfolder").String(cfg.Subfolder))
	}

	if cfg.Region != "" {
		attrs = append(attrs, integration.Key(identifier, "region").String(cfg.Region))
	}

	if cfg.Endpoint != "" {
		attrs = append(attrs, integration.Key(identifier, "endpoint").String(cfg.Endpoint))
	}

	return attrs
}
