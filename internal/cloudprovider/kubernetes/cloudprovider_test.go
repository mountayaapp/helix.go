package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
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

func TestAttributes(t *testing.T) {
	k := &kubernetes{
		namespace: "production",
		pod:       "api-pod-xyz789",
	}

	attrs := k.Attributes()

	assert.Len(t, attrs, 3)
	assert.Equal(t, attribute.String("kubernetes.namespace", "production"), attrs[0])
	assert.Equal(t, attribute.String("kubernetes.pod", "api-pod-xyz789"), attrs[1])
	assert.Equal(t, attribute.String("service.name", "api-pod-xyz789"), attrs[2])
}

func TestAttributes_ServiceNameMatchesPod(t *testing.T) {
	k := &kubernetes{
		namespace: "staging",
		pod:       "worker-pod-001",
	}

	attrs := k.Attributes()

	assert.Len(t, attrs, 3)
	assert.Equal(t, attribute.String("service.name", "worker-pod-001"), attrs[2])
}
