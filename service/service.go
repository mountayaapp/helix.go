package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/internal/telemetry/log"
	"github.com/mountayaapp/helix.go/internal/telemetry/trace"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
)

/*
serviceState represents the current state of a Service.
*/
type serviceState int

const (
	stateCreated serviceState = iota
	stateStarted
	stateStopped
)

/*
serviceGuard ensures only one Service is ever created per process. Attempting to
call New more than once returns an error.
*/
var serviceGuard sync.Once

/*
Service is the central dependency container. It owns the logger, tracer, cloud
provider detection, and the service lifecycle. Only one instance is allowed per
application.
*/
type Service struct {
	logger   *log.Logger
	tracer   *trace.Tracer
	cloud    *cloud
	resource *resource.Resource

	mu              sync.Mutex
	server          integration.Server
	dependencies    []integration.Dependency
	state           serviceState
	shutdownTimeout time.Duration
	signals         []os.Signal
}

/*
New creates a fully-initialized Service with auto-detected cloud provider,
configured logger, and configured tracer. Returns an error instead of panicking.
Options allow overriding defaults for testing.

Only one Service instance is allowed per application. Calling New more than once
returns an error.
*/
func New(opts ...Option) (*Service, error) {
	var svc *Service
	var newErr error
	called := false

	serviceGuard.Do(func() {
		called = true

		cfg := &serviceConfig{
			shutdownTimeout: 30 * time.Second,
			signals:         []os.Signal{syscall.SIGINT, syscall.SIGTERM},
		}

		for _, opt := range opts {
			opt(cfg)
		}

		otelDisabled := strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true")

		// Default OTLP protocol to gRPC when not explicitly configured.
		if !otelDisabled && os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL") == "" {
			_ = os.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
		}

		c := cfg.cloud
		if c == nil {
			c = detectCloudProvider()
		}

		// Create a shared OpenTelemetry resource from the cloud provider attributes
		// and standard OTEL_RESOURCE_ATTRIBUTES / OTEL_SERVICE_NAME env vars.
		// WithFromEnv is applied last so that OTEL_SERVICE_NAME overrides
		// the cloud-detected service name. Both the logger and tracer use
		// the same resource to ensure consistent service identification
		// across all signals.
		res, err := resource.New(context.Background(),
			resource.WithAttributes(c.attributes()...),
			resource.WithFromEnv(),
		)
		if err != nil {
			newErr = fmt.Errorf("service: failed to create OpenTelemetry resource: %w", err)
			return
		}

		var logger *log.Logger
		if otelDisabled {
			logger = log.NewNopLogger()
		} else {
			var err error
			logger, err = log.NewLogger(log.DefaultLogLevel(), res)
			if err != nil {
				newErr = fmt.Errorf("service: failed to create logger: %w", err)
				return
			}
		}

		var tracer *trace.Tracer
		if otelDisabled {
			tracer = trace.NewNopTracer()
		} else {
			var err error
			tracer, err = trace.NewTracer(res)
			if err != nil {
				newErr = fmt.Errorf("service: failed to create tracer: %w", err)
				return
			}
		}

		// Register global OpenTelemetry providers. This is done at the service level
		// (not inside constructors) because global registration is an
		// application-level concern. Propagators are signal-agnostic.
		otel.SetTracerProvider(tracer.Provider())
		otel.SetTextMapPropagator(
			propagation.NewCompositeTextMapPropagator(propagation.Baggage{}, propagation.TraceContext{}),
		)

		svc = &Service{
			logger:          logger,
			tracer:          tracer,
			cloud:           c,
			resource:        res,
			state:           stateCreated,
			shutdownTimeout: cfg.shutdownTimeout,
			signals:         cfg.signals,
		}
	})

	if !called {
		return nil, fmt.Errorf("service: a Service has already been created; only one instance is allowed per process")
	}

	return svc, newErr
}

/*
requireState checks that the Service is in the expected state and returns
validation errors if not. Must be called while holding svc.mu.
*/
func (svc *Service) requireState(expected serviceState) []errorstack.Validation {
	if svc.state == expected {
		return nil
	}

	var msg string
	switch svc.state {
	case stateCreated:
		msg = "Service has not been started yet"
	case stateStarted:
		msg = "Service has already been started"
	case stateStopped:
		msg = "Service has already been stopped"
	}

	return []errorstack.Validation{
		{
			Message: msg,
		},
	}
}

/*
serve registers the server integration for this Service. Only one server can be
registered — calling serve twice returns an error. Server integrations define
how the service accepts work: REST API, GraphQL API, or Temporal Worker.

Not exported: use the package-level Serve function instead.
*/
func (svc *Service) serve(server integration.Server) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	stack := errorstack.New("Failed to register server integration")
	if err := svc.requireState(stateCreated); err != nil {
		return stack.WithValidations(err...)
	}

	if server == nil {
		stack.WithValidations(errorstack.Validation{
			Message: "Server must not be nil",
		})

		return stack
	}

	if server.Name() == "" {
		stack.WithValidations(errorstack.Validation{
			Message: "Server's name must be set and not be empty",
			Path:    []string{"server.Name()"},
		})

		return stack
	}

	if svc.server != nil {
		stack.WithValidations(errorstack.Validation{
			Message: fmt.Sprintf("A server integration has already been registered (%s). A Service can only have one server", svc.server.Name()),
		})

		return stack
	}

	svc.server = server
	return nil
}

