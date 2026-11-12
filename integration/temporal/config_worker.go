package temporal

import (
	"math"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"

	"go.temporal.io/sdk/worker"
)

// defaultStopTimeout is how long a worker waits for its running activities when
// it stops, unless ConfigWorker.StopTimeout says otherwise. It is what a Service
// with the default 30s ShutdownTimeout has left for its integrations once it has
// set aside the part reserved for flushing telemetry.
const defaultStopTimeout = 25 * time.Second

// ConfigWorker is used to configure a Temporal worker registered as a server,
// along with the client it polls with.
type ConfigWorker struct {

	// Client holds the configuration of the client the worker polls with. Its Name
	// is ignored: the worker's own Name applies to both.
	Client ConfigClient `json:"client"`

	// Name identifies the worker in health output, error context and telemetry,
	// where it is the temporal.name attribute. The worker is registered as a server
	// under it, and the client it polls with as a dependency under it too.
	//
	// Default:
	//
	//   TaskQueue
	Name string `json:"name"`

	// TaskQueue is the task queue the worker polls. It identifies the group of
	// workflow and activity implementations hosted by the workers serving it.
	//
	// Required.
	TaskQueue string `json:"task_queue"`

	// MaxConcurrentActivities is how many activities the worker runs at once. An
	// activity task is polled only while one of that many slots is free, so the
	// limit is also what bounds the memory, the connections and the upstream
	// calls the activities of one worker hold together. The SDK's default suits
	// activities that hold next to nothing: size it from what one activity holds
	// and for how long.
	//
	// Default:
	//
	//   1 000
	MaxConcurrentActivities int `json:"max_concurrent_activities,omitzero"`

	// MaxConcurrentLocalActivities is how many local activities the worker runs at
	// once. They run in the worker that runs their workflow, outside the limit
	// MaxConcurrentActivities sets.
	//
	// Default:
	//
	//   1 000
	MaxConcurrentLocalActivities int `json:"max_concurrent_local_activities,omitzero"`

	// MaxConcurrentWorkflowTasks is how many workflow tasks the worker runs at
	// once: the steps of workflow code between two waits, each short. It must be 0
	// or at least 2: at 1 the worker would only poll its sticky queue and never
	// the task queue itself, which the SDK refuses by panicking.
	//
	// Default:
	//
	//   1 000
	MaxConcurrentWorkflowTasks int `json:"max_concurrent_workflow_tasks,omitzero"`

	// WorkerActivitiesPerSecond sets the rate limiting on number of activities that
	// can be executed per second per worker. This can be used to limit resources
	// used by the worker.
	//
	// Notice that the number is represented in float, so that you can set it to
	// less than 1 if needed. For example, set the number to 0.1 means you want
	// your activity to be executed once for every 10 seconds. This can be used to
	// protect down stream services from flooding.
	//
	// Default:
	//
	//   100 000
	WorkerActivitiesPerSecond float64 `json:"worker_activities_per_second,omitzero"`

	// TaskQueueActivitiesPerSecond sets the rate limiting on number of activities
	// that can be executed per second. This is managed by the server and controls
	// activities per second for your entire task queue.
	//
	// Notice that the number is represented in float, so that you can set it to
	// less than 1 if needed. For example, set the number to 0.1 means you want
	// your activity to be executed once for every 10 seconds. This can be used to
	// protect down stream services from flooding.
	//
	// Default:
	//
	//   100 000
	TaskQueueActivitiesPerSecond float64 `json:"task_queue_activities_per_second,omitzero"`

	// EnableSessionWorker enables the session worker, which is required for
	// workflows that use sessions for stateful activity routing. Only enable this
	// when workflows explicitly need sessions, as it consumes extra resources.
	//
	// Default:
	//
	//   false
	EnableSessionWorker bool `json:"enable_session_worker"`

	// DisableWorkflowWorker makes the worker serve the activities of its task queue
	// and nothing else: it polls no workflow task, on the task queue or on a sticky
	// queue of its own. Without it a worker polls for workflow tasks whether or not a
	// workflow is registered, so a worker meant for activities alone keeps long
	// polls open for work it is never given. It is how a group of activities gets
	// slots of its own beside another worker's in the same process: schedule them on
	// a task queue such a worker serves. Registering a workflow on it panics, as the
	// SDK does, and MaxConcurrentWorkflowTasks does not apply.
	//
	// Default:
	//
	//   false
	DisableWorkflowWorker bool `json:"disable_workflow_worker"`

	// StopTimeout is how long the worker waits, once told to stop, for the
	// activities it is running to finish. Activities still running after it are
	// cancelled, and Temporal retries them on another worker according to their
	// retry policy.
	//
	// It is spent inside the Service's ShutdownTimeout, which every integration
	// shares and which keeps part of itself for flushing telemetry: with the
	// Service's defaults, 25 of its 30 seconds. A StopTimeout longer than what the
	// Service has left is cut short — Stop reports the worker as not drained, and
	// the client it polls with is closed under the activities still running.
	// Raise ShutdownTimeout along with it.
	//
	// Default:
	//
	//   25 * time.Second
	StopTimeout time.Duration `json:"stop_timeout,omitzero"`

	// SysInfoProvider reports the host's CPU and memory usage in the heartbeats the
	// worker sends to the server. Left nil, the heartbeats report zero for both.
	//
	// go.temporal.io/sdk/contrib/sysinfo provides one built on gopsutil. It is not
	// wired by default because it brings gopsutil, cgroups and eBPF packages into
	// every binary that imports this integration. The SDK marks the option as
	// experimental.
	SysInfoProvider worker.SysInfoProvider `json:"-"`
}

