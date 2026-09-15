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

// Client exposes an opinionated way to interact with Temporal's client capabilities.
// It wraps the Temporal client for automatic tracing and error recording.
//
// Client is a concrete type, not an interface, because its methods are generic and
// Go does not permit interface methods to declare type parameters. Its exported
// methods take a Workflow or Signal handle and infer the payload types from it, so
// callers never hand untyped values to Temporal.
type Client struct {
	client client.Client
}

// Connect creates a client-only Temporal connection and registers it as a
// dependency via service.Attach. Use this for services that need to start or
// schedule workflows without processing them.
func Connect(svc *service.Service, cfg ConfigClient) (*Client, error) {

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

	return &Client{
		client: c,
	}, nil
}

// ExecuteWorkflow starts a workflow execution and returns a WorkflowRun handle.
//
// Input and Result are inferred from wf, so input is checked against the workflow's
// declared input type at compile time and the returned run yields the workflow's
// declared result type.
//
// It automatically handles tracing and error recording via interceptor.
func (c *Client) ExecuteWorkflow[Input, Result any](
	ctx context.Context,
	wf Workflow[Input, Result],
	opts client.StartWorkflowOptions,
	input Input,
) (*WorkflowRun[Result], error) {
	run, err := c.client.ExecuteWorkflow(ctx, opts, wf.name, input)

	return &WorkflowRun[Result]{run: run}, err
}

// SignalWorkflow sends a payload to a running workflow execution. Payload is
// inferred from sig, so the value is checked against the signal's declared payload
// type at compile time. A run Id is optional; an empty string targets the latest run
// for the workflow Id.
//
// It automatically handles tracing and error recording via interceptor.
func (c *Client) SignalWorkflow[Payload any](
	ctx context.Context,
	sig Signal[Payload],
	workflowID, runID string,
	payload Payload,
) error {
	return c.client.SignalWorkflow(ctx, workflowID, runID, sig.name, payload)
}

// CreateSchedule creates a new Temporal Schedule that executes wf. If a schedule
// with the same ID already exists and has identical properties (workflow name, task
// queue, overlap policy), the call is a no-op: no error is returned and no error is
// recorded in traces. If the existing schedule differs, an error is returned.
func (c *Client) CreateSchedule[Input, Result any](
	ctx context.Context,
	wf Workflow[Input, Result],
	opts ScheduleOptions,
) error {
	return c.createSchedule(ctx, client.ScheduleOptions{
		ID: wf.name,
		Spec: client.ScheduleSpec{
			CronExpressions: opts.CronExpressions,
		},
		Action: &client.ScheduleWorkflowAction{
			ID:        wf.name,
			Workflow:  wf.name,
			TaskQueue: opts.TaskQueue,
		},
		Overlap: opts.OverlapPolicy,
	})
}

// WorkflowResultIfClosed retrieves a workflow run's result by workflowID (runID
// optional; empty targets the latest run) WITHOUT blocking on a run still in
// progress. Result is inferred from wf.
//
// The bool reports whether the run had closed: false (with the zero Result) on an
// in-progress or unknown run, true once it has closed — in which case result holds
// the deserialized Result for a completed run, or err carries a failed run's error.
// It is the non-blocking counterpart to WorkflowRun.Await.
func (c *Client) WorkflowResultIfClosed[Input, Result any](
	ctx context.Context,
	wf Workflow[Input, Result],
	workflowID, runID string,
) (Result, bool, error) {
	var output Result
	closed, err := c.closedWorkflowResult(ctx, workflowID, runID, &output)

	return output, closed, err
}

// createSchedule creates a new schedule of a workflow type. If a schedule with the
// same ID already exists and has identical properties, the error is silently ignored.
// If properties differ, the error is returned.
func (c *Client) createSchedule(ctx context.Context, opts client.ScheduleOptions) error {

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

// closedWorkflowResult deserializes the result of a workflow run that has already
// closed into valuePtr, without blocking on a run still in progress. The bool reports
// whether the run had closed: an in-progress or unknown run reports false and leaves
// valuePtr untouched, so a caller polling a long-lived run never parks a goroutine on
// it; a closed run reports true, with valuePtr holding a completed run's result or the
// returned error carrying a failed run's failure (in which case valuePtr is unreliable).
func (c *Client) closedWorkflowResult(ctx context.Context, workflowID, runID string, valuePtr any) (bool, error) {
	desc, err := c.client.DescribeWorkflowExecution(ctx, workflowID, runID)
	if err != nil {

		// An unknown or history-expired run is not an error for a best-effort terminal
		// read: report it as not-closed so the caller proceeds rather than failing.
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return false, nil
		}

		return false, err
	}

	// A still-running execution must not be read with Get, which blocks until the run
	// closes; report it as not-closed so the caller never parks a goroutine on it.
	if desc.GetWorkflowExecutionInfo().GetStatus() == enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		return false, nil
	}

	// The run has closed, so Get returns immediately: it deserializes a completed run's
	// result into valuePtr, or returns a failed or terminated run's error.
	return true, c.client.GetWorkflow(ctx, workflowID, runID).Get(ctx, valuePtr)
}

// schedulesMatch reports whether the existing schedule matches the requested
// schedule options by comparing overlap policy, workflow name, and task queue.
//
// Note: CronExpressions are not compared because the Temporal server converts them
// into StructuredCalendar entries, and Describe() never returns CronExpressions back.
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
