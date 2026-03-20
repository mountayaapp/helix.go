package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	internallog "github.com/mountayaapp/helix.go/internal/telemetry/log"
	internaltrace "github.com/mountayaapp/helix.go/internal/telemetry/trace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// Disable OpenTelemetry SDK globally for tests to avoid creating real exporters.
	os.Setenv("OTEL_SDK_DISABLED", "true")
}

type mockServer struct {
	name      string
	started   atomic.Bool
	stopped   atomic.Bool
	startErr  error
	stopErr   error
	statusVal int
	statusErr error
}

func (s *mockServer) Name() string { return s.name }
func (s *mockServer) Start(_ context.Context) error {
	s.started.Store(true)
	if s.startErr != nil {
		return s.startErr
	}

	// Block until stopped.
	for !s.stopped.Load() {
		time.Sleep(5 * time.Millisecond)
	}

	return nil
}
func (s *mockServer) Stop(_ context.Context) error {
	s.stopped.Store(true)
	return s.stopErr
}
func (s *mockServer) Status(_ context.Context) (int, error) {
	return s.statusVal, s.statusErr
}

type mockDep struct {
	name      string
	closed    atomic.Bool
	closeErr  error
	statusVal int
	statusErr error
}

func (d *mockDep) Name() string                          { return d.name }
func (d *mockDep) Close(_ context.Context) error         { d.closed.Store(true); return d.closeErr }
func (d *mockDep) Status(_ context.Context) (int, error) { return d.statusVal, d.statusErr }

type slowDep struct {
	name  string
	delay time.Duration
}

func (d *slowDep) Name() string                  { return d.name }
func (d *slowDep) Close(_ context.Context) error { return nil }
func (d *slowDep) Status(ctx context.Context) (int, error) {
	select {
	case <-time.After(d.delay):
		return http.StatusOK, nil
	case <-ctx.Done():
		return http.StatusServiceUnavailable, ctx.Err()
	}
}

// newTestService resets the singleton guard and creates a Service with tracing
// and logging disabled. Additional options can be passed to override defaults.
func newTestService(t *testing.T, opts ...Option) *Service {
	t.Helper()
	serviceGuard = sync.Once{}
	svc, err := New(opts...)
	require.NoError(t, err)
	return svc
}

func TestNew_Defaults(t *testing.T) {
	svc := newTestService(t)

	assert.NotNil(t, svc.logger)
	assert.NotNil(t, svc.tracer)
	assert.NotNil(t, svc.cloud)
	assert.Equal(t, stateCreated, svc.state)
	assert.Equal(t, 30*time.Second, svc.shutdownTimeout)
}

func TestNew_WithOptions(t *testing.T) {
	svc := newTestService(t, WithShutdownTimeout(10*time.Second))

	assert.Equal(t, 10*time.Second, svc.shutdownTimeout)
}

func TestNew_Twice(t *testing.T) {
	_ = newTestService(t)

	_, err := New()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "only one instance is allowed")
}

func TestNew_DetectsCloudProvider(t *testing.T) {
	svc := newTestService(t)

	assert.NotNil(t, svc.cloud)
	assert.NotEmpty(t, svc.cloud.name())
}

func TestNew_DefaultSignals(t *testing.T) {
	svc := newTestService(t)

	assert.Len(t, svc.signals, 2)
	assert.Contains(t, svc.signals, os.Signal(syscall.SIGINT))
	assert.Contains(t, svc.signals, os.Signal(syscall.SIGTERM))
}

func TestServe_Success(t *testing.T) {
	svc := newTestService(t)
	srv := &mockServer{name: "test-server"}

	err := Serve(svc, srv)

	assert.NoError(t, err)
	assert.Equal(t, srv, svc.server)
}

