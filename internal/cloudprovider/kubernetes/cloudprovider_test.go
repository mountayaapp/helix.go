package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap/zapcore"
)

func TestBuild_NoEnvVar(t *testing.T) {

	// The init() already ran without KUBERNETES_SERVICE_HOST, so cp should be nil.
	assert.Nil(t, cp)
}

func TestBuild_WithEnvButNoNamespaceFile(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")

	// build() should return nil because the namespace file doesn't exist in test
	// environments.
	provider := build()

	assert.Nil(t, provider)
}

func TestGet(t *testing.T) {

	// In test environment without KUBERNETES_SERVICE_HOST, Get() returns nil.
	provider := Get()

	assert.Nil(t, provider)
}

func TestString(t *testing.T) {
	k := &kubernetes{}

	assert.Equal(t, "kubernetes", k.String())
}

func TestService(t *testing.T) {
	testcases := []struct {
		name     string
		pod      string
		expected string
	}{
		{
			name:     "returns pod name as service",
			pod:      "my-pod-abc123",
			expected: "my-pod-abc123",
		},
		{
			name:     "returns empty when pod is empty",
			pod:      "",
			expected: "",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			k := &kubernetes{pod: tc.pod}

			assert.Equal(t, tc.expected, k.Service())
		})
	}
}

func TestLoggerFields(t *testing.T) {
	k := &kubernetes{
		namespace: "production",
		pod:       "api-pod-xyz789",
	}

	fields := k.LoggerFields()

	assert.Len(t, fields, 2)
	assert.Equal(t, "kubernetes_namespace", fields[0].Key)
	assert.Equal(t, zapcore.StringType, fields[0].Type)
	assert.Equal(t, "production", fields[0].String)
	assert.Equal(t, "kubernetes_pod", fields[1].Key)
	assert.Equal(t, zapcore.StringType, fields[1].Type)
	assert.Equal(t, "api-pod-xyz789", fields[1].String)
}

func TestLoggerFields_Empty(t *testing.T) {
	k := &kubernetes{}

	fields := k.LoggerFields()

	assert.Len(t, fields, 2)
	for _, f := range fields {
		assert.Equal(t, zapcore.StringType, f.Type)
		assert.Equal(t, "", f.String)
	}
}

func TestTracerAttributes(t *testing.T) {
	k := &kubernetes{
		namespace: "production",
		pod:       "api-pod-xyz789",
	}

	attrs := k.TracerAttributes()

	assert.Len(t, attrs, 3)
	assert.Equal(t, attribute.String("kubernetes.namespace", "production"), attrs[0])
	assert.Equal(t, attribute.String("kubernetes.pod", "api-pod-xyz789"), attrs[1])
	assert.Equal(t, attribute.String("service.name", "api-pod-xyz789"), attrs[2])
}

func TestTracerAttributes_ServiceNameMatchesPod(t *testing.T) {
	k := &kubernetes{
		namespace: "staging",
		pod:       "worker-pod-001",
	}

	attrs := k.TracerAttributes()

	assert.Len(t, attrs, 3)
	assert.Equal(t, attribute.String("service.name", "worker-pod-001"), attrs[2])
}
