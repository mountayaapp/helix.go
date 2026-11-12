package temporal

import (
	"context"
	"reflect"
	"sync"

	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"
	"github.com/mountayaapp/helix.go/telemetry/log"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
)

// activityPanicMessage is the message an activity that panicked fails with: the
// whole of what the failure says, in the history of the workflow that scheduled
// the activity and to that workflow's code.
const activityPanicMessage = "activity panicked"

// The names the SDK records an activity's panic under in its own recover
// (internal/common/metrics), which the interceptor records it under in its place:
// the counter of a panicking activity task and of a panicking local activity, and
// the tags the SDK puts on the second, which the context of a local activity does
// not carry.
const (
	activityPanicCounter      = "temporal_activity_task_error"
	localActivityPanicCounter = "temporal_local_activity_error"
	workflowTypeTag           = "workflow_type"
	activityTypeTag           = "activity_type"
)

// panicFailureType is the type the SDK gives the failure of an activity that
// panicked, read from the SDK's own error so that it follows it: "PanicError". A
// retry policy lists it in NonRetryableErrorTypes to stop retrying a panic, and
// the SDK turns a failure of that type back into a *temporal.PanicError for the
// workflow.
var panicFailureType = reflect.TypeFor[temporal.PanicError]().Name()

// WorkerInterceptors returns the interceptors a worker built by New runs its
// activities under, beside the ones its client brings (tracing, and the
// attributes of the entry spans). They need no Service, so that a test can run an
// activity in the SDK's test environment the way a worker runs it:
//
//	env := suite.NewTestActivityEnvironment()
//	env.SetWorkerOptions(worker.Options{Interceptors: temporal.WorkerInterceptors()})
//
// There is one today: an activity that panics is recovered, and fails with a
// fixed message. See recoverInterceptor.
func WorkerInterceptors() []interceptor.WorkerInterceptor {
	return workerInterceptors(nil)
}

// workerInterceptors returns the interceptors of a worker served by svc, whose
// logger the panic of an activity is logged through, as the SDK logs it through
// the client's. With a nil svc, it is the logger the activity's context carries,
// or the process-wide one.
func workerInterceptors(svc *service.Service) []interceptor.WorkerInterceptor {
	return []interceptor.WorkerInterceptor{&recoverInterceptor{svc: svc}}
}

// recoverInterceptor fails an activity that panicked with a fixed message, in
// place of the failure the SDK builds from the panic.
//
// The SDK recovers an activity's panic itself, and reports it as a failure whose
// message is the value the code panicked with, printed, and whose stack trace is
// the goroutine's. Both are stored in the history of the workflow that scheduled
// the activity, which outlives the worker, is read by whoever may read the
// namespace, and is handed back to the workflow's code as the error's text. A
// panic's value is whatever the failing code held at that moment: a query, an
// address, a key.
//
// The failure built here keeps what a workflow and a retry policy act on, and
// nothing else. Its type is the SDK's own for a panic (panicFailureType), so a
// policy naming it in NonRetryableErrorTypes still stops on it and the workflow
// still receives a *temporal.PanicError; it is retryable, as the SDK's is, so an
// activity that panicked is retried exactly as before. Its message is
// activityPanicMessage and it has no stack trace.
//
// The panic itself goes where the worker's operators read it: logged with its
// stack against the activity's context, and recorded on the activity's span, the
// value as an exception and the stack as an event beside it
// (integration.RecordPanic). What the SDK records of a panic in its own recover
// is recorded as it would have been: the line "Activity panic." (or "LocalActivity
// panic.") with the workflow, the run, the activity type and the attempt, the
// panic as an error field the sanitizer is applied to, and the counter
// temporal_activity_task_error (or temporal_local_activity_error) under the tags
// the SDK gives it, so that what alerts on either goes on seeing a panic.
//
// It is installed through the worker's options rather than the client's, which
// places it after the client's interceptors: innermost, so the span the tracing
// interceptor opened is on the context it recovers under. A panic in a workflow's
// own code is not an activity's and is not recovered here.
type recoverInterceptor struct {
	interceptor.WorkerInterceptorBase

	// svc is the Service whose logger a panic is logged through, when there is
	// one.
	svc *service.Service
}

// InterceptActivity wraps the inbound activity interceptor chain.
func (r *recoverInterceptor) InterceptActivity(
	ctx context.Context,
	next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	i := &activityRecoverInterceptor{svc: r.svc}
	i.Next = next

	return i
}

