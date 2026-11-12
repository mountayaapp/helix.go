package temporal

import (
	"math"
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"github.com/stretchr/testify/assert"
)

// defaultClient is the client configuration an empty ConfigClient sanitizes to.
var defaultClient = ConfigClient{
	Address:   "127.0.0.1:7233",
	Namespace: "default",
}

func TestConfigWorker_Sanitize(t *testing.T) {
	testcases := []struct {
		name   string
		before ConfigWorker
		after  ConfigWorker
		err    error
	}{
		{
			name:   "empty config returns task queue error and applies client defaults",
			before: ConfigWorker{},
			after: ConfigWorker{
				Client:      defaultClient,
				StopTimeout: 25 * time.Second,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be set",
					Path:    []any{"config", "task_queue"},
				},
			),
		},
		{
			name: "valid config with task queue applies defaults",
			before: ConfigWorker{
				TaskQueue: "my-task-queue",
			},
			after: ConfigWorker{
				Client:      defaultClient,
				TaskQueue:   "my-task-queue",
				StopTimeout: 25 * time.Second,
			},
			err: nil,
		},
		{
			name: "custom client, name and stop timeout are preserved",
			before: ConfigWorker{
				Client: ConfigClient{
					Address:   "temporal.example.com:7233",
					Namespace: "production",
				},
				Name:        "orders-worker",
				TaskQueue:   "my-task-queue",
				StopTimeout: time.Minute,
			},
			after: ConfigWorker{
				Client: ConfigClient{
					Address:   "temporal.example.com:7233",
					Namespace: "production",
				},
				Name:        "orders-worker",
				TaskQueue:   "my-task-queue",
				StopTimeout: time.Minute,
			},
			err: nil,
		},
		{
			name: "fractional rate limits are preserved",
			before: ConfigWorker{
				TaskQueue:                    "queue",
				WorkerActivitiesPerSecond:    0.1,
				TaskQueueActivitiesPerSecond: 0.5,
				EnableSessionWorker:          true,
			},
			after: ConfigWorker{
				Client:                       defaultClient,
				TaskQueue:                    "queue",
				WorkerActivitiesPerSecond:    0.1,
				TaskQueueActivitiesPerSecond: 0.5,
				EnableSessionWorker:          true,
				StopTimeout:                  25 * time.Second,
			},
			err: nil,
		},
		{
			name: "concurrency limits are preserved",
			before: ConfigWorker{
				TaskQueue:                    "queue",
				MaxConcurrentActivities:      1,
				MaxConcurrentLocalActivities: 8,
				MaxConcurrentWorkflowTasks:   2,
			},
			after: ConfigWorker{
				Client:                       defaultClient,
				TaskQueue:                    "queue",
				MaxConcurrentActivities:      1,
				MaxConcurrentLocalActivities: 8,
				MaxConcurrentWorkflowTasks:   2,
				StopTimeout:                  25 * time.Second,
			},
			err: nil,
		},
		{
			name: "negative concurrency limits return errors",
			before: ConfigWorker{
				TaskQueue:                    "queue",
				MaxConcurrentActivities:      -1,
				MaxConcurrentLocalActivities: -1,
				MaxConcurrentWorkflowTasks:   -1,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be greater than or equal to 0",
					Path:    []any{"config", "max_concurrent_activities"},
				},
				errorstack.Entry{
					Message: "Must be greater than or equal to 0",
					Path:    []any{"config", "max_concurrent_local_activities"},
				},
				errorstack.Entry{
					Message: "Must be 0 or greater than or equal to 2",
					Path:    []any{"config", "max_concurrent_workflow_tasks"},
				},
			),
		},
		{
			name: "one workflow task at a time, which the SDK panics on, returns an error",
			before: ConfigWorker{
				TaskQueue:                  "queue",
				MaxConcurrentWorkflowTasks: 1,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be 0 or greater than or equal to 2",
					Path:    []any{"config", "max_concurrent_workflow_tasks"},
				},
			),
		},
		{
			name: "negative, infinite and NaN rate limits and a negative stop timeout return errors",
			before: ConfigWorker{
				TaskQueue:                    "queue",
				WorkerActivitiesPerSecond:    -1,
				TaskQueueActivitiesPerSecond: math.NaN(),
				StopTimeout:                  -time.Second,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be a finite number greater than or equal to 0",
					Path:    []any{"config", "worker_activities_per_second"},
				},
				errorstack.Entry{
					Message: "Must be a finite number greater than or equal to 0",
					Path:    []any{"config", "task_queue_activities_per_second"},
				},
				errorstack.Entry{
					Message: "Must be greater than or equal to 0",
					Path:    []any{"config", "stop_timeout"},
				},
			),
		},
		{
			name: "client entries are reported under config.client",
			before: ConfigWorker{
				Client: ConfigClient{
					Address: "no-port",
					TLS: integration.ConfigTLS{
						Enabled: true,
						KeyPEM:  []byte("key"),
					},
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be set",
					Path:    []any{"config", "task_queue"},
				},
				errorstack.Entry{
					Message: "Must be a host:port address or a gRPC target",
					Path:    []any{"config", "client", "address"},
				},
				errorstack.Entry{
					Message: "Must be set together; cert_pem and key_pem are required as a pair",
					Path:    []any{"config", "client", "tls"},
				},
			),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.before.sanitize()

			assert.Equal(t, tc.err, err)
			if tc.err == nil {
				assert.Equal(t, tc.after, tc.before)
			}
		})
	}

	t.Run("an infinite rate limit returns error", func(t *testing.T) {
		cfg := ConfigWorker{TaskQueue: "queue", WorkerActivitiesPerSecond: math.Inf(1)}
		assert.Error(t, cfg.sanitize())
	})
}

func TestConfigWorker_Name(t *testing.T) {
	assert.Equal(t, "temporal", (&ConfigWorker{}).name())
	assert.Equal(t, "orders", (&ConfigWorker{TaskQueue: "orders"}).name())
	assert.Equal(t, "orders-worker", (&ConfigWorker{Name: "orders-worker", TaskQueue: "orders"}).name())

	// The client's own name names nothing when it belongs to a worker.
	assert.Equal(t, "orders", (&ConfigWorker{Client: ConfigClient{Name: "client"}, TaskQueue: "orders"}).name())
}
