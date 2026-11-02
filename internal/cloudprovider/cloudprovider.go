package cloudprovider

import (
	"go.opentelemetry.io/otel/attribute"
)

// CloudProvider defines the requirements each cloud provider must meet to be
// compatible with the helix.go ecosystem.
type CloudProvider interface {

	// Attributes returns OpenTelemetry attributes populated by the cloud provider.
	// Used as resource attributes for traces, metrics and logs.
	Attributes() []attribute.KeyValue
}
