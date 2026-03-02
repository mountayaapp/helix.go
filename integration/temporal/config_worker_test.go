package temporal

import (
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"github.com/stretchr/testify/assert"
)

func TestConfigWorker_Sanitize(t *testing.T) {
	testcases := []struct {
		name   string
		before ConfigWorker
		after  ConfigWorker
		err    error
	}{
		{
			name: "valid config with task queue applies client defaults",
			before: ConfigWorker{
				TaskQueue: "my-task-queue",
			},
			after: ConfigWorker{
				Client: ConfigClient{
					Address:   "127.0.0.1:7233",
					Namespace: "default",
				},
				TaskQueue: "my-task-queue",
			},
			err: nil,
		},
		{
			name: "missing task queue returns error",
			before: ConfigWorker{
				Client: ConfigClient{
					Address:   "temporal.example.com:7233",
					Namespace: "production",
				},
			},
			after: ConfigWorker{
				Client: ConfigClient{
					Address:   "temporal.example.com:7233",
					Namespace: "production",
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "TaskQueue must be set and not be empty",
						Path:    []string{"Config", "Worker", "TaskQueue"},
					},
				},
			},
		},
		{
			name: "rate limits are preserved",
			before: ConfigWorker{
				TaskQueue:                    "queue",
				WorkerActivitiesPerSecond:    500,
				TaskQueueActivitiesPerSecond: 1000,
			},
			after: ConfigWorker{
				Client: ConfigClient{
					Address:   "127.0.0.1:7233",
					Namespace: "default",
				},
				TaskQueue:                    "queue",
				WorkerActivitiesPerSecond:    500,
				TaskQueueActivitiesPerSecond: 1000,
			},
			err: nil,
		},
		{
			name: "missing task queue and invalid TLS returns combined errors",
			before: ConfigWorker{
				Client: ConfigClient{
					TLS: integration.ConfigTLS{
						Enabled: true,
						KeyFile: "cert.key",
					},
				},
			},
			after: ConfigWorker{
				Client: ConfigClient{
					Address:   "127.0.0.1:7233",
					Namespace: "default",
					TLS: integration.ConfigTLS{
						Enabled: true,
						KeyFile: "cert.key",
					},
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "TaskQueue must be set and not be empty",
						Path:    []string{"Config", "Worker", "TaskQueue"},
					},
					{
						Message: "CertFile and KeyFile must be set together or neither must be set",
						Path:    []string{"Config", "TLS"},
					},
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.before.sanitize()

			assert.Equal(t, tc.after, tc.before)
			assert.Equal(t, tc.err, err)
		})
	}
}
