package temporal

import (
	"context"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"
)

/*
Activity defines the contract for a fully type-safe Temporal Activity. Generics
[Input, Result] enforce the data contract end-to-end, abstracting away Temporal's
reliance on interface{} for arguments/results.
*/
type Activity[Input, Result any] interface {

	// Register links the implementation to the activity name on a worker. The
	// function signature of impl is strictly checked against [Input] and [Result].
	Register(worker Worker, impl func(ctx context.Context, input Input) (Result, error))

	// Execute requests activity execution from within a workflow, enforcing the
	// [Input] type. It returns an ActivityRun wrapper for type-safe result
	// retrieval.
	Execute(ctx workflow.Context, input Input) ActivityRun[Result]
}

/*
activityDefinition is the concrete implementation of a type-safe Temporal activity.
*/
type activityDefinition[Input, Result any] struct {
	name string
	opts workflow.ActivityOptions
}

/*
NewActivity is the factory function for creating a new type-safe Activity handle.
The provided workflow.ActivityOptions are applied on every Execute call, ensuring
consistent timeout and retry behavior across all executions of this activity.

Per Temporal best practices, callers should always set at least StartToCloseTimeout
in the options.
*/
func NewActivity[Input, Result any](name string, opts workflow.ActivityOptions) Activity[Input, Result] {
	return &activityDefinition[Input, Result]{
		name: name,
		opts: opts,
	}
}

/*
Register registers the implementation with the worker. The primary value of this
wrapper is ensuring the function signature matches the defined Activity contract
at compile time.
*/
func (d *activityDefinition[Input, Result]) Register(
	worker Worker,
	impl func(ctx context.Context, input Input) (Result, error),
) {
	worker.registerActivity(impl, activity.RegisterOptions{
		Name: d.name,
	})
}

/*
Execute wraps workflow.ExecuteActivity. The [Input] type constraint on input
prevents runtime errors from passing incorrectly typed arguments to the activity
via interface{}. Activity options defined at creation time are applied to the
workflow context before execution.
*/
func (d *activityDefinition[Input, Result]) Execute(
	ctx workflow.Context,
	input Input,
) ActivityRun[Result] {
	ctx = workflow.WithActivityOptions(ctx, d.opts)
	future := workflow.ExecuteActivity(ctx, d.name, input)
	result := activityRun[Result]{
		future: future,
	}

	return &result
}

/*
repeatableActivity extends Activity by allowing an additional generic [Config]
parameter. [Config] is intended for execution-specific options, settings, or
dynamic activity options that are not part of the primary data payload ([Input]).
This allows separation of execution metadata from activity business input.
For example, if the core business payload ([Input]) contains multiple images to
download, [Config] could specify a photo details, such as the URL or specific
image processing flags.
*/
type repeatableActivity[Input, Config, Result any] interface {

	// Register links the implementation to the activity name on a worker. The
	// function signature of impl is strictly checked against [Input, Config] and
	// [Result].
	Register(worker Worker, impl func(ctx context.Context, input Input, config Config) (Result, error))

	// Execute requests activity execution from within a workflow, enforcing the
	// [Input, Config] type. It returns an ActivityRun wrapper for type-safe result
	// retrieval.
	Execute(ctx workflow.Context, input Input, config Config) ActivityRun[Result]
}

/*
repeatableActivityDefinition is the concrete implementation for activities with
an extra Config parameter (= repeatable activities).
*/
type repeatableActivityDefinition[Input, Config, Result any] struct {
	name string
	opts workflow.ActivityOptions
}

/*
NewRepeatableActivity is the factory function for creating a type-safe repeatable
Activity handle with [Config]. The provided workflow.ActivityOptions are applied
on every Execute call.
*/
func NewRepeatableActivity[Input, Config, Result any](name string, opts workflow.ActivityOptions) repeatableActivity[Input, Config, Result] {
	return &repeatableActivityDefinition[Input, Config, Result]{
		name: name,
		opts: opts,
	}
}

/*
Register registers the implementation with the worker. The primary value of this
wrapper is ensuring the worker function signature matches the defined Activity
contract at compile time.
*/
func (d *repeatableActivityDefinition[Input, Config, Result]) Register(
	worker Worker,
	impl func(ctx context.Context, input Input, config Config) (Result, error),
) {
	worker.registerActivity(impl, activity.RegisterOptions{
		Name: d.name,
	})
}

/*
Execute wraps workflow.ExecuteActivity, requiring and enforcing both [Input] and
[Config] types. Both are passed as arguments to the underlying activity
implementation. Activity options defined at creation time are applied to the
workflow context before execution.
*/
func (d *repeatableActivityDefinition[Input, Config, Result]) Execute(
	ctx workflow.Context,
	input Input,
	config Config,
) ActivityRun[Result] {
	ctx = workflow.WithActivityOptions(ctx, d.opts)
	future := workflow.ExecuteActivity(ctx, d.name, input, config)
	result := activityRun[Result]{
		future: future,
	}

	return &result
}

/*
ActivityRun abstracts and provides type-safe retrieval from the workflow.Future.
*/
type ActivityRun[Result any] interface {

	// GetResult blocks until the activity completes. It requires a pointer to the
	// generic [Result] type, guaranteeing the unmarshalling target is correct.
	GetResult(ctx workflow.Context, valuePtr *Result) error
}

/*
activityRun is the concrete implementation to wrap the underlying Temporal
workflow.Future.
*/
type activityRun[Result any] struct {
	future workflow.Future
}

/*
GetResult delegates to workflow.Future.Get, ensuring that only a pointer
to the expected [Result] type is used for deserialization.
*/
func (ar *activityRun[Result]) GetResult(ctx workflow.Context, valuePtr *Result) error {
	return ar.future.Get(ctx, valuePtr)
}
