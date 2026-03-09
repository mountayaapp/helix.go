package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestBuild_NoEnvVar(t *testing.T) {

	// The init() already ran without RENDER, so cp should be nil.
	assert.Nil(t, cp)
}

func TestBuild_WithEnvVars(t *testing.T) {
	t.Setenv("RENDER", "true")
	t.Setenv("RENDER_INSTANCE_ID", "inst-abc")
	t.Setenv("RENDER_SERVICE_ID", "srv-123")
	t.Setenv("RENDER_SERVICE_NAME", "my-api")
	t.Setenv("RENDER_SERVICE_TYPE", "web")

	provider := build()

	assert.NotNil(t, provider)
	assert.Equal(t, "render", provider.String())
}

func TestBuild_WithPartialEnvVars(t *testing.T) {
	t.Setenv("RENDER", "true")
	t.Setenv("RENDER_INSTANCE_ID", "")
	t.Setenv("RENDER_SERVICE_ID", "")
	t.Setenv("RENDER_SERVICE_NAME", "")
	t.Setenv("RENDER_SERVICE_TYPE", "")

	provider := build()

	assert.NotNil(t, provider)
	assert.Equal(t, "render", provider.String())
}

func TestGet(t *testing.T) {

	// In test environment without RENDER, Get() returns nil.
	provider := Get()

	assert.Nil(t, provider)
}

func TestString(t *testing.T) {
	r := &render{}

	assert.Equal(t, "render", r.String())
}

func TestAttributes(t *testing.T) {
	r := &render{
		instanceId:  "inst-abc",
		serviceId:   "srv-123",
		serviceName: "my-api",
		serviceType: "web",
	}

	attrs := r.Attributes()

	assert.Len(t, attrs, 5)
	assert.Equal(t, attribute.String("render.instance_id", "inst-abc"), attrs[0])
	assert.Equal(t, attribute.String("render.service_id", "srv-123"), attrs[1])
	assert.Equal(t, attribute.String("render.service_name", "my-api"), attrs[2])
	assert.Equal(t, attribute.String("render.service_type", "web"), attrs[3])
	assert.Equal(t, attribute.String("service.name", "srv-123"), attrs[4])
}

func TestAttributes_ServiceNameMatchesServiceId(t *testing.T) {
	r := &render{
		serviceId: "srv-custom",
	}

	attrs := r.Attributes()

	assert.Len(t, attrs, 5)
	assert.Equal(t, attribute.String("service.name", "srv-custom"), attrs[4])
}
