package bucket

import (
	"github.com/mountaya/helix.go/integration"

	"go.opentelemetry.io/otel/attribute"
)

// Pre-computed attribute keys to avoid allocations on every call.
//
// Only the key varies per operation. The driver, bucket, subfolder, region and
// endpoint are fixed for the life of the connection, so they are handed to the
// Instrument once and stamped by it rather than rebuilt on every span.
var attrKeyKey = integration.Key(identifier, "key")

// instrumentAttributes returns the attributes every span of this connection
// carries, derived from the sanitized Config: the driver's name, the bucket, the
// subfolder when set, then whatever the driver adds. A credential is never one of
// them.
func instrumentAttributes(cfg *Config) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		integration.Key(identifier, "driver").String(cfg.Driver.name()),
		integration.Key(identifier, "bucket").String(cfg.Bucket),
	}

	if cfg.Subfolder != "" {
		attrs = append(attrs, integration.Key(identifier, "subfolder").String(cfg.Subfolder))
	}

	return append(attrs, cfg.Driver.attributes()...)
}

// endpointAttributes returns the attribute of a driver's endpoint, or none when it
// is empty.
func endpointAttributes(endpoint string) []attribute.KeyValue {
	if endpoint == "" {
		return nil
	}

	return []attribute.KeyValue{integration.Key(identifier, "endpoint").String(endpoint)}
}