// sanitize sets default values - when applicable - and validates the
// configuration, including the embedded client configuration, whose entries are
// reported under config.client. Returns an error if configuration is not valid.
func (cfg *ConfigWorker) sanitize() error {
	var entries []errorstack.Entry

	if cfg.TaskQueue == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "task_queue"},
		})
	}

	if cfg.MaxConcurrentActivities < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be greater than or equal to 0",
			Path:    []any{"config", "max_concurrent_activities"},
		})
	}

	if cfg.MaxConcurrentLocalActivities < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be greater than or equal to 0",
			Path:    []any{"config", "max_concurrent_local_activities"},
		})
	}

	// The SDK panics on 1: a worker limited to one workflow task at a time would
	// only ever poll its sticky queue.
	if cfg.MaxConcurrentWorkflowTasks < 0 || cfg.MaxConcurrentWorkflowTasks == 1 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be 0 or greater than or equal to 2",
			Path:    []any{"config", "max_concurrent_workflow_tasks"},
		})
	}

	if !validRate(cfg.WorkerActivitiesPerSecond) {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a finite number greater than or equal to 0",
			Path:    []any{"config", "worker_activities_per_second"},
		})
	}

	if !validRate(cfg.TaskQueueActivitiesPerSecond) {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a finite number greater than or equal to 0",
			Path:    []any{"config", "task_queue_activities_per_second"},
		})
	}

	switch {
	case cfg.StopTimeout < 0:
		entries = append(entries, errorstack.Entry{
			Message: "Must be greater than or equal to 0",
			Path:    []any{"config", "stop_timeout"},
		})
	case cfg.StopTimeout == 0:
		cfg.StopTimeout = defaultStopTimeout
	}

	entries = append(entries, rebase(cfg.Client.validate(), "config", "client")...)

	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}

// name returns the name the worker and its client register under. A worker is
// identified by the task queue it serves unless it is given a name, and the
// integration's identifier is the last resort.
func (cfg *ConfigWorker) name() string {
	switch {
	case cfg.Name != "":
		return cfg.Name
	case cfg.TaskQueue != "":
		return cfg.TaskQueue
	default:
		return identifier
	}
}

// validRate reports whether rate is a usable activities-per-second limit. Zero is
// valid: it leaves the SDK's default in place.
func validRate(rate float64) bool {
	return rate >= 0 && !math.IsInf(rate, 1)
}
