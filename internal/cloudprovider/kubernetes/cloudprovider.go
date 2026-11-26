package kubernetes

import (
	"os"
	"strings"

	"github.com/mountaya/helix.go/internal/cloudprovider"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// namespaceFile is where Kubernetes mounts the namespace of the pod's service
// account. A variable so that tests can point it elsewhere.
var namespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// kubernetes holds some details about the service currently running in Kubernetes
// and implements the CloudProvider interface.
type kubernetes struct {
	namespace string
	pod       string
}

// Detect returns the cloud provider for Kubernetes, or nil when the process is not
// running in Kubernetes.
func Detect() cloudprovider.CloudProvider {
	if _, exists := os.LookupEnv("KUBERNETES_SERVICE_HOST"); !exists {
		return nil
	}

	ns, err := os.ReadFile(namespaceFile)
	if err != nil {
		return nil
	}

	return &kubernetes{
		namespace: strings.TrimSpace(string(ns)),
		pod:       os.Getenv("HOSTNAME"),
	}
}

// Attributes returns the semantic-convention attributes of the pod.
//
// It does not set service.name. The pod name is unique to one replica and changes
// on every deploy, so using it as the service's name made every pod a service of
// its own in every backend. The name comes from OTEL_SERVICE_NAME, or the
// executable's name when that is unset.
func (k *kubernetes) Attributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		semconv.K8SNamespaceName(k.namespace),
		semconv.K8SPodName(k.pod),
	}
}
