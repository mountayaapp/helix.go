package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"
	"github.com/mountaya/helix.go/internal/telemetry/log"
	"github.com/mountaya/helix.go/internal/telemetry/trace"
	helixlog "github.com/mountaya/helix.go/telemetry/log"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	otellog "go.opentelemetry.io/otel/log"
	otelmetric "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"
)

const (
	// defaultShutdownTimeout is the budget given to graceful shutdown when Config
	// leaves ShutdownTimeout at zero.
	defaultShutdownTimeout = 30 * time.Second

	// healthTimeout caps a health check when the caller's context has no deadline,
	// so that a wedged dependency cannot hang a readiness probe indefinitely.
	healthTimeout = 5 * time.Second

	// telemetryDrainTimeout is the part of the shutdown budget reserved for
	// flushing telemetry, which happens last. It is carved out of
	// ShutdownTimeout — never added to it — so that the whole of Stop stays within
	// the budget an orchestrator was promised, and so that an integration which
	// will not drain cannot spend the time the spans explaining why need to be
	// exported.
	telemetryDrainTimeout = 5 * time.Second
)

// Config configures a Service. The zero value is usable: resources are detected
// from the standard OTEL_* environment, global providers are installed, and
// shutdown gets a 30s budget.
type Config struct {

	// ShutdownTimeout caps the graceful shutdown of servers, dependencies and
	// telemetry. Zero means 30 seconds. Up to five seconds of it — a quarter, when
	// it is shorter than twenty — is kept for flushing telemetry, so that servers
	// and dependencies that will not stop cannot leave nothing for it.
	ShutdownTimeout time.Duration

	// LogLevel is the minimum level logged, to stderr and to the OpenTelemetry
	// export alike. Zero means the OTEL_LOG_LEVEL environment variable decides, and
	// info when that is unset too.
	LogLevel helixlog.Level

	// SkipGlobals leaves the process-wide OpenTelemetry providers and propagator
	// untouched, along with the default logger and tracer that telemetry/log and
	// telemetry/trace fall back to when a context carries neither. Global
	// registration is the one genuinely once-per-process act in this package; set
	// this when a second Service runs in the same process, or when the application
	// installs its own globals.
	//
	// It does not disable context propagation: the integrations carry the
	// Service's own propagator rather than reading the global one, so a Service
	// built with SkipGlobals still continues an inbound trace and still injects
	// into outbound requests.
	SkipGlobals bool

	// Propagator carries trace context and baggage across process boundaries: the
	// HTTP integrations extract it from inbound requests and inject it into
	// outbound ones, and Temporal propagates through it too.
	//
	// Zero means the W3C pair the ecosystem agreed on — TraceContext and Baggage —
	// which is also what New registers globally unless SkipGlobals says not to.
	// Supply one to add a legacy format alongside them.
	Propagator propagation.TextMapPropagator

	// TracerProvider supplies traces instead of the SDK provider that New would
	// otherwise build from the environment. It is an API type, so the Service does
	// not own its lifecycle and Stop will not shut it down.
	//
	// Whatever is supplied here is wrapped before use, so that the instrumentation
	// scopes helix suppresses are suppressed here too. See the note on Service.
	TracerProvider oteltrace.TracerProvider

	// LoggerProvider supplies log records instead of the SDK provider that New
	// would otherwise build from the environment. It is an API type, so the
	// Service does not own its lifecycle and Stop will not shut it down.
	LoggerProvider otellog.LoggerProvider

	// MeterProvider supplies metrics instead of the SDK provider that New would
	// otherwise build from the environment. It is an API type, so the Service does
	// not own its lifecycle and Stop will not shut it down.
	MeterProvider otelmetric.MeterProvider
}

// namedServer pairs a server with the name the application gave it at Serve time.
type namedServer struct {
	name   string
	server integration.Server
}

// namedDependency pairs a dependency with the name the application gave it at
// Attach time.
type namedDependency struct {
	name string
	dep  integration.Dependency
}

