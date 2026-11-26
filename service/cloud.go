package service

import (
	"os"
	"strings"

	"github.com/mountaya/helix.go/internal/cloudprovider"
	"github.com/mountaya/helix.go/internal/cloudprovider/kubernetes"
	"github.com/mountaya/helix.go/internal/cloudprovider/nomad"
	"github.com/mountaya/helix.go/internal/cloudprovider/unknown"

	"go.opentelemetry.io/otel/attribute"
)

// cloudAttributes returns the resource attributes describing where the process
// runs: a baseline service.name taken from the executable, followed by the
// attributes of the first cloud provider detected.
//
// The baseline comes first so that a provider which knows better can override it
// — attributes later in a resource win — and so that a provider which does not,
// such as Kubernetes, still leaves the service named. OTEL_SERVICE_NAME and
// OTEL_RESOURCE_ATTRIBUTES are applied after both, by New.
func cloudAttributes() []attribute.KeyValue {
	attrs := unknown.Detect().Attributes()

	for _, detect := range []func() cloudprovider.CloudProvider{kubernetes.Detect, nomad.Detect} {
		if p := detect(); p != nil {
			return append(attrs, p.Attributes()...)
		}
	}

	return attrs
}

// otelSDKDisabled reports whether OTEL_SDK_DISABLED asks for telemetry to be
// turned off, as defined by the OpenTelemetry environment variable specification.
//
// Reading it here rather than mutating the environment is deliberate: os.Setenv
// races with any concurrent os.Getenv in the process, and a library has no
// business changing global process state to talk to itself.
func otelSDKDisabled() bool {
	return strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true")
}
