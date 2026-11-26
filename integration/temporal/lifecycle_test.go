package temporal

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/service/servicetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// TestReturnedHandleCarriesNoLifecycle pins the invariant: what Connect and New
// hand back must not implement integration.Dependency or integration.Server, so
// that no type assertion can reach Close, Start or Stop. The Service owns the
// lifecycle of the connection and of the worker.
func TestReturnedHandleCarriesNoLifecycle(t *testing.T) {
	for _, handle := range []any{(*Client)(nil), (*Worker)(nil)} {
		_, isDependency := handle.(integration.Dependency)
		assert.False(t, isDependency, "%T must not implement integration.Dependency", handle)

		_, isServer := handle.(integration.Server)
		assert.False(t, isServer, "%T must not implement integration.Server", handle)
	}

	// The adapters, which are what actually get registered, do implement them.
	var registered any = (*clientConnection)(nil)
	_, isDependency := registered.(integration.Dependency)
	assert.True(t, isDependency)

	registered = (*serverConnection)(nil)
	_, isServer := registered.(integration.Server)
	assert.True(t, isServer)
}

// newLazyServerConnection returns a serverConnection over a worker whose client
// has not connected to anything, which is enough for a lifecycle that never
// reaches the server.
func newLazyServerConnection(t *testing.T) *serverConnection {
	t.Helper()

	c, err := client.NewLazyClient(client.Options{HostPort: "127.0.0.1:1"})
	require.NoError(t, err)
	t.Cleanup(c.Close)

	conn := newServerConnection()
	conn.worker = worker.New(c, "lifecycle", worker.Options{OnFatalError: conn.fail})

	return conn
}

// TestStartAfterStop pins the defect that crashed the process: the SDK's worker
// panics with "attempted to start a worker that has been stopped before" when
// started after Stop. The Service starts each server on a goroutine of its own and
// stops it from another, so a Service torn down before that goroutine was
// scheduled — Run with a context already cancelled, a SIGTERM during startup —
// called Stop first and Start second.
//
// Start after Stop must return, without starting anything, and without panicking.
func TestStartAfterStop(t *testing.T) {
	conn := newLazyServerConnection(t)

	require.NoError(t, conn.Stop(t.Context()))

	done := make(chan error, 1)
	assert.NotPanics(t, func() {
		go func() { done <- conn.Start(t.Context()) }()

		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Start after Stop did not return")
		}
	})

	assert.Error(t, conn.CheckHealth(t.Context()), "a worker that never started is not healthy")
}

// TestStopWithoutStart pins that Stop on a worker that never started does
// nothing, and in particular does not touch the SDK's worker — which would make
// any later Start panic.
func TestStopWithoutStart(t *testing.T) {
	conn := newLazyServerConnection(t)

	require.NoError(t, conn.Stop(t.Context()))
	require.NoError(t, conn.Stop(t.Context()), "Stop is idempotent")

	assert.False(t, conn.started)
}

// TestStopHonoursItsBudget pins that Stop returns when ctx is done, even while the
// worker is still stopping: a StopTimeout longer than the Service's budget must
// not hold the Service's teardown past it.
func TestStopHonoursItsBudget(t *testing.T) {
	conn := newServerConnection()

	// A Start in progress holds the lock Stop needs, the way the SDK's own Start
	// does while it reaches the server.
	conn.mu.Lock()
	t.Cleanup(conn.mu.Unlock)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := conn.Stop(ctx)

	assert.ErrorContains(t, err, "Worker did not stop within the shutdown budget")
	assert.Less(t, time.Since(start), 5*time.Second)
}

// TestFatalErrorEndsStart pins that a worker stopping on a fatal error — a task
// queue it is not allowed to poll, a namespace deleted under it — ends Start with
// that error, which ends the Service, rather than leaving a Service that reports
// itself alive with a worker that is not polling.
func TestFatalErrorEndsStart(t *testing.T) {
	conn := newServerConnection()
	conn.started = true
	conn.running.Store(true)

	conn.fail(assert.AnError)

	assert.ErrorIs(t, conn.wait(), assert.AnError)
	assert.Error(t, conn.CheckHealth(t.Context()))

	// The SDK reports a fatal error once, but nothing must block if it reported
	// another after Start returned.
	assert.NotPanics(t, func() { conn.fail(assert.AnError) })
}

// TestTeardownWithoutStart is the defect TestStartAfterStop pins, reached through
// the Service: stopping a Service that never started must return, and return
// within its budget.
func TestTeardownWithoutStart(t *testing.T) {
	address := startTemporal(t)
	svc, _ := servicetest.New(t)

	_, _, err := New(t.Context(), svc, ConfigWorker{
		Client:    ConfigClient{Address: address},
		TaskQueue: "lifecycle",
	})
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- svc.Stop(context.Background()) }()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Stop(ctx) on a Service that never started did not return")
	}
}

