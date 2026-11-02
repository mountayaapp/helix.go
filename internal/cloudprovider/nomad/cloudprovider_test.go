package nomad

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestDetect_NoEnvVar(t *testing.T) {
	t.Setenv("NOMAD_JOB_ID", "")
	os.Unsetenv("NOMAD_JOB_ID")

	assert.Nil(t, Detect())
}

func TestDetect_InNomad(t *testing.T) {
	t.Setenv("NOMAD_ALLOC_ID", "5f3b2c1a-9d8e-4f7a-b6c5-1e2d3c4b5a69")
	t.Setenv("NOMAD_DC", "dc1")
	t.Setenv("NOMAD_GROUP_NAME", "api")
	t.Setenv("NOMAD_JOB_ID", "web")
	t.Setenv("NOMAD_JOB_NAME", "web")
	t.Setenv("NOMAD_NAMESPACE", "default")
	t.Setenv("NOMAD_REGION", "global")
	t.Setenv("NOMAD_TASK_NAME", "server")

	provider := Detect()
	require.NotNil(t, provider)

	assert.Equal(t, []attribute.KeyValue{
		attribute.String("nomad.alloc_id", "5f3b2c1a-9d8e-4f7a-b6c5-1e2d3c4b5a69"),
		attribute.String("nomad.datacenter", "dc1"),
		attribute.String("nomad.group", "api"),
		attribute.String("nomad.job_id", "web"),
		attribute.String("nomad.job_name", "web"),
		attribute.String("nomad.namespace", "default"),
		attribute.String("nomad.region", "global"),
		attribute.String("nomad.task", "server"),
		attribute.String("service.name", "server"),
	}, provider.Attributes())
}

// TestAttributes_EmptyTaskLeavesServiceUnnamed pins that a missing task name
// never overrides the service's name with an empty one.
func TestAttributes_EmptyTaskLeavesServiceUnnamed(t *testing.T) {
	n := &nomad{jobID: "web"}

	for _, attr := range n.Attributes() {
		assert.NotEqual(t, attribute.Key("service.name"), attr.Key)
	}
}