// activityRecoverInterceptor recovers the panic of one activity execution.
type activityRecoverInterceptor struct {
	interceptor.ActivityInboundInterceptorBase

	// svc is recoverInterceptor.svc.
	svc *service.Service
}

// ExecuteActivity runs the activity, and fails it with activityPanicMessage when
// it panics.
func (i *activityRecoverInterceptor) ExecuteActivity(
	ctx context.Context,
	in *interceptor.ExecuteActivityInput,
) (result any, err error) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}

		logCtx := ctx
		if i.svc != nil {
			logCtx = service.Context(ctx, i.svc)
		}

		recordActivityPanic(logCtx, v)

		failure := temporal.NewApplicationError(activityPanicMessage, panicFailureType)
		recoveredFailures.add(failure)

		result, err = nil, failure
	}()

	return i.Next.ExecuteActivity(ctx, in)
}

// recordActivityPanic records the panic v of the activity ctx runs as the SDK
// records one in its own recover, which the interceptor's stands in front of: a
// line through the Service's logger and a counter through the activity's metrics
// handler. The value goes on the activity's span too (integration.RecordPanic).
func recordActivityPanic(ctx context.Context, v any) {
	info := activity.GetInfo(ctx)

	var workflowType string
	if info.WorkflowType != nil {
		workflowType = info.WorkflowType.Name
	}

	message, counter := "Activity panic.", activityPanicCounter
	handler := activity.GetMetricsHandler(ctx)
	if info.IsLocalActivity {
		message, counter = "LocalActivity panic.", localActivityPanicCounter
		handler = handler.WithTags(map[string]string{
			workflowTypeTag: workflowType,
			activityTypeTag: info.ActivityType.Name,
		})
	}

	integration.RecordPanic(ctx, message, "failed to run activity", v,
		log.String("WorkflowID", info.WorkflowExecution.ID),
		log.String("RunID", info.WorkflowExecution.RunID),
		log.String("ActivityType", info.ActivityType.Name),
		log.Int("Attempt", int(info.Attempt)),
	)

	handler.Counter(counter).Inc(1)
}

// sdkActivityErrorMessage and sdkErrorKey are the message and the key of the line
// the SDK logs for an activity that returned an error, the error under the key
// (internal_task_handlers.go and internal_logging_tags.go of the SDK).
const (
	sdkActivityErrorMessage = "Activity error."
	sdkErrorKey             = "Error"
)

// maxRecoveredFailures is how many of the failures the interceptor returned
// recoveredFailures remembers: far more than a worker returns in the instant
// between an activity's failure and the SDK's line for it, which follows on the
// same goroutine.
const maxRecoveredFailures = 256

// recoveredFailures remembers the failures the interceptor returned for a panic,
// by identity, so that the SDK's line for an activity that returned an error is
// not written for them (customLogger.Error). The panic was logged already, as
// the SDK logs one in its own recover (recordActivityPanic), and the failure the
// SDK would log beside it is the interceptor's fixed one: one event, which a
// plain worker logs once. It holds the last maxRecoveredFailures, a failure whose
// line never comes (the SDK logs none for an activity that ended past its
// deadline) being forgotten as newer ones come.
var recoveredFailures = &failureRing{}

// failureRing is a bounded set of errors, compared by identity, the oldest
// forgotten first. It is safe for concurrent use.
type failureRing struct {
	mu     sync.Mutex
	errors [maxRecoveredFailures]error
	next   int
}

// add remembers err, forgetting the oldest error remembered when full.
func (r *failureRing) add(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.errors[r.next] = err
	r.next = (r.next + 1) % maxRecoveredFailures
}

// take reports whether err is remembered, and forgets it.
func (r *failureRing) take(err error) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, remembered := range r.errors {
		if remembered != nil && remembered == err { //nolint:errorlint // By identity: the very failure the interceptor returned.
			r.errors[i] = nil
			return true
		}
	}

	return false
}

// reportsRecoveredFailure reports whether a line the SDK logs, its message and
// its key-value pairs, is its line for an activity that returned a failure the
// interceptor returned for a panic (recoveredFailures).
func reportsRecoveredFailure(msg string, keyvals []any) bool {
	if msg != sdkActivityErrorMessage {
		return false
	}

	for i := 0; i+1 < len(keyvals); i += 2 {
		if key, ok := keyvals[i].(string); ok && key == sdkErrorKey {
			if err, ok := keyvals[i+1].(error); ok {
				return recoveredFailures.take(err)
			}
		}
	}

	return false
}
