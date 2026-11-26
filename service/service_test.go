package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mountaya/helix.go/internal/telemetry/trace"
	helixlog "github.com/mountaya/helix.go/telemetry/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"
)

// mockServer blocks in Start until Stop is called, in the manner of a real
// server holding a listener open.
type mockServer struct {
	started   atomic.Bool
	stopped   atomic.Bool
	startErr  error
	stopErr   error
	healthErr error
}

func (s *mockServer) Start(ctx context.Context) error {
	s.started.Store(true)
	if s.startErr != nil {
		return s.startErr
	}

	for !s.stopped.Load() {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Millisecond):
		}
	}

	return nil
}

func (s *mockServer) Stop(_ context.Context) error {
	s.stopped.Store(true)
	return s.stopErr
}

func (s *mockServer) CheckHealth(_ context.Context) error { return s.healthErr }

type mockDep struct {
	closed    atomic.Bool
	closeErr  error
	healthErr error
}

func (d *mockDep) Close(_ context.Context) error {
	d.closed.Store(true)
	return d.closeErr
}

func (d *mockDep) CheckHealth(_ context.Context) error { return d.healthErr }

// slowDep takes delay to report health, to exercise the health-check deadline.
type slowDep struct {
	delay time.Duration
}

func (d *slowDep) Close(_ context.Context) error { return nil }

func (d *slowDep) CheckHealth(ctx context.Context) error {
	select {
	case <-time.After(d.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// newTestService creates a Service with telemetry disabled and the process-wide
// OpenTelemetry globals left alone, so that tests do not interfere with one
// another.
func newTestService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("OTEL_SDK_DISABLED", "true")

	svc, err := New(t.Context(), Config{SkipGlobals: true})
	require.NoError(t, err)
	return svc
}

func TestNew_Defaults(t *testing.T) {
	svc := newTestService(t)

	assert.NotNil(t, svc.logger)
	assert.NotNil(t, svc.tracer)
	assert.Equal(t, defaultShutdownTimeout, svc.shutdownTimeout)
}

func TestNew_ShutdownTimeout(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")

	svc, err := New(t.Context(), Config{ShutdownTimeout: time.Second, SkipGlobals: true})
	require.NoError(t, err)
	assert.Equal(t, time.Second, svc.shutdownTimeout)
}

// TestNew_ManyPerProcess is the point of removing the singleton: a failed or
// completed New must not poison the process for every later caller.
func TestNew_ManyPerProcess(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")

	first, err := New(t.Context(), Config{SkipGlobals: true})
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := New(t.Context(), Config{SkipGlobals: true})
	require.NoError(t, err)
	require.NotNil(t, second)

	assert.NotSame(t, first, second)
}

// TestCloudAttributes_AlwaysNameTheService pins the baseline: whatever provider
// is detected, the resource names the service, so that a provider which does not
// — Kubernetes — leaves no service unnamed.
func TestCloudAttributes_AlwaysNameTheService(t *testing.T) {
	var named bool
	for _, attr := range cloudAttributes() {
		if attr.Key == "service.name" && attr.Value.AsString() != "" {
			named = true
		}
	}

	assert.True(t, named)
}

func TestNew_LogLevel(t *testing.T) {
	t.Setenv("OTEL_LOG_LEVEL", "debug")

	assert.Equal(t, zapcore.DebugLevel, logLevel(Config{}), "unset falls back to OTEL_LOG_LEVEL")
	assert.Equal(t, zapcore.WarnLevel, logLevel(Config{LogLevel: helixlog.LevelWarn}))
}

func TestServe_Success(t *testing.T) {
	svc := newTestService(t)

	require.NoError(t, Serve(svc, "public", &mockServer{}))
	assert.Len(t, svc.servers, 1)
}

// TestServe_Multiple covers the shape the single-server restriction forbade: a
// REST API next to a worker.
func TestServe_Multiple(t *testing.T) {
	svc := newTestService(t)

	require.NoError(t, Serve(svc, "public", &mockServer{}))
	require.NoError(t, Serve(svc, "worker", &mockServer{}))
	assert.Len(t, svc.servers, 2)
}

func TestServe_NilServer(t *testing.T) {
	svc := newTestService(t)

	assert.Error(t, Serve(svc, "public", nil))
}

func TestServe_EmptyName(t *testing.T) {
	svc := newTestService(t)

	assert.Error(t, Serve(svc, "", &mockServer{}))
}

func TestServe_DuplicateName(t *testing.T) {
	svc := newTestService(t)

	require.NoError(t, Serve(svc, "public", &mockServer{}))
	assert.Error(t, Serve(svc, "public", &mockServer{}))
}

func TestAttach_Success(t *testing.T) {
	svc := newTestService(t)

	require.NoError(t, Attach(svc, "primary", &mockDep{}))
	assert.Len(t, svc.dependencies, 1)
}

func TestAttach_MultipleDeps(t *testing.T) {
	svc := newTestService(t)

	require.NoError(t, Attach(svc, "primary", &mockDep{}))
	require.NoError(t, Attach(svc, "replica", &mockDep{}))
	assert.Len(t, svc.dependencies, 2)
}

func TestAttach_NilDep(t *testing.T) {
	svc := newTestService(t)

	assert.Error(t, Attach(svc, "primary", nil))
}

func TestAttach_EmptyName(t *testing.T) {
	svc := newTestService(t)

	assert.Error(t, Attach(svc, "", &mockDep{}))
}

// TestAttach_DuplicateName is the reason names moved to the call site: two pools
// of the same kind must be distinguishable.
func TestAttach_DuplicateName(t *testing.T) {
	svc := newTestService(t)

	require.NoError(t, Attach(svc, "primary", &mockDep{}))
	assert.Error(t, Attach(svc, "primary", &mockDep{}))
}

// TestStart_NoServer covers the shape a consumer loop or a batch job has: a
// Service that attached dependencies and registered no server at all. It is legal,
// and Start says so.
func TestStart_NoServer(t *testing.T) {
	svc := newTestService(t)

	assert.NoError(t, svc.Start(t.Context()))
	require.NoError(t, svc.Stop(context.Background()))
}

func TestStart_AlreadyStarted(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Serve(svc, "public", &mockServer{}))

	require.NoError(t, svc.Start(t.Context()))
	assert.Error(t, svc.Start(t.Context()))

	require.NoError(t, svc.Stop(context.Background()))
}

func TestRun_ReturnsOnContextCancellation(t *testing.T) {
	svc := newTestService(t)
	srv := &mockServer{}
	require.NoError(t, Serve(svc, "public", srv))

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	require.NoError(t, svc.Run(ctx))
	assert.True(t, srv.started.Load())
	assert.True(t, srv.stopped.Load())
}

func TestRun_ReturnsServerFailure(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Serve(svc, "public", &mockServer{startErr: errors.New("bind: address in use")}))

	err := svc.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "public")
}