// Service owns the telemetry pipeline and the lifecycle of the servers and
// dependencies attached to it.
//
// More than one Service may exist in a process. Only global registration is
// process-wide, and Config.SkipGlobals controls it.
//
// # Suppressed instrumentation
//
// The tracer and meter providers a Service resolves are filtered views: spans and
// metrics from the instrumentation scopes listed in the internal trace package's
// DroppedScopes are discarded rather than exported. Today that is
// gocloud.dev/blob, whose spans and metrics the bucket integration duplicates — it
// creates them from the global providers with no way to inject one or turn them
// off, so one operation would otherwise be recorded twice, once in a vocabulary
// nothing else here uses.
//
// This is deliberate and worth knowing about: helix suppressing a third party's
// telemetry is surprising, and it is the only place it does so.
type Service struct {
	logger *log.Logger
	tracer *trace.Tracer

	// propagator is what the integrations use to cross a process boundary. It is
	// resolved once here and handed to them, rather than read from the global
	// registry at each call, so that Config.SkipGlobals cannot silently turn
	// distributed tracing into a no-op.
	propagator propagation.TextMapPropagator

	shutdownTimeout time.Duration

	mu           sync.Mutex
	servers      []namedServer
	dependencies []namedDependency
	started      bool
	stopped      bool

	// meterProvider is the resolved metric provider. Unlike the logger and tracer
	// it has no helix wrapper: the integrations' instruments reach it through the
	// package-level MeterProvider accessor, and the instrumentation helix does not
	// wire itself through the global provider.
	meterProvider otelmetric.MeterProvider

	// sdkTracerProvider, sdkLoggerProvider and sdkMeterProvider are the SDK
	// providers this Service built and is therefore responsible for shutting down.
	// They are nil when the providers were supplied through Config or when
	// telemetry is disabled, in which case their lifecycle belongs to whoever
	// created them.
	sdkTracerProvider *sdktrace.TracerProvider
	sdkLoggerProvider *sdklog.LoggerProvider
	sdkMeterProvider  *sdkmetric.MeterProvider

	// wg tracks the server goroutines Start launched.
	wg sync.WaitGroup

	// done is closed the first time a server exits or Stop is called, and err is
	// what the first server to exit returned. err is written before done is closed
	// and read only after, which is what makes the pair safe to read concurrently.
	done     chan struct{}
	doneOnce sync.Once
	err      error

	// health coalesces concurrent CheckHealth calls into one.
	health healthCheck
}

// New creates a Service with a configured logger and tracer sharing one
// OpenTelemetry resource, so that logs and traces agree on what produced them.
//
// The caller's context bounds the setup, including resource detection and
// exporter construction. It is not retained: it does not bound the Service's
// lifetime, which Run and Stop control.
func New(ctx context.Context, cfg Config) (*Service, error) {
	shutdownTimeout := cfg.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}

	otelDisabled := otelSDKDisabled()

	// Create a shared OpenTelemetry resource from the cloud provider attributes
	// and standard OTEL_RESOURCE_ATTRIBUTES / OTEL_SERVICE_NAME env vars.
	// WithFromEnv is applied last so that OTEL_SERVICE_NAME overrides the
	// cloud-detected service name.
	res, err := resource.New(ctx,
		resource.WithAttributes(cloudAttributes()...),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, errorstack.Wrap(err, "Failed to create OpenTelemetry resource",
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	svc := &Service{
		shutdownTimeout: shutdownTimeout,
		done:            make(chan struct{}),
	}

	// A provider built here runs goroutines and holds exporter connections, so one
	// that was built before a later step failed is shut down rather than leaked.
	fail := func(err error) (*Service, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), telemetryDrainTimeout)
		defer cancel()

		svc.shutdownProviders(ctx)
		return nil, err
	}

	svc.logger, svc.sdkLoggerProvider, err = newLogger(ctx, cfg, res, otelDisabled)
	if err != nil {
		return fail(err)
	}

	svc.tracer, svc.sdkTracerProvider, err = newTracer(ctx, cfg, res, otelDisabled)
	if err != nil {
		return fail(err)
	}

	svc.meterProvider, svc.sdkMeterProvider, err = newMeterProvider(ctx, cfg, res, otelDisabled)
	if err != nil {
		return fail(err)
	}

	svc.propagator = cfg.Propagator
	if svc.propagator == nil {
		svc.propagator = propagation.NewCompositeTextMapPropagator(propagation.Baggage{}, propagation.TraceContext{})
	}

	// The logger and tracer are installed twice over: their providers as the
	// OpenTelemetry globals, for instrumentation helix does not wire itself, and the
	// helix wrappers as the defaults telemetry/log and telemetry/trace fall back to
	// when a context was not enriched by service.Context.
	if !cfg.SkipGlobals {
		otel.SetLoggerProvider(svc.logger.Provider())
		otel.SetTracerProvider(svc.tracer.Provider())
		otel.SetMeterProvider(svc.meterProvider)
		otel.SetTextMapPropagator(svc.propagator)

		log.SetDefault(svc.logger)
		trace.SetDefault(svc.tracer)
	}

	return svc, nil
}

