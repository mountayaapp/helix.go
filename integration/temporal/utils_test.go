package temporal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace/noop"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"
)

func TestSetWorkflowAttributes_WithInfo(t *testing.T) {
	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(t.Context(), "test")

	info := &workflow.Info{
		TaskQueueName: "my-queue",
		Namespace:     "production",
		WorkflowType:  workflow.Type{Name: "ProcessOrder"},
		Attempt:       3,
	}

	// Should not panic with noop span.
	assert.NotPanics(t, func() {
		setWorkflowAttributes(span, info)
	})
}

func TestSetWorkflowAttributes_NilInfo(t *testing.T) {
	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(t.Context(), "test")

	// Should not panic with nil info.
	assert.NotPanics(t, func() {
		setWorkflowAttributes(span, nil)
	})
}

func TestSetActivityAttributes(t *testing.T) {
	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(t.Context(), "test")

	info := activity.Info{
		TaskQueue:         "my-queue",
		WorkflowNamespace: "production",
		WorkflowType:      &workflow.Type{Name: "ProcessOrder"},
		ActivityType:      activity.Type{Name: "DownloadImage"},
		Attempt:           2,
	}

	// Should not panic with noop span.
	assert.NotPanics(t, func() {
		setActivityAttributes(span, info)
	})
}

func TestSetActivityAttributes_ZeroValues(t *testing.T) {
	tracer := noop.NewTracerProvider().Tracer("test")
	_, span := tracer.Start(t.Context(), "test")

	info := activity.Info{
		WorkflowType: &workflow.Type{},
	}

	assert.NotPanics(t, func() {
		setActivityAttributes(span, info)
	})
}

func TestPreComputedAttributeKeys(t *testing.T) {
	assert.Equal(t, attribute.Key("temporal.worker.taskqueue"), attrKeyWorkerTaskQueue)
	assert.Equal(t, attribute.Key("temporal.workflow.namespace"), attrKeyWorkflowNamespace)
	assert.Equal(t, attribute.Key("temporal.workflow.type"), attrKeyWorkflowType)
	assert.Equal(t, attribute.Key("temporal.workflow.attempt"), attrKeyWorkflowAttempt)
	assert.Equal(t, attribute.Key("temporal.activity.type"), attrKeyActivityType)
	assert.Equal(t, attribute.Key("temporal.activity.attempt"), attrKeyActivityAttempt)
	assert.Equal(t, attribute.Key("temporal.server.address"), attrKeyServerAddress)
	assert.Equal(t, attribute.Key("temporal.namespace"), attrKeyNamespace)
}

func TestHealthCheckTimeout(t *testing.T) {
	assert.Equal(t, 5*time.Second, healthCheckTimeout)
}
