package nomad

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestBuild_NoEnvVar(t *testing.T) {

	// The init() already ran without NOMAD_JOB_ID, so cp should be nil.
	assert.Nil(t, cp)
}

func TestBuild_WithEnvVars(t *testing.T) {
	t.Setenv("NOMAD_JOB_ID", "job-123")
	t.Setenv("NOMAD_DC", "dc1")
	t.Setenv("NOMAD_JOB_NAME", "my-job")
	t.Setenv("NOMAD_NAMESPACE", "default")
	t.Setenv("NOMAD_REGION", "us-east-1")
	t.Setenv("NOMAD_TASK_NAME", "web")

	provider := build()

	assert.NotNil(t, provider)
	assert.Equal(t, "nomad", provider.String())
}

func TestBuild_WithPartialEnvVars(t *testing.T) {
	t.Setenv("NOMAD_JOB_ID", "job-456")
	t.Setenv("NOMAD_DC", "")
	t.Setenv("NOMAD_JOB_NAME", "")
	t.Setenv("NOMAD_NAMESPACE", "")
	t.Setenv("NOMAD_REGION", "")
	t.Setenv("NOMAD_TASK_NAME", "")

	provider := build()

	assert.NotNil(t, provider)
	assert.Equal(t, "nomad", provider.String())
}

func TestGet(t *testing.T) {

	// In test environment without NOMAD_JOB_ID, Get() returns nil.
	provider := Get()

	assert.Nil(t, provider)
}

func TestString(t *testing.T) {
	n := &nomad{}

	assert.Equal(t, "nomad", n.String())
}

func TestAttributes(t *testing.T) {
	n := &nomad{
		datacenter: "dc1",
		jobId:      "job-123",
		jobName:    "my-job",
		namespace:  "default",
		region:     "us-east-1",
		task:       "web",
	}

	attrs := n.Attributes()

	assert.Len(t, attrs, 7)
	assert.Equal(t, attribute.String("nomad.datacenter", "dc1"), attrs[0])
	assert.Equal(t, attribute.String("nomad.job_id", "job-123"), attrs[1])
	assert.Equal(t, attribute.String("nomad.job_name", "my-job"), attrs[2])
	assert.Equal(t, attribute.String("nomad.namespace", "default"), attrs[3])
	assert.Equal(t, attribute.String("nomad.region", "us-east-1"), attrs[4])
	assert.Equal(t, attribute.String("nomad.task", "web"), attrs[5])
	assert.Equal(t, attribute.String("service.name", "web"), attrs[6])
}

func TestAttributes_ServiceNameMatchesTask(t *testing.T) {
	n := &nomad{
		task: "api-worker",
	}

	attrs := n.Attributes()

	assert.Len(t, attrs, 7)
	assert.Equal(t, attribute.String("service.name", "api-worker"), attrs[6])
}
