package temporal

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// panickingLocalActivity panics on the local activity's own goroutine.
func panickingLocalActivity(context.Context, string) (string, error) {
	panic(panicValue)
}

// panickingWorkflow runs the activity "panicky" and a local activity, each of
// which panics, once each, and returns once both have failed.
func panickingWorkflow(ctx workflow.Context, input string) (string, error) {
	once := &temporal.RetryPolicy{MaximumAttempts: 1}

	activityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: once})
	_ = workflow.ExecuteActivity(activityCtx, "panicky", input).Get(activityCtx, nil)

	localCtx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: once})
	_ = workflow.ExecuteLocalActivity(localCtx, panickingLocalActivity, input).Get(localCtx, nil)

	return "done", nil
}

// panicSeries returns, for each counter the SDK records a panic under, the
// attributes and the value of each of its series, but for the task queue and the
// client's name, which say which of the two workers recorded it.
func panicSeries(t *testing.T, rec *servicetest.Recorder) map[string][]map[string]any {
	t.Helper()

	series := map[string][]map[string]any{}
	for _, m := range rec.Metrics() {
		if m.Name != activityPanicCounter && m.Name != localActivityPanicCounter {
			continue
		}

		sum, ok := m.Data.(metricdata.Sum[int64])
		require.True(t, ok, "%s is no counter", m.Name)

		for _, point := range sum.DataPoints {
			attrs := map[string]any{"value": point.Value}
			for _, kv := range point.Attributes.ToSlice() {
				if kv.Key != "task_queue" && kv.Key != "temporal.name" {
					attrs[string(kv.Key)] = kv.Value.String()
				}
			}

			series[m.Name] = append(series[m.Name], attrs)
		}
	}

	return series
}

// panicLines returns the attributes of every line logged for a panicking
// activity, by message.
func panicLines(rec *servicetest.Recorder) map[string][]string {
	lines := map[string][]string{}
	for _, record := range rec.Records() {
		body := record.Body().AsString()
		if body != "Activity panic." && body != "LocalActivity panic." {
			continue
		}

		var keys []string
		record.WalkAttributes(func(kv attribute.KeyValue) bool {
			keys = append(keys, string(kv.Key))
			return true
		})

		lines[body] = keys
	}

	return lines
}

// errorLines returns how many lines rec holds whose message is message.
func errorLines(rec *servicetest.Recorder, message string) int {
	n := 0
	for _, record := range rec.Records() {
		if record.Body().AsString() == message {
			n++
		}
	}

	return n
}

// TestActivityPanic_KeepsWhatTheSDKRecordsOfIt pins what the worker's
// interceptors keep of what the SDK records of a panicking activity in its own
// recover, which theirs stands in front of: the counters temporal_activity_task_error
// and temporal_local_activity_error, under the same tags and at the same value, and
// the lines "Activity panic." and "LocalActivity panic.". The same workflow runs on
// a worker built by New and on one built by the SDK alone, against one server, each
// under a Service of its own.
func TestActivityPanic_KeepsWhatTheSDKRecordsOfIt(t *testing.T) {
	address := startTemporal(t)

	// A worker built by New, its interceptors installed.
	recoveredSvc, recoveredRec := servicetest.New(t)

	c, w, err := New(t.Context(), recoveredSvc, ConfigWorker{
		Client:    ConfigClient{Address: address},
		TaskQueue: "panics-recovered",
	})
	require.NoError(t, err)

	w.RegisterActivity(NewActivity[string, string]("panicky", workflow.ActivityOptions{StartToCloseTimeout: time.Minute}), func(context.Context, string) (string, error) {
		panic(panicValue)
	})
	w.RegisterWorkflow(NewWorkflow[string, string]("panicking-workflow"), panickingWorkflow)

	require.NoError(t, recoveredSvc.Start(t.Context()))
	t.Cleanup(func() { _ = recoveredSvc.Stop(context.Background()) })

	// A worker built by the SDK alone, on a client built by Connect: the SDK's own
	// recover.
	sdkSvc, sdkRec := servicetest.New(t)

	sdkClient, err := Connect(t.Context(), sdkSvc, ConfigClient{Address: address})
	require.NoError(t, err)

	sdkWorker := worker.New(sdkClient.client, "panics-unrecovered", worker.Options{})
	sdkWorker.RegisterActivityWithOptions(func(context.Context, string) (string, error) {
		panic(panicValue)
	}, activity.RegisterOptions{Name: "panicky"})
	sdkWorker.RegisterWorkflowWithOptions(panickingWorkflow, workflow.RegisterOptions{Name: "panicking-workflow"})
	require.NoError(t, sdkWorker.Start())
	t.Cleanup(sdkWorker.Stop)

	for _, run := range []struct {
		client *Client
		queue  string
	}{
		{client: c, queue: "panics-recovered"},
		{client: sdkClient, queue: "panics-unrecovered"},
	} {
		started, err := run.client.client.ExecuteWorkflow(t.Context(), client.StartWorkflowOptions{ID: run.queue, TaskQueue: run.queue}, "panicking-workflow", "input")
		require.NoError(t, err)

		var result string
		require.NoError(t, started.Get(t.Context(), &result))
		require.Equal(t, "done", result)
	}

	sdk := panicSeries(t, sdkRec)
	require.Len(t, sdk[activityPanicCounter], 1, "the SDK recorded no panic of an activity: the comparison would hold nothing")
	require.Len(t, sdk[localActivityPanicCounter], 1, "the SDK recorded no panic of a local activity: the comparison would hold nothing")

	recovered := panicSeries(t, recoveredRec)
	assert.Equal(t, sdk, recovered, "the counters of a panic differ from the SDK's own")

	sdkLines, recoveredLines := panicLines(sdkRec), panicLines(recoveredRec)
	assert.Equal(t, []string{"Activity panic.", "LocalActivity panic."}, sortedKeys(sdkLines))
	assert.Equal(t, sortedKeys(sdkLines), sortedKeys(recoveredLines), "the lines of a panic differ from the SDK's own")

	// The SDK logs "Activity error." for an activity that returned an error, which
	// the recovered panic is, and never for one that panicked in its own recover:
	// a panic is one line, under either worker.
	assert.Equal(t, errorLines(sdkRec, "Activity error."), errorLines(recoveredRec, "Activity error."), "a recovered panic is logged as two events")

	for message, keys := range recoveredLines {
		for _, key := range []string{"WorkflowID", "RunID", "ActivityType", "Attempt", "exception.message"} {
			assert.Contains(t, keys, key, "%q", message)
		}
	}
}

// sortedKeys returns the keys of m, sorted.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
