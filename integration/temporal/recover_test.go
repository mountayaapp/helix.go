package temporal

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mountaya/helix.go/service/servicetest"
	"github.com/mountaya/helix.go/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// panicValue is what the activities below panic with: a sentence quoting a
// value shaped like a credential, as code holding one may.
const panicValue = "refresh failed for token tok_0123456789abcdef"

// panickingActivity panics on the activity's own goroutine.
func panickingActivity(context.Context, string) (string, error) {
	panic(panicValue)
}

// executePanickingActivity runs panickingActivity in the SDK's test environment
// under interceptors, rooted at ctx, and returns the error it failed with.
func executePanickingActivity(t *testing.T, ctx context.Context, interceptors []interceptor.WorkerInterceptor) error {
	t.Helper()

	var suite testsuite.WorkflowTestSuite

	env := suite.NewTestActivityEnvironment()
	env.SetWorkerOptions(worker.Options{BackgroundActivityContext: ctx, Interceptors: interceptors})
	env.RegisterActivity(panickingActivity)

	_, err := env.ExecuteActivity(panickingActivity, "input")
	require.Error(t, err)

	return err
}

// storedFailure returns err as the SDK stores it in a workflow's history: the
// failure its converter builds from it.
func storedFailure(err error) *failurepb.Failure {
	return temporal.GetDefaultFailureConverter().ErrorToFailure(err)
}

// panicFailure returns the failure of the panic itself within stored: the
// innermost one, under whatever the SDK wrapped it in.
func panicFailure(stored *failurepb.Failure) *failurepb.Failure {
	for stored.GetCause() != nil {
		stored = stored.GetCause()
	}

	return stored
}

// TestActivityPanic_StoresNothingOfThePanic pins what the failure of an activity
// that panicked holds. Left to the SDK it holds the value the code panicked with
// and the stack of its goroutine, both stored in the workflow's history. Under
// the interceptors of a worker it holds a fixed message and no stack, and is the
// same failure in everything else: the type a retry policy and a workflow match
// on, and retryable.
func TestActivityPanic_StoresNothingOfThePanic(t *testing.T) {
	bare := executePanickingActivity(t, t.Context(), nil)
	recovered := executePanickingActivity(t, t.Context(), WorkerInterceptors())

	t.Run("left to the SDK, the failure holds the value and a stack", func(t *testing.T) {
		stored := panicFailure(storedFailure(bare))

		assert.Equal(t, panicValue, stored.GetMessage())
		assert.Contains(t, stored.GetStackTrace(), "panickingActivity")
	})

	t.Run("under a worker's interceptors, it holds neither", func(t *testing.T) {
		assert.NotContains(t, recovered.Error(), "tok_")

		var panicked *temporal.PanicError
		require.ErrorAs(t, recovered, &panicked, "the workflow no longer receives a *temporal.PanicError")
		assert.Equal(t, activityPanicMessage, panicked.Error())
		assert.Empty(t, panicked.StackTrace())

		encoded, err := protojson.Marshal(storedFailure(recovered))
		require.NoError(t, err)

		for _, piece := range []string{"tok_", "refresh failed", "goroutine", ".go:"} {
			assert.NotContains(t, string(encoded), piece)
		}

		stored := panicFailure(storedFailure(recovered))
		assert.Equal(t, activityPanicMessage, stored.GetMessage())
		assert.Empty(t, stored.GetStackTrace())
	})

	t.Run("it is the SDK's failure in everything but the message and the stack", func(t *testing.T) {
		theirs := proto.CloneOf(panicFailure(storedFailure(bare)))
		ours := proto.CloneOf(panicFailure(storedFailure(recovered)))

		assert.Equal(t, "PanicError", ours.GetApplicationFailureInfo().GetType())
		assert.False(t, ours.GetApplicationFailureInfo().GetNonRetryable(), "a panic is retried as any other failure")

		theirs.Message, theirs.StackTrace = "", ""
		ours.Message, ours.StackTrace = "", ""
		assert.True(t, proto.Equal(theirs, ours), "theirs: %v\nours: %v", theirs, ours)
	})
}

