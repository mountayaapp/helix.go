package temporal

import (
	"context"
	"errors"

	"github.com/mountayaapp/helix.go/service"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// Client exposes an opinionated way to interact with Temporal's client capabilities.
// It wraps the Temporal client for automatic tracing and error recording.
//
// Client is a concrete type, not an interface, because its methods are generic and
// Go does not permit interface methods to declare type parameters. Its exported
// methods take a Workflow, Signal, Query or Update handle and infer the payload
// types from it, so callers never hand untyped values to Temporal.
type Client struct {
	client client.Client

	// converter is the DataConverter the client encodes with, which reads back
	// what CreateSchedule stored in a schedule.
	converter converter.DataConverter
}

// newClient returns the Client handed to the caller for c, dialed from cfg.
func newClient(c client.Client, cfg *ConfigClient) *Client {
	return &Client{
		client:    c,
		converter: cfg.dataConverter(),
	}
}

// Connect creates a client-only Temporal connection. Use this for services that
// need to start or schedule workflows without processing them.
//
// The connection is attached to svc from inside, which owns it from there. The
// Client handed back can start and signal workflows; it carries no lifecycle
// methods, so the connection it uses cannot be closed through it.
func Connect(ctx context.Context, svc *service.Service, cfg ConfigClient) (*Client, error) {

	// No need to continue if ConfigClient is not valid.
	if err := cfg.sanitize(); err != nil {
		return nil, err
	}

	c, err := dial(ctx, svc, &cfg, cfg.name(), "config")
	if err != nil {
		return nil, err
	}

	// Hand the connection to the Service, which owns its lifecycle from here. The
	// Client returned to the caller carries no lifecycle methods of its own.
	if err := service.Attach(svc, cfg.name(), &clientConnection{client: c}); err != nil {
		c.Close()
		return nil, err
	}

	return newClient(c, &cfg), nil
}

// ExecuteWorkflow starts a workflow execution and returns a WorkflowRun handle, or
// nil and the error when the workflow could not be started.
//
// Input and Result are inferred from wf, so input is checked against the workflow's
// declared input type at compile time and the returned run yields the workflow's
// declared result type.
//
// The Event set on ctx with event.NewContext travels to the workflow in its
// header. See ConfigClient.PropagateFullEvent for what that includes.
func (c *Client) ExecuteWorkflow[Input, Result any](
	ctx context.Context,
	wf Workflow[Input, Result],
	opts client.StartWorkflowOptions,
	input Input,
) (*WorkflowRun[Result], error) {
	run, err := c.client.ExecuteWorkflow(ctx, opts, wf.name, input)
	if err != nil {
		return nil, err
	}

	return &WorkflowRun[Result]{run: run}, nil
}

// WorkflowRun returns a handle on an existing workflow run, to await its result.
// An empty runID targets the latest run for workflowID. Result is inferred from wf.
//
// No request is made until the handle is used, so an unknown workflow is reported
// by Await rather than here.
func (c *Client) WorkflowRun[Input, Result any](
	ctx context.Context,
	wf Workflow[Input, Result],
	workflowID, runID string,
) *WorkflowRun[Result] {
	return &WorkflowRun[Result]{run: c.client.GetWorkflow(ctx, workflowID, runID)}
}

// SignalWorkflow sends a payload to a running workflow execution. Payload is
// inferred from sig, so the value is checked against the signal's declared payload
// type at compile time. A run ID is optional; an empty string targets the latest run
// for the workflow ID.
func (c *Client) SignalWorkflow[Payload any](
	ctx context.Context,
	sig Signal[Payload],
	workflowID, runID string,
	payload Payload,
) error {
	return c.client.SignalWorkflow(ctx, workflowID, runID, sig.name, payload)
}

// SignalWithStartWorkflow sends a payload to the workflow whose ID is opts.ID,
// starting it with input first if it is not running. It returns a handle on the
// run that received the signal.
//
// Input and Result are inferred from wf, and Payload from sig.
func (c *Client) SignalWithStartWorkflow[Input, Result, Payload any](
	ctx context.Context,
	wf Workflow[Input, Result],
	sig Signal[Payload],
	opts client.StartWorkflowOptions,
	input Input,
	payload Payload,
) (*WorkflowRun[Result], error) {
	run, err := c.client.SignalWithStartWorkflow(ctx, opts.ID, sig.name, payload, opts, wf.name, input)
	if err != nil {
		return nil, err
	}

	return &WorkflowRun[Result]{run: run}, nil
}

// QueryWorkflow runs a query against a workflow execution and returns its result.
// Input and Result are inferred from q. An empty runID targets the latest run for
// workflowID.
func (c *Client) QueryWorkflow[Input, Result any](
	ctx context.Context,
	q Query[Input, Result],
	workflowID, runID string,
	input Input,
) (Result, error) {
	var result Result

	value, err := c.client.QueryWorkflow(ctx, workflowID, runID, q.name, input)
	if err != nil {
		return result, err
	}

	if value == nil || !value.HasValue() {
		return result, nil
	}

	err = value.Get(&result)
	return result, err
}

// UpdateWorkflow sends an update to a workflow execution and waits for it to
// complete, returning its result. Input and Result are inferred from u. An empty
// runID targets the latest run for workflowID.
//
// An update the workflow's validator rejects returns that rejection as the error,
// and is not recorded in the workflow's history.
func (c *Client) UpdateWorkflow[Input, Result any](
	ctx context.Context,
	u Update[Input, Result],
	workflowID, runID string,
	input Input,
) (Result, error) {
	var result Result

	handle, err := c.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   workflowID,
		RunID:        runID,
		UpdateName:   u.name,
		Args:         []any{input},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		return result, err
	}

	err = handle.Get(ctx, &result)
	return result, err
}

