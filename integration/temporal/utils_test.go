package temporal

import (
	"errors"
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/workflow"
)

func TestWorkflowAttributes(t *testing.T) {
	info := &workflow.Info{
		TaskQueueName: "orders",
		Namespace:     "production",
		WorkflowType:  workflow.Type{Name: "process-order"},
		Attempt:       3,
	}

	assert.Equal(t, []attribute.KeyValue{
		attribute.String("temporal.worker.task_queue", "orders"),
		attribute.Int("temporal.workflow.attempt", 3),
	}, workflowAttributes(nil, info))

	assert.Empty(t, workflowAttributes(nil, nil))
}

func TestActivityAttributes(t *testing.T) {
	info := activity.Info{
		TaskQueue:    "orders",
		Namespace:    "production",
		WorkflowType: &workflow.Type{Name: "process-order"},
		ActivityType: activity.Type{Name: "charge-user"},
		Attempt:      2,
	}

	assert.Equal(t, []attribute.KeyValue{
		attribute.String("temporal.worker.task_queue", "orders"),
		attribute.Int("temporal.activity.attempt", 2),
		attribute.String("temporal.workflow.type", "process-order"),
	}, activityAttributes(nil, info))

	// An activity started outside a workflow has no workflow type.
	assert.NotContains(t, activityAttributes(nil, activity.Info{}), attribute.String("temporal.workflow.type", ""))
}

func TestPreComputedAttributeKeys(t *testing.T) {
	assert.Equal(t, attribute.Key("temporal.server.address"), attrKeyServerAddress)
	assert.Equal(t, attribute.Key("temporal.namespace"), attrKeyNamespace)
	assert.Equal(t, attribute.Key("temporal.worker.task_queue"), attrKeyTaskQueue)
	assert.Equal(t, attribute.Key("temporal.workflow.type"), attrKeyWorkflowType)
	assert.Equal(t, attribute.Key("temporal.workflow.attempt"), attrKeyWorkflowAttempt)
	assert.Equal(t, attribute.Key("temporal.activity.type"), attrKeyActivityType)
	assert.Equal(t, attribute.Key("temporal.activity.attempt"), attrKeyActivityAttempt)
}

func TestRebase(t *testing.T) {
	entries := []errorstack.Entry{
		{Message: "a", Path: []any{"config", "tls"}},
		{Message: "b", Path: []any{"config", "tls", "root_ca_pems"}},
		{Message: "c", Path: []any{"elsewhere"}},
		{Message: "d"},
	}

	assert.Equal(t, []errorstack.Entry{
		{Message: "a", Path: []any{"config", "client", "tls"}},
		{Message: "b", Path: []any{"config", "client", "tls", "root_ca_pems"}},
		{Message: "c", Path: []any{"config", "client", "elsewhere"}},
		{Message: "d", Path: []any{"config", "client"}},
	}, rebase(entries, "config", "client"))
}

func TestCheckHealth(t *testing.T) {
	t.Run("a reachable server is healthy", func(t *testing.T) {
		mockClient := mocks.NewClient(t)
		mockClient.On("CheckHealth", mock.Anything, &client.CheckHealthRequest{}).Return(&client.CheckHealthResponse{}, nil)

		assert.NoError(t, checkHealth(t.Context(), mockClient))
	})

	t.Run("an unreachable server is unhealthy", func(t *testing.T) {
		mockClient := mocks.NewClient(t)
		mockClient.On("CheckHealth", mock.Anything, &client.CheckHealthRequest{}).Return(nil, errors.New("connection refused"))

		err := checkHealth(t.Context(), mockClient)

		var e *errorstack.Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, errorstack.CodeServiceUnavailable, e.Entries[0].Extensions["code"])
	})

	t.Run("the dependency checks through its client", func(t *testing.T) {
		mockClient := mocks.NewClient(t)
		mockClient.On("CheckHealth", mock.Anything, &client.CheckHealthRequest{}).Return(&client.CheckHealthResponse{}, nil)

		assert.NoError(t, (&clientConnection{client: mockClient}).CheckHealth(t.Context()))
	})
}
