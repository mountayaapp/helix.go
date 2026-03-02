package temporal

import (
	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/service"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

/*
iworker is the internal worker used as Temporal worker. It implements the Worker
interface and allows to wrap the Temporal's worker functions for following best
practices.
*/
type iworker struct {
	worker worker.Worker
}

/*
Worker exposes an opinionated way to interact with Temporal's worker capabilities.
*/
type Worker interface {
	registerWorkflow(w any, opts workflow.RegisterOptions)
	registerActivity(a any, opts activity.RegisterOptions)
}

/*
New creates a Temporal worker along with a client and registers the worker as a
server via service.Serve. Use this for worker services that process workflows and
activities.
*/
func New(cfg ConfigWorker) (Client, Worker, error) {

	// No need to continue if ConfigWorker is not valid.
	err := cfg.sanitize()
	if err != nil {
		return nil, nil, err
	}

	// Dial the Temporal server.
	c, err := dialClient(&cfg.Client)
	if err != nil {
		return nil, nil, err
	}

	// Create a Temporal worker.
	stack := errorstack.New("Failed to initialize integration", errorstack.WithIntegration(identifier))
	var optsWorker = worker.Options{
		WorkerActivitiesPerSecond:    cfg.WorkerActivitiesPerSecond,
		TaskQueueActivitiesPerSecond: cfg.TaskQueueActivitiesPerSecond,
		EnableSessionWorker:          true,
	}

	w := worker.New(c, cfg.TaskQueue, optsWorker)
	if w == nil {
		stack.WithValidations(errorstack.Validation{
			Message: "Failed to create worker from client",
		})

		return nil, nil, stack
	}

	sc := &serverConnection{
		client: c,
		worker: w,
	}

	// Register the worker connection as a server.
	if err := service.Serve(sc); err != nil {
		return nil, nil, err
	}

	ic := &iclient{
		config: &cfg.Client,
		client: c,
	}

	iw := &iworker{
		worker: w,
	}

	return ic, iw, nil
}

/*
registerWorkflow registers a workflow function with the worker.
*/
func (iw *iworker) registerWorkflow(w any, opts workflow.RegisterOptions) {
	iw.worker.RegisterWorkflowWithOptions(w, opts)
}

/*
registerActivity registers an activity function with the worker.
*/
func (iw *iworker) registerActivity(a any, opts activity.RegisterOptions) {
	iw.worker.RegisterActivityWithOptions(a, opts)
}