// TestActivityPanic_IsRecordedOnTheActivitysSpan pins where the panic goes
// instead: on the span the activity runs under, the value as an exception and the
// stack of the goroutine that panicked as an event beside it.
func TestActivityPanic_IsRecordedOnTheActivitysSpan(t *testing.T) {
	_, rec := servicetest.New(t)

	ctx, span := trace.Start(t.Context(), trace.SpanKindServer, "Run Activity")
	executePanickingActivity(t, ctx, WorkerInterceptors())
	span.End()

	spans := rec.Spans()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Error, spans[0].Status().Code)
	assert.Equal(t, "failed to run activity", spans[0].Status().Description)

	recorded := map[string]string{}
	for _, event := range spans[0].Events() {
		for _, attr := range event.Attributes {
			recorded[event.Name+"/"+string(attr.Key)] = attr.Value.AsString()
		}
	}

	assert.Equal(t, "panic: "+panicValue, recorded["exception/exception.message"])
	assert.Contains(t, recorded["panic/exception.stacktrace"], "panickingActivity", "the stack is not the panicking goroutine's")
}

// An activity that returns, with or without an error, is not this interceptor's
// business.
func TestActivityRecover_LeavesAnActivityThatReturnsAlone(t *testing.T) {
	failed := errors.New("out of stock")

	var suite testsuite.WorkflowTestSuite

	env := suite.NewTestActivityEnvironment()
	env.SetWorkerOptions(worker.Options{Interceptors: WorkerInterceptors()})

	succeeds := func(context.Context, string) (string, error) { return "done", nil }
	fails := func(context.Context, string) (string, error) { return "", failed }
	env.RegisterActivity(succeeds)
	env.RegisterActivity(fails)

	value, err := env.ExecuteActivity(succeeds, "input")
	require.NoError(t, err)

	var result string
	require.NoError(t, value.Get(&result))
	assert.Equal(t, "done", result)

	_, err = env.ExecuteActivity(fails, "input")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of stock")

	_, panicked := errors.AsType[*temporal.PanicError](err)
	assert.False(t, panicked)
}

// panicReport is what the workflow below returns of the error its activity
// failed with, as workflow code reads it.
type panicReport struct {
	IsPanicError bool
	Message      string
	StackTrace   string
}