// logLevel resolves the level the Service logs at: the Config's when it sets one,
// and OTEL_LOG_LEVEL's otherwise.
func logLevel(cfg Config) zapcore.Level {
	switch cfg.LogLevel {
	case helixlog.LevelDebug:
		return zapcore.DebugLevel
	case helixlog.LevelInfo:
		return zapcore.InfoLevel
	case helixlog.LevelWarn:
		return zapcore.WarnLevel
	case helixlog.LevelError:
		return zapcore.ErrorLevel
	default:
		return log.DefaultLevel()
	}
}

// newLogger resolves the logger from the Config, falling back to an SDK logger
// built from the environment. The returned provider is non-nil only when this
// call built it, and is then the Service's to shut down.
func newLogger(ctx context.Context, cfg Config, res *resource.Resource, disabled bool) (*log.Logger, *sdklog.LoggerProvider, error) {
	switch {
	case cfg.LoggerProvider != nil:
		return log.NewLoggerWithProvider(cfg.LoggerProvider, logLevel(cfg), res.Attributes()), nil, nil
	case disabled:
		return log.NewNopLogger(), nil, nil
	}

	// autoexport respects OTEL_LOGS_EXPORTER and all standard OTEL_EXPORTER_OTLP_*
	// environment variables.
	exporter, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, nil, errorstack.Wrap(err, "Failed to create log exporter",
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	provider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)

	return log.NewLoggerWithProvider(provider, logLevel(cfg), res.Attributes()), provider, nil
}

// newTracer resolves the tracer from the Config, falling back to an SDK tracer
// built from the environment. The returned provider is non-nil only when this call
// built it, and is then the Service's to shut down.
func newTracer(ctx context.Context, cfg Config, res *resource.Resource, disabled bool) (*trace.Tracer, *sdktrace.TracerProvider, error) {
	switch {
	case cfg.TracerProvider != nil:
		return trace.NewTracerWithProvider(trace.WithDroppedScopes(cfg.TracerProvider)), nil, nil
	case disabled:
		return trace.NewNopTracer(), nil, nil
	}

	// autoexport respects OTEL_TRACES_EXPORTER and all standard
	// OTEL_EXPORTER_OTLP_* environment variables.
	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, nil, errorstack.Wrap(err, "Failed to create span exporter",
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exporter),
	)

	// The SDK provider is returned unwrapped: it is what Stop shuts down. What the
	// rest of the process sees — including the globally registered provider — is
	// the filtered view, so a dropped scope gets a no-op tracer wherever it asks
	// for one.
	return trace.NewTracerWithProvider(trace.WithDroppedScopes(provider)), provider, nil
}

// newMeterProvider resolves the metric provider from the Config, falling back to
// an SDK provider built from the environment. The returned SDK provider is non-nil
// only when this call built it, and is then the Service's to shut down.
//
// It returns the API type rather than a helix wrapper: the integrations build
// their instruments from it directly, so there is nothing for a wrapper to add.
func newMeterProvider(ctx context.Context, cfg Config, res *resource.Resource, disabled bool) (otelmetric.MeterProvider, *sdkmetric.MeterProvider, error) {
	switch {
	case cfg.MeterProvider != nil:
		return withDroppedMeterScopes(cfg.MeterProvider), nil, nil
	case disabled:
		return metricnoop.NewMeterProvider(), nil, nil
	}

	// autoexport respects OTEL_METRICS_EXPORTER and all standard
	// OTEL_EXPORTER_OTLP_* environment variables.
	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, nil, errorstack.Wrap(err, "Failed to create metric reader",
			errorstack.WithExtension("reason", integration.NormalizeErrorMessage(err)),
		)
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	)

	// As with the tracer provider, the SDK provider is returned unwrapped for Stop
	// to shut down, and the rest of the process sees the filtered view.
	return withDroppedMeterScopes(provider), provider, nil
}

