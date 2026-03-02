package postgres

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
			name:   "empty config applies default address",
			before: Config{},
			after: Config{
				Address: "127.0.0.1:5432",
			},
			err: nil,
		},
		{
			name: "custom address and credentials are preserved",
			before: Config{
				Address:  "postgres.example.com:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
			},
			after: Config{
				Address:  "postgres.example.com:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
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
				Address: "127.0.0.1:5432",
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
			name: "TLS with both CertFile and KeyFile is valid",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled:  true,
					CertFile: "cert.crt",
					KeyFile:  "cert.key",
				},
			},
			after: Config{
				Address: "127.0.0.1:5432",
				TLS: integration.ConfigTLS{
					Enabled:  true,
					CertFile: "cert.crt",
					KeyFile:  "cert.key",
				},
			},
			err: nil,
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
				Address: "127.0.0.1:5432",
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
