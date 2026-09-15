package temporal

import (
	"go.temporal.io/sdk/workflow"
)

// Activity is the type-safe handle for a Temporal activity definition. Generics
// [Input, Result] enforce the data contract end-to-end, abstracting away Temporal's
// reliance on interface{} for arguments and results.
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
// The provided workflow.ActivityOptions are applied on every Execute call, ensuring
// consistent timeout and retry behavior across all executions of this activity.
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
// via interface{}. Activity options defined at creation time are applied to the
// workflow context before execution.
//
// Execute takes no Client: inside a workflow the dispatcher travels on the
// workflow.Context.
func (a Activity[Input, Result]) Execute(
	ctx workflow.Context,
	input Input,
) *ActivityRun[Result] {
	ctx = workflow.WithActivityOptions(ctx, a.opts)

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
// Activity handle with [Config]. The provided workflow.ActivityOptions are applied
// on every Execute call.
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
// implementation. Activity options defined at creation time are applied to the
// workflow context before execution.
func (a RepeatableActivity[Input, Config, Result]) Execute(
	ctx workflow.Context,
	input Input,
	config Config,
) *ActivityRun[Result] {
	ctx = workflow.WithActivityOptions(ctx, a.opts)

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