// serve registers a server under the given name. Servers define how the Service
// accepts work: a REST API, a GraphQL API, a Temporal worker. More than one may
// be registered; Run starts them all and Stop drains them all.
//
// The name identifies the server in health output and error context, and must be
// unique among the servers of this Service.
//
// Not exported: integration constructors reach it through the package-level
// function.
func (svc *Service) serve(name string, server integration.Server) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	var entries []errorstack.Entry
	switch {
	case server == nil:
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"server"},
		})
	case name == "":
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"server", "name"},
		})
	case svc.started:
		entries = append(entries, errorstack.Entry{
			Message: "Must be registered before the Service starts",
			Path:    []any{"server", name},
		})
	default:
		if slices.ContainsFunc(svc.servers, func(existing namedServer) bool { return existing.name == name }) {
			entries = append(entries, errorstack.Entry{
				Message: fmt.Sprintf("Must be unique; a server named %q is already registered", name),
				Path:    []any{"server", name},
			})
		}
	}

	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	svc.servers = append(svc.servers, namedServer{name: name, server: server})
	return nil
}

// attach registers a dependency under the given name. Dependencies are
// connections to external systems: databases, caches, blob storage. Their Close
// method is called when the Service stops, and they are closed concurrently.
//
// The name identifies the dependency in health output and error context, and must
// be unique among the dependencies of this Service. Two pools of the same kind —
// a primary and a replica — are told apart by it.
//
// Not exported: integration constructors reach it through the package-level
// function.
func (svc *Service) attach(name string, dep integration.Dependency) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()

	var entries []errorstack.Entry
	switch {
	case dep == nil:
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"dependency"},
		})
	case name == "":
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"dependency", "name"},
		})
	case svc.started:
		entries = append(entries, errorstack.Entry{
			Message: "Must be attached before the Service starts",
			Path:    []any{"dependency", name},
		})
	default:
		if slices.ContainsFunc(svc.dependencies, func(existing namedDependency) bool { return existing.name == name }) {
			entries = append(entries, errorstack.Entry{
				Message: fmt.Sprintf("Must be unique; a dependency named %q is already attached", name),
				Path:    []any{"dependency", name},
			})
		}
	}

	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	svc.dependencies = append(svc.dependencies, namedDependency{name: name, dep: dep})
	return nil
}

// Run starts the registered servers and blocks until ctx is cancelled or a server
// exits, then tears the Service down: servers drain, dependencies close
// concurrently, and telemetry flushes.
//
// Teardown runs on every path, including the one where startup failed. That is
// the path whose telemetry is most worth keeping, and it is the path a state
// machine is most likely to skip.
//
// A server exiting on its own ends Run, whether it failed or returned nil: a
// Service that has stopped accepting work in one of its servers would otherwise
// keep reporting itself alive.
//
// A Service with no servers is legal: Run then blocks until ctx is cancelled and
// tears down the same way, which is the shape a consumer loop or a batch job
// wants from a Service it attached dependencies to.
//
// Signal handling belongs to the application. The usual shape is:
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//	defer stop()
//	return svc.Run(ctx)
func (svc *Service) Run(ctx context.Context) error {
	runErr := svc.Start(ctx)
	if runErr == nil {
		select {
		case <-ctx.Done():
		case <-svc.Done():
			runErr = svc.Err()
		}
	}

	// Shutdown gets a context detached from ctx: by the time Run returns, ctx is
	// usually already cancelled, and draining still needs its budget.
	stopErr := svc.Stop(context.WithoutCancel(ctx))

	switch {
	case runErr != nil && stopErr != nil:
		return errorstack.New("Failed to run Service").
			Append(errorstack.Nest(runErr)...).
			Append(errorstack.Nest(stopErr)...)
	case runErr != nil:
		return runErr
	default:
		return stopErr
	}
}