func TestServe_NilServer(t *testing.T) {
	svc := newTestService(t)

	err := Serve(svc, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not be nil")
}

func TestServe_EmptyName(t *testing.T) {
	svc := newTestService(t)

	err := Serve(svc, &mockServer{name: ""})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name must be set")
}

func TestServe_Twice(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "first"})

	err := Serve(svc, &mockServer{name: "second"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already been registered")
}

func TestServe_AfterStart(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv"})
	svc.state = stateStarted

	err := Serve(svc, &mockServer{name: "second"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already been started")
}

func TestServe_AfterStop(t *testing.T) {
	svc := newTestService(t)
	svc.state = stateStopped

	err := Serve(svc, &mockServer{name: "srv"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already been stopped")
}

func TestAttach_Success(t *testing.T) {
	svc := newTestService(t)
	dep := &mockDep{name: "test-dep"}

	err := Attach(svc, dep)

	assert.NoError(t, err)
	assert.Len(t, svc.dependencies, 1)
}

func TestAttach_MultipleDeps(t *testing.T) {
	svc := newTestService(t)

	Attach(svc, &mockDep{name: "dep-1"})
	Attach(svc, &mockDep{name: "dep-2"})
	Attach(svc, &mockDep{name: "dep-3"})

	assert.Len(t, svc.dependencies, 3)
}

func TestAttach_NilDep(t *testing.T) {
	svc := newTestService(t)

	err := Attach(svc, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not be nil")
}

func TestAttach_EmptyName(t *testing.T) {
	svc := newTestService(t)

	err := Attach(svc, &mockDep{name: ""})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name must be set")
}

func TestAttach_DuplicateNamesAllowed(t *testing.T) {
	svc := newTestService(t)

	err1 := Attach(svc, &mockDep{name: "dep"})
	err2 := Attach(svc, &mockDep{name: "dep"})

	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.Len(t, svc.dependencies, 2)
}

func TestAttach_AfterStart(t *testing.T) {
	svc := newTestService(t)
	svc.state = stateStarted

	err := Attach(svc, &mockDep{name: "dep"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already been started")
}

func TestAttach_AfterStop(t *testing.T) {
	svc := newTestService(t)
	svc.state = stateStopped

	err := Attach(svc, &mockDep{name: "dep"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already been stopped")
}

func TestStart_NoServer(t *testing.T) {
	svc := newTestService(t)

	err := svc.Start(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must have a server registered")
}

func TestStart_ServerError(t *testing.T) {
	svc := newTestService(t)
	srv := &mockServer{
		name:     "failing",
		startErr: errors.New("bind: address already in use"),
	}
	Serve(svc, srv)

	err := svc.Start(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bind: address already in use")
	assert.Equal(t, stateCreated, svc.state, "state should remain stateCreated on failure")
}

func TestStart_AlreadyStarted(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv"})
	svc.state = stateStarted

	err := svc.Start(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already been started")
}

func TestStart_AfterStop(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv"})
	svc.state = stateStopped

	err := svc.Start(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already been stopped")
}

func TestStart_SignalTriggersGracefulReturn(t *testing.T) {
	svc := newTestService(t, WithSignals(syscall.SIGUSR1))
	srv := &mockServer{name: "srv"}
	Serve(svc, srv)

	done := make(chan error, 1)
	go func() {
		done <- svc.Start(t.Context())
	}()

	// Wait until the server has started, then send signal.
	for !srv.started.Load() {
		time.Sleep(5 * time.Millisecond)
	}

	p, _ := os.FindProcess(os.Getpid())
	p.Signal(syscall.SIGUSR1)

	err := <-done
	assert.NoError(t, err)
	assert.Equal(t, stateStarted, svc.state)

	// Verify Stop works after Start.
	err = svc.Stop(t.Context())
	assert.NoError(t, err)
	assert.Equal(t, stateStopped, svc.state)
}

func TestStop_Success(t *testing.T) {
	svc := newTestService(t)
	srv := &mockServer{name: "srv"}
	dep := &mockDep{name: "dep"}
	Serve(svc, srv)
	Attach(svc, dep)
	svc.state = stateStarted

	err := svc.Stop(t.Context())

	assert.NoError(t, err)
	assert.True(t, srv.stopped.Load())
	assert.True(t, dep.closed.Load())
	assert.Equal(t, stateStopped, svc.state)
}

func TestStop_NotStarted(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv"})

	err := svc.Stop(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has not been started yet")
}

func TestStop_Twice(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv"})
	svc.state = stateStarted

	err := svc.Stop(t.Context())
	assert.NoError(t, err)

	err = svc.Stop(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already been stopped")
}

func TestStop_NoDeps(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv"})
	svc.state = stateStarted

	err := svc.Stop(t.Context())

	assert.NoError(t, err)
	assert.Equal(t, stateStopped, svc.state)
}

func TestStop_ServerError(t *testing.T) {
	svc := newTestService(t)
	srv := &mockServer{name: "srv", stopErr: errors.New("stop failed")}
	Serve(svc, srv)
	svc.state = stateStarted

	err := svc.Stop(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stop failed")
}

func TestStop_DependencyError(t *testing.T) {
	svc := newTestService(t)
	srv := &mockServer{name: "srv"}
	dep := &mockDep{name: "dep", closeErr: errors.New("close failed")}
	Serve(svc, srv)
	Attach(svc, dep)
	svc.state = stateStarted

	err := svc.Stop(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "close failed")
}

func TestStop_ServerAndDependencyErrors(t *testing.T) {
	svc := newTestService(t)
	srv := &mockServer{name: "srv", stopErr: errors.New("server stop failed")}
	dep := &mockDep{name: "dep", closeErr: errors.New("dep close failed")}
	Serve(svc, srv)
	Attach(svc, dep)
	svc.state = stateStarted

	err := svc.Stop(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "server stop failed")
	assert.Contains(t, err.Error(), "dep close failed")
}

func TestStop_MultipleDependencyErrors(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv"})
	Attach(svc, &mockDep{name: "dep-1", closeErr: errors.New("err-1")})
	Attach(svc, &mockDep{name: "dep-2", closeErr: errors.New("err-2")})
	svc.state = stateStarted

	err := svc.Stop(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "err-1")
	assert.Contains(t, err.Error(), "err-2")
}

func TestStop_ClosesAllDependencies(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv"})
	deps := make([]*mockDep, 5)
	for i := range deps {
		deps[i] = &mockDep{name: fmt.Sprintf("dep-%d", i)}
		Attach(svc, deps[i])
	}
	svc.state = stateStarted

	svc.Stop(t.Context())

	for i, dep := range deps {
		assert.True(t, dep.closed.Load(), "dep-%d should be closed", i)
	}
}

func TestStop_DependencyErrorStillStopsServer(t *testing.T) {
	svc := newTestService(t)
	srv := &mockServer{name: "srv"}
	dep := &mockDep{name: "dep", closeErr: errors.New("close failed")}
	Serve(svc, srv)
	Attach(svc, dep)
	svc.state = stateStarted

	err := svc.Stop(t.Context())

	assert.Error(t, err)
	assert.True(t, srv.stopped.Load())
	assert.True(t, dep.closed.Load())
}

func TestStatus_AllHealthy(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv", statusVal: http.StatusOK})
	Attach(svc, &mockDep{name: "dep", statusVal: http.StatusOK})

	status, err := svc.Status(t.Context())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestStatus_OneUnhealthy(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv", statusVal: http.StatusOK})
	Attach(svc, &mockDep{name: "dep", statusVal: http.StatusServiceUnavailable})

	status, err := svc.Status(t.Context())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

func TestStatus_WithError(t *testing.T) {
	svc := newTestService(t)
	Attach(svc, &mockDep{
		name:      "failing-dep",
		statusVal: http.StatusServiceUnavailable,
		statusErr: errors.New("connection refused"),
	})

	status, err := svc.Status(t.Context())

	assert.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

func TestStatus_NoIntegrations(t *testing.T) {
	svc := newTestService(t)

	status, err := svc.Status(t.Context())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestStatus_ServerOnly(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv", statusVal: http.StatusOK})

	status, err := svc.Status(t.Context())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestStatus_DepsOnly(t *testing.T) {
	svc := newTestService(t)
	Attach(svc, &mockDep{name: "dep-1", statusVal: http.StatusOK})
	Attach(svc, &mockDep{name: "dep-2", statusVal: http.StatusOK})

	status, err := svc.Status(t.Context())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}

func TestStatus_HighestWins(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{name: "srv", statusVal: http.StatusOK})
	Attach(svc, &mockDep{name: "dep-ok", statusVal: http.StatusOK})
	Attach(svc, &mockDep{name: "dep-bad", statusVal: http.StatusBadGateway})

	status, err := svc.Status(t.Context())

	assert.NoError(t, err)
	assert.Equal(t, http.StatusBadGateway, status)
}

func TestStatus_MultipleErrors(t *testing.T) {
	svc := newTestService(t)
	Attach(svc, &mockDep{
		name:      "dep-1",
		statusVal: http.StatusServiceUnavailable,
		statusErr: errors.New("dep-1 down"),
	})
	Attach(svc, &mockDep{
		name:      "dep-2",
		statusVal: http.StatusServiceUnavailable,
		statusErr: errors.New("dep-2 down"),
	})

	status, err := svc.Status(t.Context())

	assert.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

func TestStatus_ServerErrorWithHealthyDeps(t *testing.T) {
	svc := newTestService(t)
	Serve(svc, &mockServer{
		name:      "srv",
		statusVal: http.StatusServiceUnavailable,
		statusErr: errors.New("server unhealthy"),
	})
	Attach(svc, &mockDep{name: "dep", statusVal: http.StatusOK})

	status, err := svc.Status(t.Context())

	assert.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

func TestStatus_ConcurrentSafety(t *testing.T) {
	svc := newTestService(t)
	for i := 0; i < 10; i++ {
		Attach(svc, &mockDep{name: fmt.Sprintf("dep-%d", i), statusVal: http.StatusOK})
	}

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, err := svc.Status(t.Context())
			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, status)
		}()
	}

	wg.Wait()
}

func TestStatus_TimeoutOnSlowDependency(t *testing.T) {
	svc := newTestService(t)
	Attach(svc, &slowDep{name: "slow", delay: 10 * time.Second})

	start := time.Now()
	status, err := svc.Status(context.Background())
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Less(t, elapsed, 6*time.Second, "should return within statusTimeout")
}

func TestStatus_RespectsExistingDeadline(t *testing.T) {
	svc := newTestService(t)
	Attach(svc, &slowDep{name: "slow", delay: 10 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	status, err := svc.Status(ctx)
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Less(t, elapsed, 2*time.Second, "should respect caller's tighter deadline")
}

func TestContext_EnrichesWithLoggerAndTracer(t *testing.T) {
	svc := newTestService(t)

	ctx := Context(svc, t.Context())

	assert.NotNil(t, internallog.LoggerFromContext(ctx))
	assert.NotNil(t, internaltrace.TracerFromContext(ctx))
	assert.Same(t, svc.logger, internallog.LoggerFromContext(ctx))
	assert.Same(t, svc.tracer, internaltrace.TracerFromContext(ctx))
}

func TestContext_Idempotent(t *testing.T) {
	svc := newTestService(t)

	ctx1 := Context(svc, t.Context())
	ctx2 := Context(svc, ctx1)

	assert.Same(t, internallog.LoggerFromContext(ctx1), internallog.LoggerFromContext(ctx2))
	assert.Same(t, internaltrace.TracerFromContext(ctx1), internaltrace.TracerFromContext(ctx2))
}

func TestContext_PreservesExistingValues(t *testing.T) {
	svc := newTestService(t)

	type customKey struct{}
	ctx := context.WithValue(t.Context(), customKey{}, "preserved")
	ctx = Context(svc, ctx)

	assert.Equal(t, "preserved", ctx.Value(customKey{}))
	assert.NotNil(t, internallog.LoggerFromContext(ctx))
	assert.NotNil(t, internaltrace.TracerFromContext(ctx))
}

func TestTracerProvider(t *testing.T) {
	svc := newTestService(t)

	assert.NotNil(t, TracerProvider(svc))
}

func TestLoggerProvider(t *testing.T) {
	svc := newTestService(t)

	// Nop logger has nil provider.
	assert.Nil(t, LoggerProvider(svc))
}

func TestWithSignals(t *testing.T) {
	svc := newTestService(t, WithSignals(syscall.SIGUSR2))

	assert.Equal(t, []os.Signal{syscall.SIGUSR2}, svc.signals)
}

func TestWithShutdownTimeout(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		svc := newTestService(t, WithShutdownTimeout(0))
		assert.Equal(t, time.Duration(0), svc.shutdownTimeout)
	})

	t.Run("custom", func(t *testing.T) {
		svc := newTestService(t, WithShutdownTimeout(5*time.Minute))
		assert.Equal(t, 5*time.Minute, svc.shutdownTimeout)
	})
}

func TestNew_OTELSDKDisabled(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")

	svc := newTestService(t)

	assert.NotNil(t, svc.tracer)
	assert.NotNil(t, svc.logger)

	// Nop logger has nil provider.
	assert.Nil(t, LoggerProvider(svc))
}

func TestNew_OTELSDKDisabled_CaseInsensitive(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "True")

	svc := newTestService(t)
	assert.Nil(t, LoggerProvider(svc))
}

func TestNew_OTELSDKDisabled_False(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	svc := newTestService(t)

	assert.NotNil(t, svc.tracer)
	assert.NotNil(t, svc.logger)
	assert.NotNil(t, TracerProvider(svc))
	assert.NotNil(t, LoggerProvider(svc))
}

func TestNew_OTELSDKEnabled_ExporterNone(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	svc := newTestService(t)

	assert.NotNil(t, TracerProvider(svc))
	assert.NotNil(t, LoggerProvider(svc))

	ctx := Context(svc, t.Context())
	assert.NotNil(t, internallog.LoggerFromContext(ctx))
	assert.NotNil(t, internaltrace.TracerFromContext(ctx))
}

func TestNew_OTELSDKEnabled_ExporterConsole(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "console")
	t.Setenv("OTEL_LOGS_EXPORTER", "console")

	svc := newTestService(t)

	assert.NotNil(t, TracerProvider(svc))
	assert.NotNil(t, LoggerProvider(svc))
}

func TestNew_OTELSDKEnabled_DefaultProtocolGRPC(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "")

	_ = newTestService(t)

	assert.Equal(t, "grpc", os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
}

func TestNew_OTELSDKEnabled_ProtocolHTTPExplicit(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_LOGS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")

	svc := newTestService(t)

	assert.NotNil(t, TracerProvider(svc))
	assert.NotNil(t, LoggerProvider(svc))
}

func TestNew_OTELSDKDisabled_DoesNotSetProtocol(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "")

	_ = newTestService(t)

	assert.Empty(t, os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
}

func TestNew_OTELLogLevel(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	levels := []string{"debug", "info", "warn", "error"}
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			t.Setenv("OTEL_LOG_LEVEL", level)
			svc := newTestService(t)
			assert.NotNil(t, svc.logger)
		})
	}
}

func TestNew_OTELResourceAttributes(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=staging,custom.tag=hello")

	svc := newTestService(t)
	assert.NotNil(t, svc.tracer)
	assert.NotNil(t, svc.logger)
}

func TestNew_OTELServiceName(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")
	t.Setenv("OTEL_SERVICE_NAME", "my-custom-service")

	svc := newTestService(t)
	assert.NotNil(t, svc.tracer)
	assert.NotNil(t, svc.logger)

	// OTEL_SERVICE_NAME must override the cloud-detected service name.
	var found bool
	for _, kv := range svc.resource.Attributes() {
		if string(kv.Key) == "service.name" {
			assert.Equal(t, "my-custom-service", kv.Value.AsString())
			found = true
			break
		}
	}
	assert.True(t, found, "service.name attribute not found in resource")
}

func TestNew_OTELCustomEndpoint(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_LOGS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:9999")

	svc := newTestService(t)
	assert.NotNil(t, TracerProvider(svc))
	assert.NotNil(t, LoggerProvider(svc))
}

func TestMultipleServices_Sequential(t *testing.T) {
	for range 5 {
		t.Run("", func(t *testing.T) {
			svc := newTestService(t)
			Attach(svc, &mockDep{name: "dep", statusVal: http.StatusOK})

			status, err := svc.Status(t.Context())

			assert.NoError(t, err)
			assert.Equal(t, http.StatusOK, status)
		})
	}
}
