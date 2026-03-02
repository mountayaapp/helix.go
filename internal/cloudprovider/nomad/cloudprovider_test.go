package nomad

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap/zapcore"
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
	assert.Equal(t, "my-job", provider.Service())
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
	assert.Equal(t, "", provider.Service())
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

func TestService(t *testing.T) {
	testcases := []struct {
		name     string
		jobName  string
		expected string
	}{
		{
			name:     "returns job name as service",
			jobName:  "my-job",
			expected: "my-job",
		},
		{
			name:     "returns empty when job name is empty",
			jobName:  "",
			expected: "",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			n := &nomad{jobName: tc.jobName}

			assert.Equal(t, tc.expected, n.Service())
		})
	}
}

func TestLoggerFields(t *testing.T) {
	n := &nomad{
		datacenter: "dc1",
		jobId:      "job-123",
		jobName:    "my-job",
		namespace:  "default",
		region:     "us-east-1",
		task:       "web",
	}

	fields := n.LoggerFields()

	assert.Len(t, fields, 6)
	assert.Equal(t, "nomad_datacenter", fields[0].Key)
	assert.Equal(t, zapcore.StringType, fields[0].Type)
	assert.Equal(t, "dc1", fields[0].String)
	assert.Equal(t, "nomad_job_id", fields[1].Key)
	assert.Equal(t, "job-123", fields[1].String)
	assert.Equal(t, "nomad_job_name", fields[2].Key)
	assert.Equal(t, "my-job", fields[2].String)
	assert.Equal(t, "nomad_namespace", fields[3].Key)
	assert.Equal(t, "default", fields[3].String)
	assert.Equal(t, "nomad_region", fields[4].Key)
	assert.Equal(t, "us-east-1", fields[4].String)
	assert.Equal(t, "nomad_task", fields[5].Key)
	assert.Equal(t, "web", fields[5].String)
}

func TestLoggerFields_Empty(t *testing.T) {
	n := &nomad{}

	fields := n.LoggerFields()

	assert.Len(t, fields, 6)
	for _, f := range fields {
		assert.Equal(t, zapcore.StringType, f.Type)
		assert.Equal(t, "", f.String)
	}
}

func TestTracerAttributes(t *testing.T) {
	n := &nomad{
		datacenter: "dc1",
		jobId:      "job-123",
		jobName:    "my-job",
		namespace:  "default",
		region:     "us-east-1",
		task:       "web",
	}

	attrs := n.TracerAttributes()

	assert.Len(t, attrs, 7)
	assert.Equal(t, attribute.String("nomad.datacenter", "dc1"), attrs[0])
	assert.Equal(t, attribute.String("nomad.job_id", "job-123"), attrs[1])
	assert.Equal(t, attribute.String("nomad.job_name", "my-job"), attrs[2])
	assert.Equal(t, attribute.String("nomad.namespace", "default"), attrs[3])
	assert.Equal(t, attribute.String("nomad.region", "us-east-1"), attrs[4])
	assert.Equal(t, attribute.String("nomad.task", "web"), attrs[5])
	assert.Equal(t, attribute.String("service.name", "web"), attrs[6])
}

func TestTracerAttributes_ServiceNameMatchesTask(t *testing.T) {
	n := &nomad{
		task: "api-worker",
	}

	attrs := n.TracerAttributes()

	assert.Len(t, attrs, 7)
	assert.Equal(t, attribute.String("service.name", "api-worker"), attrs[6])
}
