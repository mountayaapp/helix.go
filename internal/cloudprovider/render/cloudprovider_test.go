package render

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap/zapcore"
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
	assert.Equal(t, "srv-123", provider.Service())
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
	assert.Equal(t, "", provider.Service())
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

func TestService(t *testing.T) {
	testcases := []struct {
		name      string
		serviceId string
		expected  string
	}{
		{
			name:      "returns service ID",
			serviceId: "srv-123",
			expected:  "srv-123",
		},
		{
			name:      "returns empty when service ID is empty",
			serviceId: "",
			expected:  "",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := &render{serviceId: tc.serviceId}

			assert.Equal(t, tc.expected, r.Service())
		})
	}
}

func TestLoggerFields(t *testing.T) {
	r := &render{
		instanceId:  "inst-abc",
		serviceId:   "srv-123",
		serviceName: "my-api",
		serviceType: "web",
	}

	fields := r.LoggerFields()

	assert.Len(t, fields, 4)
	assert.Equal(t, "render_instance_id", fields[0].Key)
	assert.Equal(t, zapcore.StringType, fields[0].Type)
	assert.Equal(t, "inst-abc", fields[0].String)
	assert.Equal(t, "render_service_id", fields[1].Key)
	assert.Equal(t, "srv-123", fields[1].String)
	assert.Equal(t, "render_service_name", fields[2].Key)
	assert.Equal(t, "my-api", fields[2].String)
	assert.Equal(t, "render_service_type", fields[3].Key)
	assert.Equal(t, "web", fields[3].String)
}

func TestLoggerFields_Empty(t *testing.T) {
	r := &render{}

	fields := r.LoggerFields()

	assert.Len(t, fields, 4)
	for _, f := range fields {
		assert.Equal(t, zapcore.StringType, f.Type)
		assert.Equal(t, "", f.String)
	}
}

func TestTracerAttributes(t *testing.T) {
	r := &render{
		instanceId:  "inst-abc",
		serviceId:   "srv-123",
		serviceName: "my-api",
		serviceType: "web",
	}

	attrs := r.TracerAttributes()

	assert.Len(t, attrs, 5)
	assert.Equal(t, attribute.String("render.instance_id", "inst-abc"), attrs[0])
	assert.Equal(t, attribute.String("render.service_id", "srv-123"), attrs[1])
	assert.Equal(t, attribute.String("render.service_name", "my-api"), attrs[2])
	assert.Equal(t, attribute.String("render.service_type", "web"), attrs[3])
	assert.Equal(t, attribute.String("service.name", "srv-123"), attrs[4])
}

func TestTracerAttributes_ServiceNameMatchesServiceId(t *testing.T) {
	r := &render{
		serviceId: "srv-custom",
	}

	attrs := r.TracerAttributes()

	assert.Len(t, attrs, 5)
	assert.Equal(t, attribute.String("service.name", "srv-custom"), attrs[4])
}
