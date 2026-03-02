package service

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/internal/logger"
	"github.com/mountayaapp/helix.go/internal/tracer"
)

/*
svc is the service being run at the moment. Only one service should be running
at a time. This is why end-users can't create multiple services in a single Go
application.
*/
var svc = new(service)

/*
service holds some information for the service running.
*/
type service struct {

	// mutex allows to lock/unlock access to the service when necessary.
	mutex sync.Mutex

	// isInitialized informs if the service has already been initialized. In other
	// words this informs if the Init() function has already been called and returned
	// with no error.
	isInitialized bool

	// isStopped informs if the service has already been stopped. In other words
	// this informs if the Stop() function has already been called and returned
	// with no error.
	isStopped bool

	// server is the server integration registered via Serve. Only one server can
	// be registered per service.
	server integration.Server

	// dependencies is the list of dependency integrations attached via Attach.
	dependencies []integration.Dependency
}

/*
Start initializes the helix service and starts the server integration registered
via Serve. Dependencies are not started — they connect eagerly in their constructors.
This returns as soon as an interrupting signal is catched or when the server returns
an error while starting.
*/
func Start(ctx context.Context) error {
	svc.mutex.Lock()
	defer svc.mutex.Unlock()

	stack := errorstack.New("Failed to initialize the service")
	if svc.isInitialized {
		stack.WithValidations(errorstack.Validation{
			Message: "Service has already been initialized",
		})

		return stack
	}

	if svc.isStopped {
		stack.WithValidations(errorstack.Validation{
			Message: "Cannot initialize a stopped service",
		})

		return stack
	}

	if svc.server == nil {
		stack.WithValidations(errorstack.Validation{
			Message: "Service must have a server registered via Serve before starting",
		})

		return stack
	}

	// Create a channel for receiving interrupting signals, and another one for
	// catching server errors. The function will then return as soon as one of
	// the channel receives a value.
	done := make(chan os.Signal, 1)
	failed := make(chan error, 1)

	// Listen for interrupting signals.
	go func() {
		signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
		<-done
	}()

	// Start the server integration. Its Start function is blocking — it listens
	// for and processes incoming work until the service stops.
	go func() {
		err := svc.server.Start(ctx)
		if err != nil {
			failed <- stack.WithChildren(err)
		}
	}()

	// Return as soon as an interrupting signal is catched or when the server
	// returns an error while starting.
	svc.isInitialized = true
	select {
	case <-done:
		return nil
	case <-failed:
		return stack
	}
}

/*
Stop tries to gracefully stop the server and close all dependency connections.
The server is stopped first to drain in-flight requests, then dependencies are
closed concurrently once idle. It then tries to drain/close the tracer and
logger.
*/
func Stop(ctx context.Context) error {
	svc.mutex.Lock()
	defer svc.mutex.Unlock()

	stack := errorstack.New("Failed to gracefully close service's connections")
	if !svc.isInitialized {
		stack.WithValidations(errorstack.Validation{
			Message: "Service must first be initialized",
		})

		return stack
	}

	if svc.isStopped {
		stack.WithValidations(errorstack.Validation{
			Message: "Service has already been stopped",
		})

		return stack
	}

	// Stop the server first to drain in-flight requests. Dependencies remain
	// available during this phase.
	if svc.server != nil {
		err := svc.server.Stop(ctx)
		if err != nil {
			stack.WithChildren(err)
		}
	}

	// Close all dependencies concurrently — connections are now idle.
	var wg sync.WaitGroup
	for _, dep := range svc.dependencies {
		wg.Go(func() {
			err := dep.Close(ctx)
			if err != nil {
				stack.WithChildren(err)
			}
		})
	}

	wg.Wait()
	if stack.HasChildren() {
		return stack
	}

	if tracer.Exporter() != nil {
		if err := tracer.Exporter().Shutdown(ctx); err != nil {
			stack.WithChildren(&errorstack.Error{
				Message: "Failed to gracefully drain/close tracer",
				Validations: []errorstack.Validation{
					{
						Message: err.Error(),
					},
				},
			})
		}
	}

	// Ignore if the error is ENOTTY, as explained in this comment on GitHub:
	// https://github.com/uber-go/zap/issues/991#issuecomment-962098428.
	if logger.Logger() != nil {
		if err := logger.Logger().Sync(); err != nil {
			if !errors.Is(err, syscall.ENOTTY) {
				stack.WithChildren(&errorstack.Error{
					Message: "Failed to gracefully drain/close logger",
					Validations: []errorstack.Validation{
						{
							Message: err.Error(),
						},
					},
				})
			}
		}
	}

	if stack.HasChildren() {
		return stack
	}

	svc.isStopped = true
	return nil
}
