package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
)

/*
Serve registers the server integration for this service. Only one server can be
registered — calling Serve twice returns an error. Server integrations define
how the service accepts work: REST API, GraphQL API, or Temporal Worker.
*/
func Serve(server integration.Server) error {
	svc.mutex.Lock()
	defer svc.mutex.Unlock()

	stack := errorstack.New("Failed to register server integration")
	if svc.isInitialized {
		stack.WithValidations(errorstack.Validation{
			Message: "Service must not be initialized for registering a server",
		})

		return stack
	}

	if svc.isStopped {
		stack.WithValidations(errorstack.Validation{
			Message: "Service must not be stopped for registering a server",
		})

		return stack
	}

	if server == nil {
		stack.WithValidations(errorstack.Validation{
			Message: "Server must not be nil",
		})

		return stack
	}

	if server.String() == "" {
		stack.WithValidations(errorstack.Validation{
			Message: "Server's name must be set and not be empty",
			Path:    []string{"server.String()"},
		})

		return stack
	}

	if svc.server != nil {
		stack.WithValidations(errorstack.Validation{
			Message: fmt.Sprintf("A server integration has already been registered (%s). A service can only have one server", svc.server.String()),
		})

		return stack
	}

	svc.server = server
	return nil
}

/*
Attach registers a dependency integration to the service. Dependencies are
connections to external systems: databases, caches, blob storage, etc. The
dependency's Close method is automatically called when the service stops.
*/
func Attach(dep integration.Dependency) error {
	svc.mutex.Lock()
	defer svc.mutex.Unlock()

	stack := errorstack.New("Failed to attach dependency integration")
	if svc.isInitialized {
		stack.WithValidations(errorstack.Validation{
			Message: "Service must not be initialized for attaching a dependency",
		})

		return stack
	}

	if svc.isStopped {
		stack.WithValidations(errorstack.Validation{
			Message: "Service must not be stopped for attaching a dependency",
		})

		return stack
	}

	if dep == nil {
		stack.WithValidations(errorstack.Validation{
			Message: "Dependency must not be nil",
		})

		return stack
	}

	if dep.String() == "" {
		stack.WithValidations(errorstack.Validation{
			Message: "Dependency's name must be set and not be empty",
			Path:    []string{"dependency.String()"},
		})

		return stack
	}

	svc.dependencies = append(svc.dependencies, dep)
	return nil
}

/*
Server returns the server integration registered via Serve.
*/
func Server() integration.Server {
	return svc.server
}

/*
Status executes a health check of the server and each dependency attached to the
service, and returns the highest HTTP status code returned. This means if all
integrations are healthy (status `200`) but one is temporarily unavailable
(status `503`), the status returned would be `503`.
*/
func Status(ctx context.Context) (int, error) {

	// Count total integrations: server (if any) + dependencies.
	total := len(svc.dependencies)
	if svc.server != nil {
		total++
	}

	// Create channels for receiving health check results.
	chStatus := make(chan int, total)
	chError := make(chan error, total)

	// Execute health checks asynchronously.
	var wg sync.WaitGroup

	if svc.server != nil {
		wg.Go(func() {
			status, err := svc.server.Status(ctx)
			if err != nil {
				chError <- err
			}

			chStatus <- status
		})
	}

	for _, dep := range svc.dependencies {
		wg.Go(func() {
			status, err := dep.Status(ctx)
			if err != nil {
				chError <- err
			}

			chStatus <- status
		})
	}

	wg.Wait()
	close(chStatus)
	close(chError)

	// Define the highest status code returned, as it will be used as the main one
	// returned by this function.
	var max int = 200
	for status := range chStatus {
		if status > max {
			max = status
		}
	}

	// Build a list of returned errors, and returned the error stack if applicable.
	stack := errorstack.New("Service is not in a healthy state")
	for err := range chError {
		stack.WithChildren(err)
	}

	if stack.HasChildren() {
		return max, stack
	}

	return max, nil
}