// CancelWorkflow requests the cancellation of a workflow execution. The workflow
// sees its context cancelled and decides how to wind down. An empty runID targets
// the latest run for workflowID.
func (c *Client) CancelWorkflow(ctx context.Context, workflowID, runID string) error {
	return c.client.CancelWorkflow(ctx, workflowID, runID)
}

// TerminateWorkflow ends a workflow execution immediately, without giving it a
// chance to clean up, and records reason in its history. An empty runID targets
// the latest run for workflowID.
func (c *Client) TerminateWorkflow(ctx context.Context, workflowID, runID, reason string) error {
	return c.client.TerminateWorkflow(ctx, workflowID, runID, reason)
}

// DescribeWorkflow returns the status and metadata of a workflow execution. An
// empty runID targets the latest run for workflowID.
func (c *Client) DescribeWorkflow(ctx context.Context, workflowID, runID string) (*client.WorkflowExecutionDescription, error) {
	return c.client.DescribeWorkflow(ctx, workflowID, runID)
}

// WorkflowResultIfClosed retrieves a workflow run's result by workflowID (runID
// optional; empty targets the latest run) WITHOUT blocking on a run that has not
// closed. Result is inferred from wf.
//
// The bool reports whether the run had closed. It is false, with the zero Result,
// for a run that is still running or paused, and for an unknown one. Once it is
// true, result holds a completed run's result, or err carries the reason the run
// closed without one: its failure, cancellation, termination or timeout.
//
// A run that continued as new is closed too, and reported as such rather than
// followed: err is then a *workflow.ContinueAsNewError, and the result belongs to
// the run it continued into, which WorkflowResultIfClosed reads when given an
// empty runID. Retries and cron runs are not followed either.
//
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

// closedWorkflowResult deserializes the result of a workflow run that has already
// closed into valuePtr, without blocking on a run that has not. The bool reports
// whether the run had closed: an open or unknown run reports false and leaves
// valuePtr untouched, so a caller polling a long-lived run never parks a goroutine
// on it; a closed run reports true, with valuePtr holding a completed run's result
// or the returned error carrying why there is none (in which case valuePtr is
// unreliable).
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

	// Only a run in a terminal state can be read without blocking: reading any other
	// waits for it to close. The statuses are allow-listed rather than "not running",
	// because a paused run is not running either, and has not closed.
	info := desc.GetWorkflowExecutionInfo()
	if !closedStatus(info.GetStatus()) {
		return false, nil
	}

	// Read the run that was described rather than whichever is latest by now: an
	// empty runID resolves to the latest run on every request, and a retry or a cron
	// run may have started in between.
	if described := info.GetExecution().GetRunId(); described != "" {
		runID = described
	}

	// Following runs is disabled for the same reason. A closed run that continued as
	// new, was retried or runs on a cron would otherwise be followed into its next
	// run, which is still open — and waited on.
	return true, c.client.GetWorkflow(ctx, workflowID, runID).GetWithOptions(ctx, valuePtr, client.WorkflowRunGetOptions{
		DisableFollowingRuns: true,
	})
}

// closedStatus reports whether status is one a workflow run ends in.
func closedStatus(status enumspb.WorkflowExecutionStatus) bool {
	switch status {
	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED,
		enumspb.WORKFLOW_EXECUTION_STATUS_FAILED,
		enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED,
		enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED,
		enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW,
		enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return true
	default:
		return false
	}
}
