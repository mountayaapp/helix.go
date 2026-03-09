package temporal

import (
	"context"
	"errors"

	"github.com/mountayaapp/helix.go/service"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
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
func Connect(svc *service.Service, cfg ConfigClient) (Client, error) {

	// No need to continue if ConfigClient is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, err
	}

	// Dial the Temporal server.
	c, err := dialClient(svc, &cfg)
	if err != nil {
		return nil, err
	}

	cc := &clientConnection{
		client: c,
	}

	// Register the client-only connection as a dependency.
	if err := service.Attach(svc, cc); err != nil {
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
createSchedule creates a new schedule of a workflow type. If a schedule with the
same ID already exists and has identical properties, the error is silently ignored.
If properties differ, the error is returned.
*/
func (c *iclient) createSchedule(ctx context.Context, opts client.ScheduleOptions) error {

	// First check if a schedule with this ID already exists. If it does and the
	// properties match, skip creation entirely to avoid the tracing interceptor
	// recording an error on the OTEL span.
	handle := c.client.ScheduleClient().GetHandle(ctx, opts.ID)
	desc, descErr := handle.Describe(ctx)
	if descErr == nil {
		if schedulesMatch(desc, opts) {
			return nil
		}

		return temporal.ErrScheduleAlreadyRunning
	}

	// If Describe failed for a reason other than "not found", something is wrong.
	var notFound *serviceerror.NotFound
	if !errors.As(descErr, &notFound) {
		return descErr
	}

	// Schedule doesn't exist yet, create it.
	_, err := c.client.ScheduleClient().Create(ctx, opts)

	return err
}

/*
schedulesMatch reports whether the existing schedule matches the requested
schedule options by comparing overlap policy, workflow name, and task queue.

Note: CronExpressions are not compared because the Temporal server converts them
into StructuredCalendar entries, and Describe() never returns CronExpressions back.
*/
func schedulesMatch(desc *client.ScheduleDescription, opts client.ScheduleOptions) bool {

	// Compare overlap policy. The server normalizes UNSPECIFIED (0) to SKIP (1),
	// so treat UNSPECIFIED as SKIP for comparison purposes.
	existingOverlap := enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
	if desc.Schedule.Policy != nil && desc.Schedule.Policy.Overlap != enumspb.SCHEDULE_OVERLAP_POLICY_UNSPECIFIED {
		existingOverlap = desc.Schedule.Policy.Overlap
	}

	requestedOverlap := opts.Overlap
	if requestedOverlap == enumspb.SCHEDULE_OVERLAP_POLICY_UNSPECIFIED {
		requestedOverlap = enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
	}

	if existingOverlap != requestedOverlap {
		return false
	}

	existing, ok := desc.Schedule.Action.(*client.ScheduleWorkflowAction)
	if !ok {
		return false
	}

	requested, ok := opts.Action.(*client.ScheduleWorkflowAction)
	if !ok {
		return false
	}

	existingWorkflow, _ := existing.Workflow.(string)
	requestedWorkflow, _ := requested.Workflow.(string)
	if existingWorkflow != requestedWorkflow {
		return false
	}

	if existing.TaskQueue != requested.TaskQueue {
		return false
	}

	return true
}