// TestRunWithCancelledContext is the same defect reached through the path an
// application actually takes: Run starts the servers and, finding the context
// already done, goes straight to teardown while the worker's goroutine may not
// have started it yet. It used to panic, which no recover in the application can
// catch: the panic is on a goroutine the Service owns.
func TestRunWithCancelledContext(t *testing.T) {
	address := startTemporal(t)

	for range 3 {
		svc, _ := servicetest.New(t)

		_, w, err := New(t.Context(), svc, ConfigWorker{
			Client:    ConfigClient{Address: address},
			TaskQueue: "lifecycle",
		})
		require.NoError(t, err)

		w.RegisterWorkflow(goldenWorkflow, func(ctx workflow.Context, input string) (string, error) {
			return input, nil
		})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		done := make(chan error, 1)
		go func() { done <- svc.Run(ctx) }()

		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Run(cancelled ctx) did not return")
		}
	}
}

// TestStopLetsRunningActivitiesFinish pins that stopping the worker gives the
// activities it is running ConfigWorker.StopTimeout to finish. The SDK's own
// default is zero, which cancelled every running activity the moment the Service
// was asked to stop.
func TestStopLetsRunningActivitiesFinish(t *testing.T) {
	address := startTemporal(t)
	svc, _ := servicetest.New(t)

	c, w, err := New(t.Context(), svc, ConfigWorker{
		Client:    ConfigClient{Address: address},
		TaskQueue: "draining",
	})
	require.NoError(t, err)

	slow := NewActivity[string, string]("slow-activity", workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})

	var (
		started   = make(chan struct{})
		finished  atomic.Bool
		cancelled atomic.Bool
	)

	w.RegisterWorkflow(goldenWorkflow, func(ctx workflow.Context, input string) (string, error) {
		return slow.Execute(ctx, input).Await(ctx)
	})

	w.RegisterActivity(slow, func(ctx context.Context, input string) (string, error) {
		close(started)

		select {
		case <-time.After(time.Second):
			finished.Store(true)
			return input, nil
		case <-ctx.Done():
			cancelled.Store(true)
			return "", ctx.Err()
		}
	})

	require.NoError(t, svc.Start(t.Context()))

	_, err = c.ExecuteWorkflow(t.Context(), goldenWorkflow, client.StartWorkflowOptions{
		ID:        "draining",
		TaskQueue: "draining",
	}, "hello")
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("the activity never started")
	}

	require.NoError(t, svc.Stop(context.Background()))

	assert.False(t, cancelled.Load(), "the running activity was cancelled instead of drained")
	assert.True(t, finished.Load(), "the running activity did not finish before Stop returned")
}

// TestWorkerClientOutlivesTheWorker pins that stopping the worker leaves the
// client it polls with open. Another server — a REST API still draining its
// requests — may be starting workflows through the Client New returned, and the
// Service closes that client only once every server has drained.
func TestWorkerClientOutlivesTheWorker(t *testing.T) {
	address := startTemporal(t)
	svc, _ := servicetest.New(t)

	cfg := ConfigWorker{Client: ConfigClient{Address: address}, TaskQueue: "outlive"}
	require.NoError(t, cfg.sanitize())

	c, err := dial(t.Context(), svc, &cfg.Client, cfg.name(), "config", "client")
	require.NoError(t, err)
	t.Cleanup(c.Close)

	conn := newServerConnection()
	conn.worker = worker.New(c, cfg.TaskQueue, workerOptions(&cfg, conn, nil))
	conn.worker.RegisterWorkflowWithOptions(func(workflow.Context, string) (string, error) {
		return "", nil
	}, workflow.RegisterOptions{Name: goldenWorkflow.Name()})

	done := make(chan error, 1)
	go func() { done <- conn.Start(t.Context()) }()

	require.Eventually(t, func() bool { return conn.CheckHealth(t.Context()) == nil },
		10*time.Second, 10*time.Millisecond, "the worker did not start")

	require.NoError(t, conn.Stop(t.Context()))
	require.NoError(t, <-done)

	assert.NoError(t, checkHealth(t.Context(), c), "the worker's Stop closed the client it polls with")
}