// Start launches the registered servers and returns without blocking. It is the
// entry point for applications embedding a Service in a larger lifecycle; most
// callers want Run, which also guarantees teardown.
//
// A caller using Start directly learns that a server exited through Done and Err,
// and is responsible for calling Stop. A Service that was stopped cannot be
// started again.
func (svc *Service) Start(ctx context.Context) error {
	svc.mu.Lock()

	switch {
	case svc.stopped:
		svc.mu.Unlock()
		return errorstack.New("Service has already been stopped")
	case svc.started:
		svc.mu.Unlock()
		return errorstack.New("Service has already been started")
	}

	svc.started = true
	servers := slices.Clone(svc.servers)
	svc.mu.Unlock()

	for _, s := range servers {
		svc.wg.Go(func() {
			svc.exit(startFailure(s.name, s.server.Start(ctx)))
		})
	}

	return nil
}

// startFailure returns the error a server named name reports when its Start
// returned err, and nil when err is nil.
//
// The server's own entries are nested under its name rather than wrapped, so that
// its message, code and reason survive into the envelope; err stays the cause, so
// that errors.Is and errors.As still reach it.
func startFailure(name string, err error) error {
	if err == nil {
		return nil
	}

	wrapped := errorstack.Wrap(err, fmt.Sprintf("Failed to start server %q", name))

	if e, ok := errors.AsType[*errorstack.Error](wrapped); ok {
		e.Append(errorstack.Nest(err, "server", name)...)
	}

	return wrapped
}

// Done returns a channel that is closed the first time one of the Service's
// servers exits, or when Stop is called — whichever comes first. A Service with
// no servers is done only once stopped.
//
// It is what an application embedding a Service with Start waits on, in the way
// it would wait on a context's Done.
func (svc *Service) Done() <-chan struct{} {
	return svc.done
}

// Err returns the error of the server whose exit closed Done, and nil when Done is
// not closed yet, when that server exited cleanly, or when it was Stop that closed
// it.
func (svc *Service) Err() error {
	select {
	case <-svc.done:
		return svc.err
	default:
		return nil
	}
}

// exit records the first reason the Service is done, and closes Done.
func (svc *Service) exit(err error) {
	svc.doneOnce.Do(func() {
		svc.err = err
		close(svc.done)
	})
}

// Stop drains the servers, closes the dependencies concurrently, and flushes
// telemetry. Servers are stopped first so that in-flight work finishes before the
// connections it depends on go away.
//
// Stop is idempotent and valid from any state: calling it on a Service that never
// started, or twice, returns nil rather than an error. Teardown that refuses to
// run is how telemetry gets lost.
//
// Every wait Stop performs is bounded by ShutdownTimeout, including the ones on
// integrations it does not control. An integration whose Stop never returns is
// reported as having failed to drain rather than allowed to hold the process
// open, because teardown that cannot finish loses the same telemetry as teardown
// that never ran. Part of the budget is reserved for telemetry, which flushes last
// whatever the integrations did with theirs.
func (svc *Service) Stop(ctx context.Context) error {
	svc.mu.Lock()
	if svc.stopped {
		svc.mu.Unlock()
		return nil
	}

	svc.stopped = true
	servers := slices.Clone(svc.servers)
	deps := slices.Clone(svc.dependencies)
	svc.mu.Unlock()

	svc.exit(nil)

	ctx, cancel := context.WithTimeout(ctx, svc.shutdownTimeout)
	defer cancel()

	reserve := min(telemetryDrainTimeout, svc.shutdownTimeout/4)
	integrationsCtx, cancelIntegrations := context.WithTimeout(ctx, svc.shutdownTimeout-reserve)
	defer cancelIntegrations()

	var (
		mu       sync.Mutex
		children []errorstack.Entry
	)

	// Nest rather than Wrap: Wrap keeps what it wraps as an unserialized cause, so
	// collecting its entries would report this function's own sentence and drop
	// the integration's message, its code and its reason. Nest carries them
	// through and records which integration they came from in the path.
	collect := func(err error, segments ...any) {
		if err == nil {
			return
		}

		mu.Lock()
		children = append(children, errorstack.Nest(err, segments...)...)
		mu.Unlock()
	}

	var serverWG sync.WaitGroup
	for _, s := range servers {
		serverWG.Go(func() {
			collect(s.server.Stop(integrationsCtx), "server", s.name)
		})
	}

	if err := waitWithin(integrationsCtx, &serverWG); err != nil {
		collect(errorstack.Wrap(err, "Servers did not drain within the shutdown budget"), "service", "servers")
	}

	// Server goroutines are done accepting work; wait for them to unwind before
	// closing the dependencies they were using.
	if err := waitWithin(integrationsCtx, &svc.wg); err != nil {
		collect(errorstack.Wrap(err, "Servers did not stop accepting work within the shutdown budget"),
			"service", "servers")
	}

	var wg sync.WaitGroup
	for _, d := range deps {
		wg.Go(func() {
			collect(d.dep.Close(integrationsCtx), "dependency", d.name)
		})
	}

	if err := waitWithin(integrationsCtx, &wg); err != nil {
		collect(errorstack.Wrap(err, "Dependencies did not close within the shutdown budget"),
			"service", "dependencies")
	}

	// Telemetry gets what is left of the whole budget, capped: it is at least the
	// reserve, since the integrations' context expired before this one, and at most
	// telemetryDrainTimeout, so that an unreachable collector cannot hold a
	// shutdown that has otherwise finished.
	telemetryCtx, cancelTelemetry := context.WithTimeout(ctx, telemetryDrainTimeout)
	defer cancelTelemetry()

	for _, err := range svc.shutdownProviders(telemetryCtx) {
		collect(err.err, "telemetry", err.name)
	}

	// Read under the lock, and copy: an integration that outlived its wait is
	// still running, and still collects into children when it finally returns.
	mu.Lock()
	failures := slices.Clone(children)
	mu.Unlock()

	if len(failures) > 0 {
		return errorstack.New("Failed to gracefully close Service's connections").Append(failures...)
	}

	return nil
}

