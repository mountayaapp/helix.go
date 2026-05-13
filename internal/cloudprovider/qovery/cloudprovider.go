package qovery

import (
	"os"

	"github.com/mountayaapp/helix.go/internal/cloudprovider"

	"go.opentelemetry.io/otel/attribute"
)

/*
cp is set if the service is running in Qovery, nil otherwise.
*/
var cp cloudprovider.CloudProvider

/*
qovery holds some details about the service currently running in Qovery and
implements the CloudProvider interface.
*/
type qovery struct {
	applicationId           string
	cloudProviderRegion     string
	environmentId           string
	environmentName         string
	environmentType         string
	kubernetesClusterDomain string
	kubernetesClusterName   string
	kubernetesNamespaceName string
	projectId               string
}

/*
init populates the cloud provider if the service is running in Qovery.
*/
func init() {
	cp = build()
}

/*
build populates the cloud provider if the service is running in Qovery. Returns
nil otherwise.
*/
func build() cloudprovider.CloudProvider {
	_, exists := os.LookupEnv("QOVERY_APPLICATION_ID")
	if !exists {
		return nil
	}

	q := &qovery{
		applicationId:           os.Getenv("QOVERY_APPLICATION_ID"),
		cloudProviderRegion:     os.Getenv("QOVERY_CLOUD_PROVIDER_REGION"),
		environmentId:           os.Getenv("QOVERY_ENVIRONMENT_ID"),
		environmentName:         os.Getenv("QOVERY_ENVIRONMENT_NAME"),
		environmentType:         os.Getenv("QOVERY_ENVIRONMENT_TYPE"),
		kubernetesClusterDomain: os.Getenv("QOVERY_KUBERNETES_CLUSTER_DOMAIN"),
		kubernetesClusterName:   os.Getenv("QOVERY_KUBERNETES_CLUSTER_NAME"),
		kubernetesNamespaceName: os.Getenv("QOVERY_KUBERNETES_NAMESPACE_NAME"),
		projectId:               os.Getenv("QOVERY_PROJECT_ID"),
	}

	return q
}

/*
Get returns the cloud provider interface for Qovery. Returns nil if not running
in Qovery.
*/
func Get() cloudprovider.CloudProvider {
	return cp
}

/*
String returns the string representation of the Qovery cloud provider.
*/
func (q *qovery) String() string {
	return "qovery"
}

/*
Attributes returns OpenTelemetry attributes when running in Qovery.
*/
func (q *qovery) Attributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("qovery.application_id", q.applicationId),
		attribute.String("qovery.cloud_provider_region", q.cloudProviderRegion),
		attribute.String("qovery.environment_id", q.environmentId),
		attribute.String("qovery.environment_name", q.environmentName),
		attribute.String("qovery.environment_type", q.environmentType),
		attribute.String("qovery.kubernetes_cluster_domain", q.kubernetesClusterDomain),
		attribute.String("qovery.kubernetes_cluster_name", q.kubernetesClusterName),
		attribute.String("qovery.kubernetes_namespace_name", q.kubernetesNamespaceName),
		attribute.String("qovery.project_id", q.projectId),
		attribute.String("service.name", q.applicationId),
	}
}