// TestRun_TearsDownOnStartupFailure is the defect D2 describes: the startup
// failure path is the one whose telemetry matters most, and it was the one that
// skipped teardown.
func TestRun_TearsDownOnStartupFailure(t *testing.T) {
	svc := newTestService(t)
	dep := &mockDep{}
	require.NoError(t, Attach(svc, "primary", dep))
	require.NoError(t, Serve(svc, "public", &mockServer{startErr: errors.New("boom")}))

	require.Error(t, svc.Run(t.Context()))

	assert.True(t, dep.closed.Load(), "dependencies must close even when startup failed")
	assert.True(t, svc.stopped, "telemetry must be flushed even when startup failed")
}

// TestRun_NoServerRegistered covers a Service that only holds dependencies: Run
// blocks on the context rather than refusing, and tears down when it is cancelled.
func TestRun_NoServerRegistered(t *testing.T) {
	svc := newTestService(t)
	dep := &mockDep{}
	require.NoError(t, Attach(svc, "primary", dep))

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	require.NoError(t, svc.Run(ctx))

	assert.True(t, dep.closed.Load())
	assert.True(t, svc.stopped)
}

func TestStop_Success(t *testing.T) {
	svc := newTestService(t)
	srv := &mockServer{}
	dep := &mockDep{}
	require.NoError(t, Serve(svc, "public", srv))
	require.NoError(t, Attach(svc, "primary", dep))
	require.NoError(t, svc.Start(t.Context()))

	require.NoError(t, svc.Stop(context.Background()))
	assert.True(t, srv.stopped.Load())
	assert.True(t, dep.closed.Load())
}