// providerError is a telemetry provider that failed to shut down.
type providerError struct {
	name string
	err  error
}

// shutdownProviders flushes and shuts down the providers this Service built, and
// syncs the logger. Providers supplied through Config belong to whoever created
// them and are left alone.
//
// Spans go first and get half of ctx's remaining time, metrics half of what is
// left, and logs the rest — logs last, so that a provider failing to deliver can
// still say so through a logger provider that has not shut down yet. Splitting the
// time is what keeps one unreachable exporter, retrying until its deadline, from
// leaving nothing for the others.
//
// A provider failing to shut down is logged, not reported. Every way it can fail
// here is its final export not being delivered — a collector that is down, or
// slow — and Run returns Stop's error for an application to exit with: a service
// whose collector is unreachable would otherwise exit non-zero after shutting down
// perfectly cleanly, which is a worse failure than the lost telemetry. Only the
// local stderr sync is reported.
func (svc *Service) shutdownProviders(ctx context.Context) []providerError {
	var failed []providerError

	warn := func(signal string, err error) {
		if err != nil && svc.logger != nil {
			svc.logger.Warn(ctx, fmt.Sprintf("Failed to deliver the final %s export", signal), helixlog.Err(err))
		}
	}

	if svc.sdkTracerProvider != nil {
		shareCtx, cancel := shareOf(ctx, 2)
		warn("traces", svc.sdkTracerProvider.Shutdown(shareCtx))
		cancel()
	}

	if svc.sdkMeterProvider != nil {
		shareCtx, cancel := shareOf(ctx, 2)
		warn("metrics", svc.sdkMeterProvider.Shutdown(shareCtx))
		cancel()
	}

	// Once the logger provider is shut down, its own failure can only reach stderr.
	if svc.sdkLoggerProvider != nil {
		warn("logs", svc.sdkLoggerProvider.Shutdown(ctx))
	}

	if svc.logger != nil {
		if err := svc.logger.Sync(); err != nil && !isUnsyncable(err) {
			failed = append(failed, providerError{"logger",
				errorstack.Wrap(err, "Failed to gracefully drain/close logger")})
		}
	}

	return failed
}

// shareOf returns a context expiring once 1/n of ctx's remaining time has passed,
// or ctx itself when it has no deadline.
func shareOf(ctx context.Context, n int) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(ctx)
	}

	return context.WithTimeout(ctx, time.Until(deadline)/time.Duration(n))
}

