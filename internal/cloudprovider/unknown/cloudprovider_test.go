package unknown

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap/zapcore"
)

func TestBuild(t *testing.T) {
	provider := build()

	assert.NotNil(t, provider)
	assert.Equal(t, "unknown", provider.String())
	assert.NotEmpty(t, provider.Service())
}

func TestGet(t *testing.T) {
	provider := Get()

	assert.NotNil(t, provider)
	assert.Equal(t, "unknown", provider.String())
}

func TestString(t *testing.T) {
	u := &unknown{name: "test-service"}

	assert.Equal(t, "unknown", u.String())
}

func TestService(t *testing.T) {
	testcases := []struct {
		name        string
		serviceName string
		expected    string
	}{
		{
			name:        "returns service name",
			serviceName: "my-service",
			expected:    "my-service",
		},
		{
			name:        "returns helix service name",
			serviceName: "helix",
			expected:    "helix",
		},
		{
			name:        "returns empty when name is empty",
			serviceName: "",
			expected:    "",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			u := &unknown{name: tc.serviceName}

			assert.Equal(t, tc.expected, u.Service())
		})
	}
}

func TestLoggerFields(t *testing.T) {
	u := &unknown{name: "my-service"}

	fields := u.LoggerFields()

	assert.Len(t, fields, 1)
	assert.Equal(t, "service_name", fields[0].Key)
	assert.Equal(t, zapcore.StringType, fields[0].Type)
	assert.Equal(t, "my-service", fields[0].String)
}

func TestLoggerFields_Empty(t *testing.T) {
	u := &unknown{name: ""}

	fields := u.LoggerFields()

	assert.Len(t, fields, 1)
	assert.Equal(t, "service_name", fields[0].Key)
	assert.Equal(t, "", fields[0].String)
}

func TestTracerAttributes(t *testing.T) {
	u := &unknown{name: "my-service"}

	attrs := u.TracerAttributes()

	assert.Len(t, attrs, 1)
	assert.Equal(t, attribute.String("service.name", "my-service"), attrs[0])
}

func TestTracerAttributes_Empty(t *testing.T) {
	u := &unknown{name: ""}

	attrs := u.TracerAttributes()

	assert.Len(t, attrs, 1)
	assert.Equal(t, attribute.String("service.name", ""), attrs[0])
}