// TestStop_NotStarted and TestStop_Twice pin the idempotence that replaced
// requireState: teardown that refuses to run is how telemetry gets lost.
func TestStop_NotStarted(t *testing.T) {
	svc := newTestService(t)

	assert.NoError(t, svc.Stop(t.Context()))
}

func TestStop_Twice(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Serve(svc, "public", &mockServer{}))
	require.NoError(t, svc.Start(t.Context()))

	require.NoError(t, svc.Stop(context.Background()))
	assert.NoError(t, svc.Stop(context.Background()))
}

func TestStop_ServerError(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Serve(svc, "public", &mockServer{stopErr: errors.New("drain failed")}))
	require.NoError(t, svc.Start(t.Context()))

	err := svc.Stop(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "public")
}

func TestStop_DependencyError(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Serve(svc, "public", &mockServer{}))
	require.NoError(t, Attach(svc, "primary", &mockDep{closeErr: errors.New("close failed")}))
	require.NoError(t, svc.Start(t.Context()))

	err := svc.Stop(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "primary")
}

func TestStop_ClosesAllDependencies(t *testing.T) {
	svc := newTestService(t)
	deps := []*mockDep{{}, {}, {}}
	require.NoError(t, Serve(svc, "public", &mockServer{}))
	for i, dep := range deps {
		require.NoError(t, Attach(svc, string(rune('a'+i)), dep))
	}
	require.NoError(t, svc.Start(t.Context()))

	require.NoError(t, svc.Stop(context.Background()))
	for i, dep := range deps {
		assert.True(t, dep.closed.Load(), "dependency %d must be closed", i)
	}
}

func TestStop_DependencyErrorStillClosesOthers(t *testing.T) {
	svc := newTestService(t)
	healthy := &mockDep{}
	require.NoError(t, Serve(svc, "public", &mockServer{}))
	require.NoError(t, Attach(svc, "broken", &mockDep{closeErr: errors.New("close failed")}))
	require.NoError(t, Attach(svc, "healthy", healthy))
	require.NoError(t, svc.Start(t.Context()))

	require.Error(t, svc.Stop(context.Background()))
	assert.True(t, healthy.closed.Load())
}

func TestCheckHealth_AllHealthy(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Serve(svc, "public", &mockServer{}))
	require.NoError(t, Attach(svc, "primary", &mockDep{}))

	assert.NoError(t, svc.CheckHealth(t.Context()))
}

func TestCheckHealth_NoIntegrations(t *testing.T) {
	svc := newTestService(t)

	assert.NoError(t, svc.CheckHealth(t.Context()))
}

func TestCheckHealth_UnhealthyDependency(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Attach(svc, "primary", &mockDep{healthErr: errors.New("connection refused")}))

	err := svc.CheckHealth(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "primary")
}

func TestCheckHealth_UnhealthyServer(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Serve(svc, "public", &mockServer{healthErr: errors.New("not listening")}))

	err := svc.CheckHealth(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "public")
}

func TestCheckHealth_NamesEachFailure(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Attach(svc, "primary", &mockDep{healthErr: errors.New("down")}))
	require.NoError(t, Attach(svc, "replica", &mockDep{healthErr: errors.New("down")}))

	err := svc.CheckHealth(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "primary")
	assert.Contains(t, err.Error(), "replica")
}

func TestCheckHealth_TimeoutOnSlowDependency(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Attach(svc, "slow", &slowDep{delay: 10 * time.Second}))

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	assert.Error(t, svc.CheckHealth(ctx))
}

func TestCheckHealth_ConcurrentSafety(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Serve(svc, "public", &mockServer{}))
	require.NoError(t, Attach(svc, "primary", &mockDep{}))

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			assert.NoError(t, svc.CheckHealth(t.Context()))
		})
	}

	wg.Wait()
}

// TestIntegrationAPI covers the surface an integration package uses to hand
// itself to a Service. It is deliberately not reachable from an end-user's
// handle: that is what keeps lifecycle inside helix.
func TestIntegrationAPI(t *testing.T) {
	svc := newTestService(t)

	assert.NotNil(t, Context(t.Context(), svc))
	assert.NotNil(t, TracerProvider(svc))
	assert.NotNil(t, LoggerProvider(svc))
}