// TestActivityPanic_OnAWorker runs an activity that panics on a worker built by
// New, against a real server, and pins the three things the fixed failure must
// keep or lose there: the activity is retried as its retry policy says, a policy
// naming the SDK's type for a panic still stops on it, and no event of the
// workflow's history holds the value or a stack.
func TestActivityPanic_OnAWorker(t *testing.T) {
	address := startTemporal(t)
	svc, _ := servicetest.New(t)

	c, w, err := New(t.Context(), svc, ConfigWorker{
		Client:    ConfigClient{Address: address},
		TaskQueue: "panics",
	})
	require.NoError(t, err)

	retried := NewActivity[string, string]("retried-activity", workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: 10 * time.Millisecond, MaximumAttempts: 3},
	})
	stopped := NewActivity[string, string]("stopped-activity", workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        10 * time.Millisecond,
			MaximumAttempts:        3,
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})

	var retriedAttempts, stoppedAttempts atomic.Int32
	w.RegisterActivity(retried, func(context.Context, string) (string, error) {
		retriedAttempts.Add(1)
		panic(panicValue)
	})
	w.RegisterActivity(stopped, func(context.Context, string) (string, error) {
		stoppedAttempts.Add(1)
		panic(panicValue)
	})

	report := func(err error) panicReport {
		panicked, ok := errors.AsType[*temporal.PanicError](err)
		if !ok {
			return panicReport{Message: err.Error()}
		}

		return panicReport{IsPanicError: true, Message: panicked.Error(), StackTrace: panicked.StackTrace()}
	}

	retrying := NewWorkflow[string, panicReport]("retrying-workflow")
	w.RegisterWorkflow(retrying, func(ctx workflow.Context, input string) (panicReport, error) {
		_, err := retried.Execute(ctx, input).Await(ctx)
		return report(err), nil
	})

	stopping := NewWorkflow[string, panicReport]("stopping-workflow")
	w.RegisterWorkflow(stopping, func(ctx workflow.Context, input string) (panicReport, error) {
		_, err := stopped.Execute(ctx, input).Await(ctx)
		return report(err), nil
	})

	require.NoError(t, svc.Start(t.Context()))
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	t.Run("the activity is retried as its policy says", func(t *testing.T) {
		run, err := c.ExecuteWorkflow(t.Context(), retrying, client.StartWorkflowOptions{ID: "retrying", TaskQueue: "panics"}, "input")
		require.NoError(t, err)

		got, err := run.Await(t.Context())
		require.NoError(t, err)

		assert.Equal(t, int32(3), retriedAttempts.Load())
		assert.Equal(t, panicReport{IsPanicError: true, Message: activityPanicMessage}, got)
	})

	t.Run("a policy naming the type of a panic stops on it", func(t *testing.T) {
		run, err := c.ExecuteWorkflow(t.Context(), stopping, client.StartWorkflowOptions{ID: "stopping", TaskQueue: "panics"}, "input")
		require.NoError(t, err)

		got, err := run.Await(t.Context())
		require.NoError(t, err)

		assert.Equal(t, int32(1), stoppedAttempts.Load())
		assert.Equal(t, panicReport{IsPanicError: true, Message: activityPanicMessage}, got)
	})

	t.Run("no event of the history holds the value or a stack", func(t *testing.T) {
		for _, id := range []string{"retrying", "stopping"} {
			var failures int

			events := c.client.GetWorkflowHistory(t.Context(), id, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
			for events.HasNext() {
				event, err := events.Next()
				require.NoError(t, err)

				encoded, err := protojson.Marshal(event)
				require.NoError(t, err)

				for _, piece := range []string{"tok_", "refresh failed", "goroutine", "recover_test.go"} {
					assert.NotContains(t, string(encoded), piece, "event %d of %s holds %q", event.GetEventId(), id, piece)
				}

				if failed := event.GetActivityTaskFailedEventAttributes(); failed != nil {
					failures++
					assert.Equal(t, activityPanicMessage, panicFailure(failed.GetFailure()).GetMessage())
				}
			}

			assert.Equal(t, 1, failures, "the history of %s records the activity's failure once, after its last attempt", id)
		}
	})
}

// TestRecoveredFailures pins how the failures the interceptor returned are
// remembered for the SDK's line: by identity, once, and no more than
// maxRecoveredFailures of them, the oldest forgotten first.
func TestRecoveredFailures(t *testing.T) {
	ring := &failureRing{}

	failure := temporal.NewApplicationError(activityPanicMessage, panicFailureType)
	ring.add(failure)

	assert.False(t, ring.take(temporal.NewApplicationError(activityPanicMessage, panicFailureType)), "an equal failure the interceptor did not return was taken")
	assert.True(t, ring.take(failure))
	assert.False(t, ring.take(failure), "a failure was taken twice")

	first := temporal.NewApplicationError(activityPanicMessage, panicFailureType)
	ring.add(first)
	for range maxRecoveredFailures {
		ring.add(temporal.NewApplicationError(activityPanicMessage, panicFailureType))
	}

	assert.False(t, ring.take(first), "the ring kept more than maxRecoveredFailures")
}

// TestReportsRecoveredFailure pins which of the SDK's lines is left out: its line
// for an activity that returned the failure the interceptor returned, and no
// other line naming that failure, and no other failure.
func TestReportsRecoveredFailure(t *testing.T) {
	failure := temporal.NewApplicationError(activityPanicMessage, panicFailureType)

	recoveredFailures.add(failure)
	assert.False(t, reportsRecoveredFailure("Activity complete after timeout.", []any{sdkErrorKey, failure}))
	assert.False(t, reportsRecoveredFailure(sdkActivityErrorMessage, []any{"Cause", failure}))
	assert.False(t, reportsRecoveredFailure(sdkActivityErrorMessage, []any{sdkErrorKey, errors.New(activityPanicMessage)}))
	assert.True(t, reportsRecoveredFailure(sdkActivityErrorMessage, []any{"ActivityType", "panicky", sdkErrorKey, failure}))
	assert.False(t, reportsRecoveredFailure(sdkActivityErrorMessage, []any{sdkErrorKey, failure}), "the line was left out twice")
}
