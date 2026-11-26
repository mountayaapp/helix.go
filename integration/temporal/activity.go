package temporal

import (
	"go.temporal.io/sdk/workflow"
)

// Activity is the type-safe handle for a Temporal activity definition. Generics
// [Input, Result] enforce the data contract end-to-end, abstracting away Temporal's
// reliance on untyped (any) arguments and results.
//
// An Activity carries no connection state, so handles are meant to be declared as
// package-level variables:
//
//	var ChargeUser = temporal.NewActivity[ChargeInput, ChargeResult]("charge-user", workflow.ActivityOptions{
//		StartToCloseTimeout: time.Minute,
//	})
//
// Pass the handle to Worker.RegisterActivity to bind an implementation; call Execute
// on it from inside a workflow.
type Activity[Input, Result any] struct {
	name string
	opts workflow.ActivityOptions
}

// NewActivity is the factory function for creating a new type-safe Activity handle.
// The provided workflow.ActivityOptions are the activity's defaults, applied on
// every Execute call to whatever the workflow context leaves unset, so that the
// activity runs with consistent timeouts and retries wherever it is called from.
//
// Per Temporal best practices, callers should always set at least StartToCloseTimeout
// in the options.
func NewActivity[Input, Result any](name string, opts workflow.ActivityOptions) Activity[Input, Result] {
	return Activity[Input, Result]{
		name: name,
		opts: opts,
	}
}

// Name returns the activity's name as registered with Temporal. Use it when
// correlating executions or for observability.
func (a Activity[Input, Result]) Name() string {
	return a.name
}

// Execute wraps workflow.ExecuteActivity. The [Input] type constraint on input
// prevents runtime errors from passing incorrectly typed arguments to the activity
// via any.
//
// The options the activity was declared with are defaults. Options already on ctx,
// set with workflow.WithActivityOptions, take precedence field by field, which is
// how one call is given a longer timeout or a different task queue:
//
//	ctx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Hour})
//	result, err := ChargeUser.Execute(ctx, input).Await(ctx)
//
// Execute takes no Client: inside a workflow the dispatcher travels on the
// workflow.Context.
func (a Activity[Input, Result]) Execute(
	ctx workflow.Context,
	input Input,
) *ActivityRun[Result] {
	ctx = withDefaultActivityOptions(ctx, a.opts)

	return &ActivityRun[Result]{
		future: workflow.ExecuteActivity(ctx, a.name, input),
	}
}

// RepeatableActivity extends Activity by allowing an additional generic [Config]
// parameter. [Config] is intended for execution-specific options, settings, or
// dynamic activity options that are not part of the primary data payload ([Input]).
// This allows separation of execution metadata from activity business input.
// For example, if the core business payload ([Input]) contains multiple images to
// download, [Config] could specify photo details, such as the URL or specific
// image processing flags.
type RepeatableActivity[Input, Config, Result any] struct {
	name string
	opts workflow.ActivityOptions
}

// NewRepeatableActivity is the factory function for creating a type-safe repeatable
// Activity handle with [Config]. The provided workflow.ActivityOptions are the
// activity's defaults, applied on every Execute call the way NewActivity's are.
func NewRepeatableActivity[Input, Config, Result any](name string, opts workflow.ActivityOptions) RepeatableActivity[Input, Config, Result] {
	return RepeatableActivity[Input, Config, Result]{
		name: name,
		opts: opts,
	}
}

// Name returns the activity's name as registered with Temporal. Use it when
// correlating executions or for observability.
func (a RepeatableActivity[Input, Config, Result]) Name() string {
	return a.name
}

