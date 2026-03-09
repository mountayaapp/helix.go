package cloudprovider

import (
	"go.opentelemetry.io/otel/attribute"
)

/*
CloudProvider defines the requirements each cloud provider must meet to be
compatible with the helix.go ecosystem.
*/
type CloudProvider interface {

	// String returns the string representation of the cloud provider.
	//
	// Examples:
	//
	//   "kubernetes"
	//   "nomad"
	//   "render"
	//   "unknown"
	String() string

	// Attributes returns OpenTelemetry attributes populated by the cloud provider.
	// Used as resource attributes for both traces and logs.
	Attributes() []attribute.KeyValue
}
