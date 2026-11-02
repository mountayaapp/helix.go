package temporal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/mountayaapp/helix.go/errorstack"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// scheduleFingerprintMemo is the memo key CreateSchedule records the fingerprint
// of the schedule it asked for under, on the action of the schedule.
//
// It is how a later CreateSchedule tells whether the schedule still matches what
// the code asks for. The server turns cron expressions into calendar specs and
// does not keep the expressions, so the spec it describes cannot be compared with
// the one requested; and a schedule's own memo cannot be updated, while its action
// can. The memo is therefore also on every workflow the schedule starts.
const scheduleFingerprintMemo = "helix.schedule.fingerprint"

// ScheduleOptions holds the options to pass for creating a new schedule of a
// workflow.
type ScheduleOptions struct {

	// ID identifies the schedule, and the workflows it starts.
	//
	// Default:
	//
	//   the workflow's name
	ID string

	// TaskQueue is the task queue the scheduled workflows are started on.
	//
	// Required.
	TaskQueue string

	// CronExpressions are the times the workflow is started at, as cron
	// expressions. The schedule starts it at the union of all of them.
	CronExpressions []string

	// OverlapPolicy controls what happens when an Action would be started by a
	// Schedule at the same time that an older Action is still running.
	//
	// Default:
	//
	//   enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
	OverlapPolicy enumspb.ScheduleOverlapPolicy
}

// CreateSchedule makes sure a Temporal Schedule starts wf with input as opts
// describes, and is meant to be called on every startup of every replica.
//
// It creates the schedule when there is none. When there is one already, it
// leaves it alone if it matches the request — no error, and none recorded in
// traces — and updates it otherwise, keeping its state: a paused schedule stays
// paused. A replica creating the schedule at the same time is not an error
// either: the schedule it created is checked and updated the same way.
//
// Whether a schedule matches is decided by a fingerprint of the request recorded
// on the schedule's action, so a change made outside the code — in the Temporal
// UI — is left in place until the request itself changes.
func (c *Client) CreateSchedule[Input, Result any](
	ctx context.Context,
	wf Workflow[Input, Result],
	opts ScheduleOptions,
	input Input,
) error {
	request, err := c.scheduleOptions(wf.name, opts, input)
	if err != nil {
		return err
	}

	return c.createSchedule(ctx, request)
}

// scheduleOptions builds the SDK options CreateSchedule asks for, with the
// fingerprint of the request on the action's memo.
func (c *Client) scheduleOptions(workflowName string, opts ScheduleOptions, input any) (client.ScheduleOptions, error) {
	id := opts.ID
	if id == "" {
		id = workflowName
	}

	overlap := opts.OverlapPolicy
	if overlap == enumspb.SCHEDULE_OVERLAP_POLICY_UNSPECIFIED {
		overlap = enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
	}

	fingerprint, err := scheduleFingerprint(workflowName, opts.TaskQueue, opts.CronExpressions, overlap, input)
	if err != nil {
		return client.ScheduleOptions{}, err
	}

	return client.ScheduleOptions{
		ID: id,
		Spec: client.ScheduleSpec{
			CronExpressions: opts.CronExpressions,
		},
		Action: &client.ScheduleWorkflowAction{
			ID:        id,
			Workflow:  workflowName,
			TaskQueue: opts.TaskQueue,
			Args:      []any{input},
			Memo: map[string]any{
				scheduleFingerprintMemo: fingerprint,
			},
		},
		Overlap: overlap,
	}, nil
}

// createSchedule creates the schedule opts describes, or brings the existing one
// in line with it.
func (c *Client) createSchedule(ctx context.Context, opts client.ScheduleOptions) error {
	schedules := c.client.ScheduleClient()
	handle := schedules.GetHandle(ctx, opts.ID)

	// Describe first rather than create and handle the conflict: the tracing
	// interceptor records a failed Create as a span error, and a schedule that
	// already exists is the normal case on every startup but the first.
	desc, err := handle.Describe(ctx)
	if err == nil {
		return c.reconcileSchedule(ctx, handle, desc, opts)
	}

	var notFound *serviceerror.NotFound
	if !errors.As(err, &notFound) {
		return err
	}

	_, err = schedules.Create(ctx, opts)
	if !errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		return err
	}

	// Another replica created it between Describe and Create. That is not a
	// conflict: check what it created the same way as a schedule found by Describe.
	desc, err = handle.Describe(ctx)
	if err != nil {
		return err
	}

	return c.reconcileSchedule(ctx, handle, desc, opts)
}

// reconcileSchedule updates the schedule handle refers to, which desc describes,
// unless it already matches opts.
func (c *Client) reconcileSchedule(
	ctx context.Context,
	handle client.ScheduleHandle,
	desc *client.ScheduleDescription,
	opts client.ScheduleOptions,
) error {
	if schedulesMatch(desc, opts) && c.fingerprintMatches(desc, opts) {
		return nil
	}

	return handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			schedule := in.Description.Schedule
			schedule.Spec = &opts.Spec
			schedule.Action = opts.Action

			policies := client.SchedulePolicies{}
			if schedule.Policy != nil {
				policies = *schedule.Policy
			}

			policies.Overlap = opts.Overlap
			schedule.Policy = &policies

			return &client.ScheduleUpdate{Schedule: &schedule}, nil
		},
	})
}

// fingerprintMatches reports whether the schedule desc describes was last created
// or updated from the request opts holds.
func (c *Client) fingerprintMatches(desc *client.ScheduleDescription, opts client.ScheduleOptions) bool {
	existing, ok := desc.Schedule.Action.(*client.ScheduleWorkflowAction)
	if !ok {
		return false
	}

	requested, ok := opts.Action.(*client.ScheduleWorkflowAction)
	if !ok {
		return false
	}

	// Describe hands memo values back as the payloads the server stores, encoded
	// with the client's DataConverter.
	payload, ok := existing.Memo[scheduleFingerprintMemo].(*commonpb.Payload)
	if !ok {
		return false
	}

	var fingerprint string
	if err := c.converter.FromPayload(payload, &fingerprint); err != nil {
		return false
	}

	return fingerprint == requested.Memo[scheduleFingerprintMemo]
}

// schedulesMatch reports whether the existing schedule matches the requested
// schedule options by comparing overlap policy, workflow name, and task queue:
// the parts of a schedule that can be compared as they are described. It catches
// a schedule changed outside the code; fingerprintMatches catches the rest.
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

	return existing.TaskQueue == requested.TaskQueue
}

// scheduleFingerprint returns a digest of everything CreateSchedule was asked for.
//
// The input is encoded as JSON for it, rather than with the client's
// DataConverter: a converter with an encrypting codec encodes the same value
// differently every time, and a fingerprint that changes on every startup would
// update the schedule on every startup.
func scheduleFingerprint(
	workflowName, taskQueue string,
	cron []string,
	overlap enumspb.ScheduleOverlapPolicy,
	input any,
) (string, error) {
	encoded, err := json.Marshal(struct {
		Workflow  string   `json:"workflow"`
		TaskQueue string   `json:"task_queue"`
		Cron      []string `json:"cron"`
		Overlap   int32    `json:"overlap"`
		Input     any      `json:"input"`
	}{
		Workflow:  workflowName,
		TaskQueue: taskQueue,
		Cron:      cron,
		Overlap:   int32(overlap),
		Input:     input,
	})
	if err != nil {
		return "", errorstack.NewValidation(errorstack.Entry{
			Message: "Must be serializable as JSON; " + err.Error(),
			Path:    []any{"input"},
		})
	}

	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
