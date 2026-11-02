package temporal

import (
	"context"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/event"
	"github.com/mountayaapp/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

// goldenWorkflow and goldenActivity are the definitions the golden runs. They are
// declared at package level the way the documentation says handles should be.
var (
	goldenWorkflow = NewWorkflow[string, string]("golden-workflow")
	goldenActivity = NewActivity[string, string]("golden-activity", workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	})
)

// startTemporal brings up a Temporal dev server for the duration of the test and
// returns its host:port.
//
// A real server rather than the SDK's test environment: the spans this package
// emits are produced by the tracing interceptor on the real client and worker
// paths, and the test environment short-circuits both.
//
// Call it *before* servicetest.New, and do not touch the container afterwards.
// testcontainers-go instruments its own Docker API calls with otelhttp against
// the global tracer provider, which servicetest installs; starting the container
// while the Recorder is live puts several dozen Docker API spans in the middle of
// this integration's golden.
func startTemporal(tb testing.TB) string {
	tb.Helper()

	container, err := testcontainers.Run(tb.Context(), "temporalio/temporal:latest",
		testcontainers.WithExposedPorts("7233/tcp"),
		testcontainers.WithCmd("server", "start-dev", "--ip", "0.0.0.0", "--log-level", "error"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("7233/tcp")),
	)
	testcontainers.CleanupContainer(tb, container)
	require.NoError(tb, err)

	endpoint, err := container.PortEndpoint(tb.Context(), "7233/tcp", "")
	require.NoError(tb, err)

	return endpoint
}

// TestTelemetryGolden pins what this integration emits, so that changing how a
// span is produced cannot silently change what is produced. It records span
// names, emitting scope, kind and attribute keys - never values, timings or IDs,
// which differ on every run.
//
// A diff here is not automatically a failure: it is a change to the library's
// observable output, and it has to be an intentional one. Update the golden in
// the same commit that changes the instrumentation, never separately.
func TestTelemetryGolden(t *testing.T) {
	address := startTemporal(t)
	svc, rec := servicetest.New(t)

	c, w, err := New(t.Context(), svc, ConfigWorker{
		Client:    ConfigClient{Address: address},
		Name:      "golden",
		TaskQueue: "golden",
	})
	require.NoError(t, err)

	w.RegisterWorkflow(goldenWorkflow, func(ctx workflow.Context, input string) (string, error) {
		return goldenActivity.Execute(ctx, input).Await(ctx)
	})

	w.RegisterActivity(goldenActivity, func(ctx context.Context, input string) (string, error) {
		return input + "!", nil
	})

	require.NoError(t, svc.Start(t.Context()))

	// The Event an HTTP handler would have set, personal data included.
	ctx := event.NewContext(t.Context(), event.Event{
		ID:        "evt_golden",
		Name:      "golden.requested",
		TenantID:  "tnt_golden",
		UserID:    "usr_golden",
		IP:        "203.0.113.7",
		UserAgent: "Golden/1.0",
	})

	run, err := c.ExecuteWorkflow(ctx, goldenWorkflow, client.StartWorkflowOptions{
		ID:        "golden",
		TaskQueue: "golden",
	}, "hello")
	require.NoError(t, err)

	result, err := run.Await(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "hello!", result)

	// Drain before reading: the worker's spans end on its own goroutines, and
	// stopping is what makes the set deterministic.
	require.NoError(t, svc.Stop(context.WithoutCancel(t.Context())))

	var got []string
	for _, s := range servicetest.Summarize(rec.Spans()) {
		got = append(got, s.String())
	}

	// Four spans for one workflow that calls one activity, and every one of them
	// comes from the Temporal SDK's tracing interceptor rather than from a span
	// site in this package. That is why the contrib tracer stays: it is what makes
	// the workflow-side span identifiers replay-safe and what propagates the trace
	// through Temporal's own headers, neither of which is a naming concern.
	//
	// What this package owns is the vocabulary, and it is the helix one -
	// "Temporal: Run Workflow" rather than the SDK's "RunWorkflow:golden-workflow",
	// with the workflow and activity types as temporal.workflow.type and
	// temporal.activity.type rather than suffixes of the name, and
	// temporal.workflow.run_id rather than the SDK's temporalRunID - down to the
	// instrumentation scope, which is the shared one rather than this package's
	// own.
	//
	// The two server spans are the entry spans of the worker side, and carry what
	// the SDK's tags leave out: the task queue, the attempt, and the Event. The
	// Event is the allow-listed part only - event.id, event.name, event.tenant_id -
	// because that is all the client put in the header: the user ID, the address
	// and the user agent the handler had never leave the client's process.
	want := []string{
		"github.com/mountayaapp/helix.go | client | Temporal: Start Workflow | temporal.name,temporal.namespace,temporal.server.address,temporal.workflow.id,temporal.workflow.type",
		"github.com/mountayaapp/helix.go | client | Temporal: Start Activity | temporal.activity.type,temporal.name,temporal.namespace,temporal.server.address,temporal.workflow.id,temporal.workflow.run_id",
		"github.com/mountayaapp/helix.go | server | Temporal: Run Activity | event.id,event.name,event.tenant_id,temporal.activity.attempt,temporal.activity.id,temporal.activity.type,temporal.name,temporal.namespace,temporal.server.address,temporal.worker.task_queue,temporal.workflow.id,temporal.workflow.run_id,temporal.workflow.type",
		"github.com/mountayaapp/helix.go | server | Temporal: Run Workflow | event.id,event.name,event.tenant_id,temporal.name,temporal.namespace,temporal.server.address,temporal.worker.task_queue,temporal.workflow.attempt,temporal.workflow.id,temporal.workflow.run_id,temporal.workflow.type",
	}

	assert.Equal(t, want, got)

	servicetest.AssertConvention(t, rec.Spans())

	// The instruments are the SDK's own, reported through the Service's meter. They
	// are pinned by a stable subset rather than in full: which of them record
	// depends on timing - an empty poll, a long poll cut short by shutdown - and a
	// golden that depended on it would test the scheduler. The namespace is pinned
	// in full: every one of them is the SDK's temporal_ vocabulary, and none is
	// helix's temporal.operation.duration, which nothing in this package records.
	var metrics []string
	for _, m := range rec.Metrics() {
		metrics = append(metrics, m.Name)
	}

	assert.Subset(t, metrics, []string{
		"temporal_request",
		"temporal_request_latency",
		"temporal_long_request",
		"temporal_long_request_latency",
		"temporal_worker_start",
		"temporal_num_pollers",
		"temporal_workflow_task_execution_latency",
		"temporal_workflow_completed",
		"temporal_workflow_endtoend_latency",
		"temporal_activity_execution_latency",
		"temporal_activity_succeed_endtoend_latency",
	})

	for _, name := range metrics {
		assert.Regexp(t, `^temporal_[a-z_]+$`, name)
	}
}
