package temporal

import (
	"context"

	"github.com/mountayaapp/helix.go/service"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/contrib/sysinfo"
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

// New creates a Temporal worker along with a client and registers the worker as a
// server via service.Serve. Use this for worker services that process workflows and
// activities.
func New(svc *service.Service, cfg ConfigWorker) (*Client, *Worker, error) {

	// No need to continue if ConfigWorker is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, nil, err
	}

	// Dial the Temporal server.
	c, err := dialClient(svc, &cfg.Client)
	if err != nil {
		return nil, nil, err
	}

	// Create a Temporal worker.
	var optsWorker = worker.Options{
		WorkerActivitiesPerSecond:    cfg.WorkerActivitiesPerSecond,
		TaskQueueActivitiesPerSecond: cfg.TaskQueueActivitiesPerSecond,
		EnableSessionWorker:          cfg.EnableSessionWorker,
		SysInfoProvider:              sysinfo.SysInfoProvider(),
	}

	w := worker.New(c, cfg.TaskQueue, optsWorker)

	sc := &serverConnection{
		client: c,
		worker: w,
	}

	// Register the worker connection as a server.
	if err := service.Serve(svc, sc); err != nil {
		return nil, nil, err
	}

	return &Client{client: c}, &Worker{worker: w}, nil
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
