package temporal

import (
	"context"

	"github.com/mountaya/helix.go/service"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// Worker exposes an opinionated way to interact with Temporal's worker capabilities.
// It wraps the Temporal worker for following best practices.
//
// Worker is a concrete type, not an interface, because its methods are generic and
// Go does not permit interface methods to declare type parameters. Its Register
// methods take a Workflow or Activity handle and check the implementation's
// signature against the handle's declared types at compile time.
type Worker struct {
	worker worker.Worker
}

// New creates a Temporal worker polling cfg.TaskQueue, along with the client it
// polls with. Use this for worker services that process workflows and activities.
//
// Both are registered with svc from inside, which owns them from there: the worker
// as a server, started by Run and drained by Stop, and its client as a dependency,
// closed once every server has drained. The Worker handed back registers workflows
// and activities, and the Client starts and signals workflows; neither carries
// lifecycle methods. Register everything before the Service starts.
func New(ctx context.Context, svc *service.Service, cfg ConfigWorker) (*Client, *Worker, error) {

	// No need to continue if ConfigWorker is not valid.
	if err := cfg.sanitize(); err != nil {
		return nil, nil, err
	}

	name := cfg.name()

	c, err := dial(ctx, svc, &cfg.Client, name, "config", "client")
	if err != nil {
		return nil, nil, err
	}

	server := newServerConnection()
	server.worker = worker.New(c, cfg.TaskQueue, workerOptions(&cfg, server, svc))

	// The client is attached before the worker is served, so that every path out of
	// here leaves it with exactly one owner: this function until Attach succeeds,
	// the Service from then on, which closes it whether or not Serve does.
	if err := service.Attach(svc, name, &clientConnection{client: c}); err != nil {
		c.Close()
		return nil, nil, err
	}

	if err := service.Serve(svc, name, server); err != nil {
		return nil, nil, err
	}

	return newClient(c, &cfg.Client), &Worker{worker: server.worker}, nil
}

// workerOptions returns the SDK options of a worker configured by cfg, run by
// server and served by svc. A concurrency limit left at zero is handed on as zero,
// which the SDK reads as its own default.
func workerOptions(cfg *ConfigWorker, server *serverConnection, svc *service.Service) worker.Options {
	return worker.Options{
		MaxConcurrentActivityExecutionSize:      cfg.MaxConcurrentActivities,
		MaxConcurrentLocalActivityExecutionSize: cfg.MaxConcurrentLocalActivities,
		MaxConcurrentWorkflowTaskExecutionSize:  cfg.MaxConcurrentWorkflowTasks,
		WorkerActivitiesPerSecond:               cfg.WorkerActivitiesPerSecond,
		TaskQueueActivitiesPerSecond:            cfg.TaskQueueActivitiesPerSecond,
		EnableSessionWorker:                     cfg.EnableSessionWorker,
		DisableWorkflowWorker:                   cfg.DisableWorkflowWorker,
		WorkerStopTimeout:                       cfg.StopTimeout,
		SysInfoProvider:                         cfg.SysInfoProvider,
		Interceptors:                            workerInterceptors(svc),
		OnFatalError:                            server.fail,
	}
}

// RegisterWorkflow links a workflow implementation to its name on the worker. Input
// and Result are inferred from wf, so impl's signature is checked against the
// workflow's declared types at compile time.
func (w *Worker) RegisterWorkflow[Input, Result any](
	wf Workflow[Input, Result],
	impl func(ctx workflow.Context, input Input) (Result, error),
) {
	w.worker.RegisterWorkflowWithOptions(impl, workflow.RegisterOptions{
		Name: wf.name,
	})
}

// RegisterActivity links an activity implementation to its name on the worker. Input
// and Result are inferred from a, so impl's signature is checked against the
// activity's declared types at compile time.
func (w *Worker) RegisterActivity[Input, Result any](
	a Activity[Input, Result],
	impl func(ctx context.Context, input Input) (Result, error),
) {
	w.worker.RegisterActivityWithOptions(impl, activity.RegisterOptions{
		Name: a.name,
	})
}

// RegisterRepeatableActivity links a repeatable activity implementation to its name
// on the worker. Input, Config and Result are inferred from a, so impl's signature is
// checked against the activity's declared types at compile time.
func (w *Worker) RegisterRepeatableActivity[Input, Config, Result any](
	a RepeatableActivity[Input, Config, Result],
	impl func(ctx context.Context, input Input, config Config) (Result, error),
) {
	w.worker.RegisterActivityWithOptions(impl, activity.RegisterOptions{
		Name: a.name,
	})
}
