package service

import (
	"github.com/mountayaapp/helix.go/internal/cloudprovider"
	"github.com/mountayaapp/helix.go/internal/cloudprovider/kubernetes"
	"github.com/mountayaapp/helix.go/internal/cloudprovider/nomad"
	"github.com/mountayaapp/helix.go/internal/cloudprovider/render"
	"github.com/mountayaapp/helix.go/internal/cloudprovider/unknown"

	"go.opentelemetry.io/otel/attribute"
)

/*
cloud wraps an internal CloudProvider implementation, keeping cloud detection
as an internal detail of the service package.
*/
type cloud struct {
	inner cloudprovider.CloudProvider
}

func (c *cloud) name() string {
	return c.inner.String()
}

func (c *cloud) attributes() []attribute.KeyValue {
	return c.inner.Attributes()
}

/*
detectCloudProvider runs through known cloud providers and returns the first one
detected. Falls back to "unknown" if none match.
*/
func detectCloudProvider() *cloud {
	providers := []cloudprovider.CloudProvider{
		kubernetes.Get(),
		nomad.Get(),
		render.Get(),
		unknown.Get(),
	}

	for _, p := range providers {
		if p != nil {
			return &cloud{inner: p}
		}
	}

	return &cloud{inner: unknown.Get()}
}