func TestWorkerOptions(t *testing.T) {
	cfg := ConfigWorker{
		TaskQueue:                    "orders",
		MaxConcurrentActivities:      4,
		MaxConcurrentLocalActivities: 6,
		MaxConcurrentWorkflowTasks:   8,
		WorkerActivitiesPerSecond:    10,
		TaskQueueActivitiesPerSecond: 20,
		EnableSessionWorker:          true,
		DisableWorkflowWorker:        true,
	}
	require.NoError(t, cfg.sanitize())

	opts := workerOptions(&cfg, newServerConnection(), nil)

	assert.Equal(t, 25*time.Second, opts.WorkerStopTimeout, "the SDK's default of zero cancels running activities on Stop")
	assert.Equal(t, 4, opts.MaxConcurrentActivityExecutionSize)
	assert.Equal(t, 6, opts.MaxConcurrentLocalActivityExecutionSize)
	assert.Equal(t, 8, opts.MaxConcurrentWorkflowTaskExecutionSize)
	assert.Equal(t, 10.0, opts.WorkerActivitiesPerSecond)
	assert.Equal(t, 20.0, opts.TaskQueueActivitiesPerSecond)
	assert.True(t, opts.EnableSessionWorker)
	assert.True(t, opts.DisableWorkflowWorker)
	assert.NotNil(t, opts.OnFatalError)
	assert.Nil(t, opts.SysInfoProvider, "system information is opt-in")

	require.Len(t, opts.Interceptors, 1)
	assert.IsType(t, &recoverInterceptor{}, opts.Interceptors[0], "an activity that panics is recovered")
}

// A concurrency limit left unset is handed to the SDK as zero, which it reads as
// its own default.
func TestWorkerOptions_UnsetConcurrencyLimitsKeepTheSDKDefaults(t *testing.T) {
	cfg := ConfigWorker{TaskQueue: "orders"}
	require.NoError(t, cfg.sanitize())

	opts := workerOptions(&cfg, newServerConnection(), nil)

	assert.Zero(t, opts.MaxConcurrentActivityExecutionSize)
	assert.Zero(t, opts.MaxConcurrentLocalActivityExecutionSize)
	assert.Zero(t, opts.MaxConcurrentWorkflowTaskExecutionSize)
	assert.False(t, opts.DisableWorkflowWorker, "a worker polls workflow tasks unless told not to")
}

// TestWorkerRunsNoMoreActivitiesAtOnceThanItsLimit pins that
// ConfigWorker.MaxConcurrentActivities is the number of activities a worker runs
// at once: with eight scheduled together and a limit of three, three are running
// before any is let go, and never a fourth beside them.
func TestWorkerRunsNoMoreActivitiesAtOnceThanItsLimit(t *testing.T) {
	const (
		limit     = 3
		scheduled = 8
	)

	address := startTemporal(t)
	svc, _ := servicetest.New(t)

	// The least workflow tasks a worker may run at once beside it: the workflow
	// below still runs to its end.
	c, w, err := New(t.Context(), svc, ConfigWorker{
		Client:                     ConfigClient{Address: address},
		TaskQueue:                  "bounded",
		MaxConcurrentActivities:    limit,
		MaxConcurrentWorkflowTasks: 2,
	})
	require.NoError(t, err)

	held := NewActivity[int, int]("held-activity", workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})

	var (
		running atomic.Int32
		peak    atomic.Int32
		release = make(chan struct{})
	)

	// Whatever ends the test lets the activities go, so that a failure does not
	// leave the worker waiting for them when it stops.
	releaseAll := sync.OnceFunc(func() { close(release) })

	w.RegisterActivity(held, func(ctx context.Context, input int) (int, error) {
		now := running.Add(1)
		defer running.Add(-1)

		for {
			seen := peak.Load()
			if now <= seen || peak.CompareAndSwap(seen, now) {
				break
			}
		}

		select {
		case <-release:
			return input, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})

	fanOut := NewWorkflow[int, int]("fan-out-workflow")
	w.RegisterWorkflow(fanOut, func(ctx workflow.Context, count int) (int, error) {
		runs := make([]*ActivityRun[int], 0, count)
		for i := range count {
			runs = append(runs, held.Execute(ctx, i))
		}

		var done int
		for _, run := range runs {
			if _, err := run.Await(ctx); err != nil {
				return done, err
			}

			done++
		}

		return done, nil
	})

	require.NoError(t, svc.Start(t.Context()))
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })
	t.Cleanup(releaseAll)

	run, err := c.ExecuteWorkflow(t.Context(), fanOut, client.StartWorkflowOptions{ID: "bounded", TaskQueue: "bounded"}, scheduled)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return running.Load() == limit },
		30*time.Second, 10*time.Millisecond, "the worker never ran as many activities as its limit allows")

	// Five more are scheduled and waiting: time for a worker that ignored its
	// limit to start one.
	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, int32(limit), running.Load(), "an activity started beside the ones the limit allows")

	releaseAll()

	done, err := run.Await(t.Context())
	require.NoError(t, err)
	assert.Equal(t, scheduled, done)
	assert.Equal(t, int32(limit), peak.Load(), "the most activities that ran at once")
}
