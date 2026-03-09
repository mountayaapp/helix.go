package render

import (
	"os"

	"github.com/mountayaapp/helix.go/internal/cloudprovider"

	"go.opentelemetry.io/otel/attribute"
)

/*
cp is set if the service is running in Render, nil otherwise.
*/
var cp cloudprovider.CloudProvider

/*
render holds some details about the service currently running in Render and
implements the CloudProvider interface.
*/
type render struct {
	instanceId  string
	serviceId   string
	serviceName string
	serviceType string
}

/*
init populates the cloud provider if the service is running in Render.
*/
func init() {
	cp = build()
}

/*
build populates the cloud provider if the service is running in Render. Returns
nil otherwise.
*/
func build() cloudprovider.CloudProvider {
	_, exists := os.LookupEnv("RENDER")
	if !exists {
		return nil
	}

	n := &render{
		instanceId:  os.Getenv("RENDER_INSTANCE_ID"),
		serviceId:   os.Getenv("RENDER_SERVICE_ID"),
		serviceName: os.Getenv("RENDER_SERVICE_NAME"),
		serviceType: os.Getenv("RENDER_SERVICE_TYPE"),
	}

	return n
}

/*
Get returns the cloud provider interface for Render. Returns nil if not running
in Render.
*/
func Get() cloudprovider.CloudProvider {
	return cp
}

/*
String returns the string representation of the Render cloud provider.
*/
func (r *render) String() string {
	return "render"
}

/*
Attributes returns OpenTelemetry attributes when running in Render.
*/
func (r *render) Attributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("render.instance_id", r.instanceId),
		attribute.String("render.service_id", r.serviceId),
		attribute.String("render.service_name", r.serviceName),
		attribute.String("render.service_type", r.serviceType),
		attribute.String("service.name", r.serviceId),
	}
}
