package temporal

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"go.temporal.io/sdk/worker"
)

// Ensure *serverConnection complies to the integration.Server type.
var _ integration.Server = (*serverConnection)(nil)

// errWorkerNotRunning is what CheckHealth reports for a worker that is not
// polling: not started yet, stopped, or stopped by a fatal error.
var errWorkerNotRunning = errors.New("worker is not running")

// serverConnection represents a Temporal worker registered as a server.
//
// It is a separate, unexported type rather than methods on Worker for a reason:
// Worker is handed to the end-user so they can register workflows and activities,
// and putting Start and Stop on it would let them drive a lifecycle the Service
// owns.
//
// The SDK's worker cannot be started once it has been stopped: Start panics. The
// Service runs Start on a goroutine of its own and Stop on another, so a Service
// stopped before that goroutine was scheduled — Run with a context already
// cancelled, a SIGTERM during startup — used to call Stop first and crash the
// process when Start followed. The state below is what orders the two.
type serverConnection struct {
	worker worker.Worker

	// stopping is closed by Stop. It is what Start blocks on once the worker
	// polls, since the worker's own Start returns immediately.
	stopping chan struct{}

	// fatal receives the error the worker stopped on by itself, which ends Start.
	// It is buffered: the SDK reports a fatal error once, and must not block on a
	// Start that has already returned.
	fatal chan error

	// running reports whether the worker is polling. It is atomic rather than
	// under mu, so that CheckHealth answers while Start or Stop is waiting on the
	// worker.
	running atomic.Bool

	// mu guards started and stopped, and is held across the worker's own Start so
	// that Stop waits for it rather than racing it.
	mu      sync.Mutex
	started bool
	stopped bool
}

// newServerConnection returns a serverConnection for a worker not yet built. The
// worker is set once built, because its options carry fail as the callback for
// fatal errors.
func newServerConnection() *serverConnection {
	return &serverConnection{
		stopping: make(chan struct{}),
		fatal:    make(chan error, 1),
	}
}

// Start starts the worker polling, and blocks until Stop is called or the worker
// stops on a fatal error, which it returns.
//
// It is not tied to ctx: the worker stops when the Service stops it, which is what
// lets running activities finish within ConfigWorker.StopTimeout rather than be
// cancelled the moment a signal arrives. A Start that comes after Stop returns nil
// without starting anything.
func (conn *serverConnection) Start(ctx context.Context) error {
	if err := conn.start(); err != nil {
		return err
	}

	return conn.wait()
}

// start starts the worker unless Stop came first.
func (conn *serverConnection) start() error {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	switch {
	case conn.stopped:
		return nil
	case conn.started:
		return errorstack.New("Worker has already been started")
	}

	// Marked before the attempt, so that Stop cleans up whatever a failed start
	// left running: the SDK does not stop every part it started when a later part
	// fails.
	conn.started = true

	if err := conn.worker.Start(); err != nil {
		return failure(err)
	}

	conn.running.Store(true)
	return nil
}

// wait blocks until Stop is called, and returns nil then, or until the worker stops
// on a fatal error, and returns that error.
func (conn *serverConnection) wait() error {
	select {
	case <-conn.stopping:
		return nil
	case err := <-conn.fatal:
		return failure(err)
	}
}

// Stop stops the worker: it stops polling, and waits up to
// ConfigWorker.StopTimeout for the activities it is running to finish. It is safe
// to call on a worker that never started, and does nothing then.
//
// It returns once the worker has stopped or ctx is done, whichever comes first,
// so that a StopTimeout longer than the Service's budget cannot hold the Service's
// teardown past it.
//
// The client the worker polls with is not closed here: it is a dependency of its
// own, which the Service closes once every server has drained.
func (conn *serverConnection) Stop(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.stop()
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errorstack.Wrap(ctx.Err(), "Worker did not stop within the shutdown budget",
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(ctx.Err())),
		)
	}
}

// stop releases Start and stops the worker if it was started.
func (conn *serverConnection) stop() {
	conn.mu.Lock()
	if conn.stopped {
		conn.mu.Unlock()
		return
	}

	conn.stopped = true
	started := conn.started
	conn.running.Store(false)
	close(conn.stopping)
	conn.mu.Unlock()

	// Outside the lock: the worker's Stop waits for running activities, and
	// nothing else needs the lock by now — a Start arriving later sees stopped.
	if started {
		conn.worker.Stop()
	}
}

// fail records a fatal error the worker stopped on, which ends Start. The SDK
// calls it at most once, on a goroutine of its own, and stops the worker itself
// afterwards.
func (conn *serverConnection) fail(err error) {
	conn.running.Store(false)

	select {
	case conn.fatal <- err:
	default:
	}
}

// CheckHealth reports whether the worker is polling. Whether the server can be
// reached is the business of the client the worker polls with, which is
// registered as a dependency and checked on its own.
func (conn *serverConnection) CheckHealth(ctx context.Context) error {
	if !conn.running.Load() {
		return integration.Unhealthy(errWorkerNotRunning)
	}

	return nil
}

// failure returns the error Start reports for err. The Service already says which
// server failed to start, so this carries the worker's own reason rather than
// another "Failed to start server".
func failure(err error) error {
	return errorstack.Wrap(err, integration.NormalizeErrorMessage(err))
}
