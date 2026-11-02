package nomad

import (
	"os"

	"github.com/mountayaapp/helix.go/internal/cloudprovider"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// nomad holds some details about the service currently running in Nomad and
// implements the CloudProvider interface.
//
// The fields come from the runtime environment Nomad sets for every task:
// https://developer.hashicorp.com/nomad/docs/reference/runtime-environment-settings.
type nomad struct {
	allocID    string
	datacenter string
	group      string
	jobID      string
	jobName    string
	namespace  string
	region     string
	task       string
}

// Detect returns the cloud provider for Nomad, or nil when the process is not
// running in Nomad.
func Detect() cloudprovider.CloudProvider {
	if _, exists := os.LookupEnv("NOMAD_JOB_ID"); !exists {
		return nil
	}

	return &nomad{
		allocID:    os.Getenv("NOMAD_ALLOC_ID"),
		datacenter: os.Getenv("NOMAD_DC"),
		group:      os.Getenv("NOMAD_GROUP_NAME"),
		jobID:      os.Getenv("NOMAD_JOB_ID"),
		jobName:    os.Getenv("NOMAD_JOB_NAME"),
		namespace:  os.Getenv("NOMAD_NAMESPACE"),
		region:     os.Getenv("NOMAD_REGION"),
		task:       os.Getenv("NOMAD_TASK_NAME"),
	}
}

// Attributes returns the attributes of the allocation. The OpenTelemetry semantic
// conventions define none for Nomad, hence the nomad namespace.
//
// It sets service.name to the task's name. Unlike a Kubernetes pod, a task is not
// one replica: its name is shared by every allocation and stays the same across
// deploys. An empty task name leaves service.name alone, so that it never replaces
// the executable's name with nothing.
func (n *nomad) Attributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("nomad.alloc_id", n.allocID),
		attribute.String("nomad.datacenter", n.datacenter),
		attribute.String("nomad.group", n.group),
		attribute.String("nomad.job_id", n.jobID),
		attribute.String("nomad.job_name", n.jobName),
		attribute.String("nomad.namespace", n.namespace),
		attribute.String("nomad.region", n.region),
		attribute.String("nomad.task", n.task),
	}

	if n.task != "" {
		attrs = append(attrs, semconv.ServiceName(n.task))
	}

	return attrs
}