func TestStart_AfterStop(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.Stop(t.Context()))

	assert.Error(t, svc.Start(t.Context()), "a stopped Service must not start servers nothing will stop")
}

func TestDone_ClosedWhenAServerFails(t *testing.T) {
	svc := newTestService(t)
	failure := errors.New("bind: address already in use")
	require.NoError(t, Serve(svc, "api", &mockServer{startErr: failure}))

	require.NoError(t, svc.Start(t.Context()))
	t.Cleanup(func() { svc.Stop(context.Background()) })

	select {
	case <-svc.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done() was not closed after a server failed")
	}

	assert.ErrorIs(t, svc.Err(), failure)
}

func TestDone_ClosedByStop(t *testing.T) {
	svc := newTestService(t)
	assert.NoError(t, svc.Err())

	require.NoError(t, svc.Stop(t.Context()))

	<-svc.Done()
	assert.NoError(t, svc.Err())
}

// exitingServer returns from Start on its own, without an error.
type exitingServer struct{}

func (exitingServer) Start(context.Context) error       { return nil }
func (exitingServer) Stop(context.Context) error        { return nil }
func (exitingServer) CheckHealth(context.Context) error { return nil }

func TestRun_ReturnsWhenAServerExitsCleanly(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, Serve(svc, "worker", exitingServer{}))

	done := make(chan error, 1)
	go func() { done <- svc.Run(t.Context()) }()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() kept blocking after its only server exited")
	}
}

// hungDep never closes until its context ends.
type hungDep struct{}

func (hungDep) Close(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (hungDep) CheckHealth(context.Context) error { return nil }

// keepingExporter is an in-memory span exporter whose spans survive Shutdown,
// which tracetest's own resets.
type keepingExporter struct {
	*tracetest.InMemoryExporter
}

func (keepingExporter) Shutdown(context.Context) error { return nil }

// TestStop_FlushesTelemetryWhenADependencyHangs pins the telemetry reserve: a
// dependency that spends the whole integrations budget must not leave the tracer
// provider shutting down on an expired context, which drops the spans explaining
// what hung.
func TestStop_FlushesTelemetryWhenADependencyHangs(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")

	svc, err := New(t.Context(), Config{SkipGlobals: true, ShutdownTimeout: 400 * time.Millisecond})
	require.NoError(t, err)

	exporter := keepingExporter{tracetest.NewInMemoryExporter()}
	svc.sdkTracerProvider = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(time.Hour)))
	svc.tracer = trace.NewTracerWithProvider(svc.sdkTracerProvider)

	_, span := svc.tracer.Start(t.Context(), oteltrace.SpanKindInternal, "Probe: BeforeStop")
	span.End()

	require.NoError(t, Attach(svc, "hung", hungDep{}))

	assert.Error(t, svc.Stop(context.Background()))
	assert.Len(t, exporter.GetSpans(), 1, "the span must be flushed despite the hung dependency")
}

// countingDep counts its health checks, and holds each one until released.
type countingDep struct {
	calls   atomic.Int32
	release chan struct{}
}

func (d *countingDep) Close(context.Context) error { return nil }

func (d *countingDep) CheckHealth(context.Context) error {
	d.calls.Add(1)
	<-d.release
	return nil
}

// TestCheckHealth_CoalescesConcurrentChecks pins that concurrent readiness probes
// share one check rather than each fanning out to every dependency.
func TestCheckHealth_CoalescesConcurrentChecks(t *testing.T) {
	svc := newTestService(t)
	dep := &countingDep{release: make(chan struct{})}
	require.NoError(t, Attach(svc, "db", dep))

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { assert.NoError(t, svc.CheckHealth(t.Context())) })
	}

	require.Eventually(t, func() bool { return dep.calls.Load() == 1 }, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	close(dep.release)
	wg.Wait()

	assert.Equal(t, int32(1), dep.calls.Load())

	// A check that completed is not reused: the next call checks again.
	require.NoError(t, svc.CheckHealth(t.Context()))
	assert.Equal(t, int32(2), dep.calls.Load())
}
