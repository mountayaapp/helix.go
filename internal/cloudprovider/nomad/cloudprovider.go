package nomad

import (
	"os"

	"github.com/mountayaapp/helix.go/internal/cloudprovider"

	"go.opentelemetry.io/otel/attribute"
)

/*
cp is set if the service is running in Nomad, nil otherwise.
*/
var cp cloudprovider.CloudProvider

/*
nomad holds some details about the service currently running in Nomad and
implements the CloudProvider interface.
*/
type nomad struct {
	datacenter string
	jobId      string
	jobName    string
	namespace  string
	region     string
	task       string
}

/*
init populates the cloud provider if the service is running in Nomad.
*/
func init() {
	cp = build()
}

/*
build populates the cloud provider if the service is running in Nomad. Returns
nil otherwise.
*/
func build() cloudprovider.CloudProvider {
	_, exists := os.LookupEnv("NOMAD_JOB_ID")
	if !exists {
		return nil
	}

	n := &nomad{
		datacenter: os.Getenv("NOMAD_DC"),
		jobId:      os.Getenv("NOMAD_JOB_ID"),
		jobName:    os.Getenv("NOMAD_JOB_NAME"),
		namespace:  os.Getenv("NOMAD_NAMESPACE"),
		region:     os.Getenv("NOMAD_REGION"),
		task:       os.Getenv("NOMAD_TASK_NAME"),
	}

	return n
}

/*
Get returns the cloud provider interface for Nomad. Returns nil if not running
in Nomad.
*/
func Get() cloudprovider.CloudProvider {
	return cp
}

/*
String returns the string representation of the Nomad cloud provider.
*/
func (n *nomad) String() string {
	return "nomad"
}

/*
Attributes returns OpenTelemetry attributes when running in Nomad.
*/
func (n *nomad) Attributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("nomad.datacenter", n.datacenter),
		attribute.String("nomad.job_id", n.jobId),
		attribute.String("nomad.job_name", n.jobName),
		attribute.String("nomad.namespace", n.namespace),
		attribute.String("nomad.region", n.region),
		attribute.String("nomad.task", n.task),
		attribute.String("service.name", n.task),
	}
}