// waitWithin blocks until wg is done or ctx is cancelled, and reports which
// happened.
//
// A sync.WaitGroup has no cancellable Wait, so an integration whose Stop never
// returns — or whose Start never unwinds — would otherwise hold teardown open
// indefinitely, past the budget ShutdownTimeout exists to enforce. The waiting
// goroutine outlives a cancelled wait and ends when the group finally completes;
// that is the cost of not being able to cancel a Wait, and it is bounded by the
// integration eventually returning.
func waitWithin(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isUnsyncable reports whether err is the operating system saying the destination
// does not support fsync, rather than a real flush failure.
//
// Which errno comes back depends on what stderr happens to be: ENOTTY for a
// terminal, EBADF for a pipe — which is the ordinary case both under `go test` and
// in a container — and EINVAL on some platforms. None of them means log data was
// lost, so none of them should make a clean shutdown report an error.
func isUnsyncable(err error) bool {
	return errors.Is(err, syscall.ENOTTY) ||
		errors.Is(err, syscall.EBADF) ||
		errors.Is(err, syscall.EINVAL)
}

// CheckHealth reports whether every server and dependency is usable. It returns
// nil when all of them are healthy, and otherwise an error whose entries name the
// components that failed.
//
// The result carries no transport-specific status code. A server exposing a
// readiness probe decides how to render it: the REST integration maps a non-nil
// error to 503.
//
// Concurrent calls share one check: a readiness probe is unauthenticated, and
// without this every request to it would fan out to every dependency. The shared
// check is detached from the caller that started it, so that a probe whose client
// hung up does not fail the others waiting on the same result.
func (svc *Service) CheckHealth(ctx context.Context) error {
	return svc.health.do(ctx, svc.checkHealth)
}

// checkHealth is CheckHealth without the coalescing.
func (svc *Service) checkHealth(ctx context.Context) error {
	svc.mu.Lock()
	servers := slices.Clone(svc.servers)
	deps := slices.Clone(svc.dependencies)
	svc.mu.Unlock()

	// Guard against contexts with no deadline to prevent indefinite hangs.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, healthTimeout)
		defer cancel()
	}

	var (
		mu       sync.Mutex
		children []errorstack.Entry
		wg       sync.WaitGroup
	)

	// Nest rather than Wrap: the integration already said what is wrong, with
	// which code and for what reason, and Wrap would replace all three with this
	// function's own sentence and CodeInternalError. What is added here is which
	// integration it was, which is the one thing the integration cannot know.
	check := func(err error, segments ...any) {
		if err == nil {
			return
		}

		mu.Lock()
		children = append(children, errorstack.Nest(err, segments...)...)
		mu.Unlock()
	}

	for _, s := range servers {
		wg.Go(func() {
			check(s.server.CheckHealth(ctx), "server", s.name)
		})
	}

	for _, d := range deps {
		wg.Go(func() {
			check(d.dep.CheckHealth(ctx), "dependency", d.name)
		})
	}

	wg.Wait()

	if len(children) > 0 {
		return errorstack.New("Service is not in a healthy state",
			errorstack.WithCode(errorstack.CodeServiceUnavailable),
		).Append(children...)
	}

	return nil
}

// healthCheck coalesces concurrent health checks into one in-flight call.
//
// It is singleflight reduced to the one key this needs, and it shares only a check
// that is still running: a caller arriving after one completed starts a fresh one,
// so readiness is never answered from a stale result.
type healthCheck struct {
	mu   sync.Mutex
	call *healthCall
}

// healthCall is one in-flight check, and its result once done is closed.
type healthCall struct {
	done chan struct{}
	err  error
}

// do runs check, or waits for the one already running, and returns its result. A
// caller whose ctx ends first stops waiting and gets ctx's error.
func (h *healthCheck) do(ctx context.Context, check func(context.Context) error) error {
	h.mu.Lock()
	call := h.call
	if call == nil {
		call = &healthCall{done: make(chan struct{})}
		h.call = call
		h.mu.Unlock()

		go func() {
			call.err = check(context.WithoutCancel(ctx))

			h.mu.Lock()
			h.call = nil
			h.mu.Unlock()

			close(call.done)
		}()
	} else {
		h.mu.Unlock()
	}

	select {
	case <-call.done:
		return call.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
