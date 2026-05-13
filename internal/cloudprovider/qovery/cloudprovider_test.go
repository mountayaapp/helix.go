package qovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestBuild_NoEnvVar(t *testing.T) {

	// The init() already ran without QOVERY_APPLICATION_ID, so cp should be nil.
	assert.Nil(t, cp)
}

func TestBuild_WithEnvVars(t *testing.T) {
	t.Setenv("QOVERY_APPLICATION_ID", "app-123")
	t.Setenv("QOVERY_CLOUD_PROVIDER_REGION", "eu-west-3")
	t.Setenv("QOVERY_ENVIRONMENT_ID", "env-456")
	t.Setenv("QOVERY_ENVIRONMENT_NAME", "production")
	t.Setenv("QOVERY_ENVIRONMENT_TYPE", "PRODUCTION")
	t.Setenv("QOVERY_KUBERNETES_CLUSTER_DOMAIN", "cluster.qovery.com")
	t.Setenv("QOVERY_KUBERNETES_CLUSTER_NAME", "prod-cluster")
	t.Setenv("QOVERY_KUBERNETES_NAMESPACE_NAME", "z1234567-app")
	t.Setenv("QOVERY_PROJECT_ID", "proj-789")

	provider := build()

	assert.NotNil(t, provider)
	assert.Equal(t, "qovery", provider.String())
}

func TestBuild_WithPartialEnvVars(t *testing.T) {
	t.Setenv("QOVERY_APPLICATION_ID", "app-456")
	t.Setenv("QOVERY_CLOUD_PROVIDER_REGION", "")
	t.Setenv("QOVERY_ENVIRONMENT_ID", "")
	t.Setenv("QOVERY_ENVIRONMENT_NAME", "")
	t.Setenv("QOVERY_ENVIRONMENT_TYPE", "")
	t.Setenv("QOVERY_KUBERNETES_CLUSTER_DOMAIN", "")
	t.Setenv("QOVERY_KUBERNETES_CLUSTER_NAME", "")
	t.Setenv("QOVERY_KUBERNETES_NAMESPACE_NAME", "")
	t.Setenv("QOVERY_PROJECT_ID", "")

	provider := build()

	assert.NotNil(t, provider)
	assert.Equal(t, "qovery", provider.String())
}

func TestGet(t *testing.T) {

	// In test environment without QOVERY_APPLICATION_ID, Get() returns nil.
	provider := Get()

	assert.Nil(t, provider)
}

func TestString(t *testing.T) {
	q := &qovery{}

	assert.Equal(t, "qovery", q.String())
}

func TestAttributes(t *testing.T) {
	q := &qovery{
		applicationId:           "app-123",
		cloudProviderRegion:     "eu-west-3",
		environmentId:           "env-456",
		environmentName:         "production",
		environmentType:         "PRODUCTION",
		kubernetesClusterDomain: "cluster.qovery.com",
		kubernetesClusterName:   "prod-cluster",
		kubernetesNamespaceName: "z1234567-app",
		projectId:               "proj-789",
	}

	attrs := q.Attributes()

	assert.Len(t, attrs, 10)
	assert.Equal(t, attribute.String("qovery.application_id", "app-123"), attrs[0])
	assert.Equal(t, attribute.String("qovery.cloud_provider_region", "eu-west-3"), attrs[1])
	assert.Equal(t, attribute.String("qovery.environment_id", "env-456"), attrs[2])
	assert.Equal(t, attribute.String("qovery.environment_name", "production"), attrs[3])
	assert.Equal(t, attribute.String("qovery.environment_type", "PRODUCTION"), attrs[4])
	assert.Equal(t, attribute.String("qovery.kubernetes_cluster_domain", "cluster.qovery.com"), attrs[5])
	assert.Equal(t, attribute.String("qovery.kubernetes_cluster_name", "prod-cluster"), attrs[6])
	assert.Equal(t, attribute.String("qovery.kubernetes_namespace_name", "z1234567-app"), attrs[7])
	assert.Equal(t, attribute.String("qovery.project_id", "proj-789"), attrs[8])
	assert.Equal(t, attribute.String("service.name", "app-123"), attrs[9])
}

func TestAttributes_ServiceNameMatchesApplicationId(t *testing.T) {
	q := &qovery{
		applicationId: "app-custom",
	}

	attrs := q.Attributes()

	assert.Len(t, attrs, 10)
	assert.Equal(t, attribute.String("service.name", "app-custom"), attrs[9])
}
