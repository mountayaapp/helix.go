package clickhouse

import (
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"github.com/stretchr/testify/assert"
)

func TestConfig_Sanitize(t *testing.T) {
	testcases := []struct {
		name   string
		before Config
		after  Config
		err    error
	}{
		{
			name:   "empty config applies all defaults",
			before: Config{},
			after: Config{
				Address:  "127.0.0.1:9000",
				Database: "default",
				User:     "default",
				Password: "default",
			},
			err: nil,
		},
		{
			name: "custom address and credentials are preserved",
			before: Config{
				Address:  "clickhouse.example.com:9000",
				Database: "analytics",
				User:     "admin",
				Password: "secret",
			},
			after: Config{
				Address:  "clickhouse.example.com:9000",
				Database: "analytics",
				User:     "admin",
				Password: "secret",
			},
			err: nil,
		},
		{
			name: "custom database preserves database and applies other defaults",
			before: Config{
				Database: "mydb",
			},
			after: Config{
				Address:  "127.0.0.1:9000",
				Database: "mydb",
				User:     "default",
				Password: "default",
			},
			err: nil,
		},
		{
			name: "custom user preserves user and applies other defaults",
			before: Config{
				User: "custom_user",
			},
			after: Config{
				Address:  "127.0.0.1:9000",
				Database: "default",
				User:     "custom_user",
				Password: "default",
			},
			err: nil,
		},
		{
			name: "TLS with only CertFile returns error",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled:  true,
					CertFile: "cert.crt",
				},
			},
			after: Config{
				Address:  "127.0.0.1:9000",
				Database: "default",
				User:     "default",
				Password: "default",
				TLS: integration.ConfigTLS{
					Enabled:  true,
					CertFile: "cert.crt",
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "CertFile and KeyFile must be set together or neither must be set",
						Path:    []string{"Config", "TLS"},
					},
				},
			},
		},
		{
			name: "TLS with InsecureSkipVerify is valid",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			after: Config{
				Address:  "127.0.0.1:9000",
				Database: "default",
				User:     "default",
				Password: "default",
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			err: nil,
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