// Execute wraps workflow.ExecuteActivity, requiring and enforcing both [Input] and
// [Config] types. Both are passed as arguments to the underlying activity
// implementation. Options are resolved the way Activity.Execute resolves them:
// those on ctx first, the activity's defaults for the rest.
func (a RepeatableActivity[Input, Config, Result]) Execute(
	ctx workflow.Context,
	input Input,
	config Config,
) *ActivityRun[Result] {
	ctx = withDefaultActivityOptions(ctx, a.opts)

	return &ActivityRun[Result]{
		future: workflow.ExecuteActivity(ctx, a.name, input, config),
	}
}

// ActivityRun wraps the Temporal workflow.Future, providing type-safe retrieval of
// the activity's [Result].
type ActivityRun[Result any] struct {
	future workflow.Future
}

// Await blocks until the activity completes and returns its result, deserialized
// into the generic [Result] type.
func (ar *ActivityRun[Result]) Await(ctx workflow.Context) (Result, error) {
	var output Result
	err := ar.future.Get(ctx, &output)

	return output, err
}

// AwaitInto blocks until the activity completes and deserializes its result into
// dst, which the caller supplies already populated when the activity's payload
// should be merged into an accumulating value rather than replace it. It returns
// a validation error when dst is nil, rather than discarding the result.
//
// The merge is a property of the DataConverter, not of AwaitInto. With the
// default JSON encoding, a field the activity left at its zero value under
// omitempty is absent from the wire and leaves whatever dst already held, while a
// field without omitempty transmits its zero value and overwrites. A protobuf
// result is decoded by protojson or proto, which both reset dst before decoding
// into it: nothing is merged. Callers that want a clean result want Await instead.
func (ar *ActivityRun[Result]) AwaitInto(ctx workflow.Context, dst *Result) error {
	if dst == nil {
		return nilDestination()
	}

	return ar.future.Get(ctx, dst)
}

// withDefaultActivityOptions returns a copy of ctx whose activity options are the
// ones ctx carries, with every field it leaves unset taken from defaults.
//
// A workflow context always carries a task queue — the workflow's own, which the
// SDK sets before the workflow runs — so that one counts as unset. The two
// booleans cannot tell "unset" from false, so either side setting them to true
// wins.
func withDefaultActivityOptions(ctx workflow.Context, defaults workflow.ActivityOptions) workflow.Context {
	opts := workflow.GetActivityOptions(ctx)

	if defaults.TaskQueue != "" && (opts.TaskQueue == "" || opts.TaskQueue == workflow.GetInfo(ctx).TaskQueueName) {
		opts.TaskQueue = defaults.TaskQueue
	}
	if opts.ScheduleToCloseTimeout == 0 {
		opts.ScheduleToCloseTimeout = defaults.ScheduleToCloseTimeout
	}
	if opts.ScheduleToStartTimeout == 0 {
		opts.ScheduleToStartTimeout = defaults.ScheduleToStartTimeout
	}
	if opts.StartToCloseTimeout == 0 {
		opts.StartToCloseTimeout = defaults.StartToCloseTimeout
	}
	if opts.HeartbeatTimeout == 0 {
		opts.HeartbeatTimeout = defaults.HeartbeatTimeout
	}
	if opts.ActivityID == "" {
		opts.ActivityID = defaults.ActivityID
	}
	if opts.RetryPolicy == nil {
		opts.RetryPolicy = defaults.RetryPolicy
	}

	// Deprecated by the SDK in favour of worker deployment versioning, and still
	// honoured for an activity that sets it, as it was before options were merged.
	if opts.VersioningIntent == 0 { //nolint:staticcheck // see above
		opts.VersioningIntent = defaults.VersioningIntent //nolint:staticcheck // see above
	}

	if opts.Summary == "" {
		opts.Summary = defaults.Summary
	}
	if opts.Priority == (workflow.ActivityOptions{}).Priority {
		opts.Priority = defaults.Priority
	}

	opts.WaitForCancellation = opts.WaitForCancellation || defaults.WaitForCancellation
	opts.DisableEagerExecution = opts.DisableEagerExecution || defaults.DisableEagerExecution

	return workflow.WithActivityOptions(ctx, opts)
}
