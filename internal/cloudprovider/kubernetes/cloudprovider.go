package kubernetes

import (
	"os"

	"github.com/mountayaapp/helix.go/internal/cloudprovider"

	"go.opentelemetry.io/otel/attribute"
)

/*
cp is set if the service is running in Kubernetes, nil otherwise.
*/
var cp cloudprovider.CloudProvider

/*
kubernetes holds some details about the service currently running in Kubernetes
and implements the CloudProvider interface.
*/
type kubernetes struct {
	namespace string
	pod       string
}

/*
init populates the cloud provider if the service is running in Kubernetes.
*/
func init() {
	cp = build()
}

/*
build populates the cloud provider if the service is running in Kubernetes.
Returns nil otherwise.
*/
func build() cloudprovider.CloudProvider {
	_, exists := os.LookupEnv("KUBERNETES_SERVICE_HOST")
	if !exists {
		return nil
	}

	ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return nil
	}

	k := &kubernetes{
		namespace: string(ns),
		pod:       os.Getenv("HOSTNAME"),
	}

	return k
}

/*
Get returns the cloud provider interface for Kubernetes. Returns nil if not
running in Kubernetes.
*/
func Get() cloudprovider.CloudProvider {
	return cp
}

/*
String returns the string representation of the Kubernetes cloud provider.
*/
func (k *kubernetes) String() string {
	return "kubernetes"
}

/*
Attributes returns OpenTelemetry attributes when running in Kubernetes.
*/
func (k *kubernetes) Attributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("kubernetes.namespace", k.namespace),
		attribute.String("kubernetes.pod", k.pod),
		attribute.String("service.name", k.pod),
	}
}
