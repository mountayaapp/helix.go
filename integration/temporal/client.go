package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/service"

	"go.temporal.io/sdk/client"
)

/*
iclient is the internal client used as Temporal client. It implements the Client
interface and allows to wrap the Temporal's client functions for automatic tracing
and error recording.
*/
type iclient struct {
	config *ConfigClient
	client client.Client
}

/*
Client exposes an opinionated way to interact with Temporal's client capabilities.
*/
type Client interface {
	executeWorkflow(ctx context.Context, opts client.StartWorkflowOptions, workflowType string, payload ...any) (client.WorkflowRun, error)
	createSchedule(ctx context.Context, opts client.ScheduleOptions) error
}

/*
Connect creates a client-only Temporal connection and registers it as a
dependency via service.Attach. Use this for services that need to start or
schedule workflows without processing them.
*/
func Connect(cfg ConfigClient) (Client, error) {

	// No need to continue if ConfigClient is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, err
	}

	// Dial the Temporal server.
	c, err := dialClient(&cfg)
	if err != nil {
		return nil, err
	}

	cc := &clientConnection{
		client: c,
	}

	// Register the client-only connection as a dependency.
	if err := service.Attach(cc); err != nil {
		return nil, err
	}

	ic := &iclient{
		config: &cfg,
		client: c,
	}

	return ic, nil
}

/*
executeWorkflow starts a workflow execution and return a WorkflowRun instance and
error.

It automatically handles tracing and error recording via interceptor.
*/
func (c *iclient) executeWorkflow(ctx context.Context, opts client.StartWorkflowOptions, workflowType string, payload ...any) (client.WorkflowRun, error) {
	return c.client.ExecuteWorkflow(ctx, opts, workflowType, payload...)
}

/*
createSchedule creates a new schedule of a workflow type.
*/
func (c *iclient) createSchedule(ctx context.Context, opts client.ScheduleOptions) error {
	_, err := c.client.ScheduleClient().Create(ctx, opts)

	return err
}