/*
attach registers a dependency integration to the Service. Dependencies are
connections to external systems: databases, caches, blob storage, etc. The
dependency's Close method is automatically called when the Service stops.

Not exported: use the package-level Attach function instead.
*/
func (svc *Service) attach(dep integration.Dependency) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	stack := errorstack.New("Failed to attach dependency integration")
	if err := svc.requireState(stateCreated); err != nil {
		return stack.WithValidations(err...)
	}

	if dep == nil {
		stack.WithValidations(errorstack.Validation{
			Message: "Dependency must not be nil",
		})

		return stack
	}

	if dep.Name() == "" {
		stack.WithValidations(errorstack.Validation{
			Message: "Dependency's name must be set and not be empty",
			Path:    []string{"dependency.Name()"},
		})

		return stack
	}

	svc.dependencies = append(svc.dependencies, dep)
	return nil
}

/*
Start initializes the helix Service and starts the server integration registered
via Serve. This blocks until an interrupting signal is caught or the server
returns an error while starting.
*/
func (svc *Service) Start(ctx context.Context) error {
	svc.mu.Lock()

	stack := errorstack.New("Failed to initialize the Service")
	if err := svc.requireState(stateCreated); err != nil {
		svc.mu.Unlock()
		return stack.WithValidations(err...)
	}

	if svc.server == nil {
		svc.mu.Unlock()
		stack.WithValidations(errorstack.Validation{
			Message: "Service must have a server registered via Serve before starting",
		})

		return stack
	}

	done := make(chan os.Signal, 1)
	failed := make(chan error, 1)

	signal.Notify(done, svc.signals...)

	go func() {
		err := svc.server.Start(ctx)
		if err != nil {
			failed <- err
		}
	}()

	svc.mu.Unlock()

	select {
	case <-done:
		signal.Stop(done)
		svc.mu.Lock()
		svc.state = stateStarted
		svc.mu.Unlock()
		return nil
	case err := <-failed:
		return stack.WithChildren(err)
	}
}

/*
Stop gracefully stops the server and closes all dependency connections. The
server is stopped first to drain in-flight requests, then dependencies are
closed concurrently once idle. It then drains/closes the tracer and logger.
*/
func (svc *Service) Stop(ctx context.Context) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	stack := errorstack.New("Failed to gracefully close Service's connections")
	if err := svc.requireState(stateStarted); err != nil {
		return stack.WithValidations(err...)
	}

	if svc.shutdownTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, svc.shutdownTimeout)
		defer cancel()
	}

	if svc.server != nil {
		err := svc.server.Stop(ctx)
		if err != nil {
			stack.WithChildren(err)
		}
	}

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

	if err := svc.tracer.Shutdown(ctx); err != nil {
		stack.WithChildren(shutdownError("Failed to gracefully drain/close tracer", err))
	}

	if err := svc.logger.Shutdown(ctx); err != nil {
		stack.WithChildren(shutdownError("Failed to gracefully drain/close logger provider", err))
	}

	if err := svc.logger.Sync(); err != nil {
		if !errors.Is(err, syscall.ENOTTY) {
			stack.WithChildren(shutdownError("Failed to gracefully drain/close logger", err))
		}
	}

	if stack.HasChildren() {
		return stack
	}

	svc.state = stateStopped
	return nil
}

func shutdownError(msg string, err error) *errorstack.Error {
	return &errorstack.Error{
		Message: msg,
		Validations: []errorstack.Validation{
			{
				Message: err.Error(),
			},
		},
	}
}

/*
statusTimeout is the maximum duration for a readiness check when the caller's
context has no deadline set.
*/
const statusTimeout = 5 * time.Second

/*
Status executes a health check of the server and each dependency attached to the
Service, and returns the highest HTTP status code returned. This means if all
integrations are healthy (status 200) but one is temporarily unavailable
(status 503), the status returned would be 503.
*/
func (svc *Service) Status(ctx context.Context) (int, error) {
	svc.mu.Lock()

	server := svc.server
	deps := make([]integration.Dependency, len(svc.dependencies))
	copy(deps, svc.dependencies)
	svc.mu.Unlock()

	// Guard against contexts with no deadline to prevent indefinite hangs.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, statusTimeout)
		defer cancel()
	}

	stack := errorstack.New("Service is not in a healthy state")
	var (
		mu  sync.Mutex
		max = 200
		wg  sync.WaitGroup
	)

	check := func(status int, err error) {
		mu.Lock()
		if status > max {
			max = status
		}
		mu.Unlock()

		if err != nil {
			stack.WithChildren(err)
		}
	}

	if server != nil {
		wg.Go(func() {
			check(server.Status(ctx))
		})
	}

	for _, dep := range deps {
		wg.Go(func() {
			check(dep.Status(ctx))
		})
	}

	wg.Wait()

	if stack.HasChildren() {
		return max, stack
	}

	return max, nil
}
