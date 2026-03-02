package temporal

import (
	"github.com/mountayaapp/helix.go/errorstack"
)

/*
ConfigWorker is used to configure a Temporal worker connection registered as a
server.
*/
type ConfigWorker struct {

	// Client holds the shared Temporal client configuration.
	Client ConfigClient `json:"client"`

	// TaskQueue is the task queue name you use to identify your client worker,
	// also identifies group of workflow and activity implementations that are hosted
	// by a single worker process.
	TaskQueue string `json:"taskqueue"`

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
	WorkerActivitiesPerSecond float64 `json:"worker_activities_per_second,omitempty"`

	// TaskQueueActivitiesPerSecond sets the rate limiting on number of activities
	// that can be executed per second. This is managed by the server and controls
	// activities per second for your entire taskqueue.
	//
	// Notice that the number is represented in float, so that you can set it to
	// less than 1 if needed. For example, set the number to 0.1 means you want
	// your activity to be executed once for every 10 seconds. This can be used to
	// protect down stream services from flooding.
	//
	// Default:
	//
	//   100 000
	TaskQueueActivitiesPerSecond float64 `json:"taskqueue_activities_per_second,omitempty"`
}

/*
sanitize validates the worker configuration by first sanitizing the embedded
client configuration, then validating worker-specific fields. Returns an error
if configuration is not valid.
*/
func (cfg *ConfigWorker) sanitize() error {
	stack := errorstack.New("Failed to validate configuration", errorstack.WithIntegration(identifier))

	if cfg.TaskQueue == "" {
		stack.WithValidations(errorstack.Validation{
			Message: "TaskQueue must be set and not be empty",
			Path:    []string{"Config", "Worker", "TaskQueue"},
		})
	}

	// Sanitize the client configuration.
	err := cfg.Client.sanitize()
	if err != nil {
		if errstack, ok := err.(*errorstack.Error); ok {
			stack.WithValidations(errstack.Validations...)
		} else {
			stack.WithValidations(errorstack.Validation{
				Message: err.Error(),
			})
		}
	}

	if stack.HasValidations() {
		return stack
	}

	return nil
}
