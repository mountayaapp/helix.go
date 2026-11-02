package kubernetes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

// withNamespaceFile points detection at a namespace file holding namespace, for
// the duration of the test.
func withNamespaceFile(t *testing.T, namespace string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "namespace")
	require.NoError(t, os.WriteFile(path, []byte(namespace), 0o600))

	previous := namespaceFile
	namespaceFile = path
	t.Cleanup(func() { namespaceFile = previous })
}

func TestDetect_NoEnvVar(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	os.Unsetenv("KUBERNETES_SERVICE_HOST")

	assert.Nil(t, Detect())
}

func TestDetect_WithEnvButNoNamespaceFile(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")

	previous := namespaceFile
	namespaceFile = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { namespaceFile = previous })

	assert.Nil(t, Detect())
}

func TestDetect_InKubernetes(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("HOSTNAME", "api-7f9c8d-x2k4")
	withNamespaceFile(t, "production\n")

	provider := Detect()
	require.NotNil(t, provider)

	assert.Equal(t, []attribute.KeyValue{
		attribute.String("k8s.namespace.name", "production"),
		attribute.String("k8s.pod.name", "api-7f9c8d-x2k4"),
	}, provider.Attributes())
}

// TestAttributes_DoNotNameTheService pins that the pod — one replica — is never
// what names the service.
func TestAttributes_DoNotNameTheService(t *testing.T) {
	k := &kubernetes{namespace: "staging", pod: "worker-pod-001"}

	for _, attr := range k.Attributes() {
		assert.NotEqual(t, attribute.Key("service.name"), attr.Key)
	}
}
