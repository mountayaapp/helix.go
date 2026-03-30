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
			name:   "empty config returns required field errors",
			before: Config{},
			after: Config{
				Address: "127.0.0.1:5432",
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Database is required and must not be empty",
						Path:    []string{"Config", "Database"},
					},
					{
						Message: "User is required and must not be empty",
						Path:    []string{"Config", "User"},
					},
					{
						Message: "Password is required and must not be empty",
						Path:    []string{"Config", "Password"},
					},
				},
			},
		},
		{
			name: "valid config with all required fields",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
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
			name: "missing only database returns database error",
			before: Config{
				User:     "admin",
				Password: "secret",
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				User:     "admin",
				Password: "secret",
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Database is required and must not be empty",
						Path:    []string{"Config", "Database"},
					},
				},
			},
		},
		{
			name: "missing only user returns user error",
			before: Config{
				Database: "mydb",
				Password: "secret",
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				Password: "secret",
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "User is required and must not be empty",
						Path:    []string{"Config", "User"},
					},
				},
			},
		},
		{
			name: "missing only password returns password error",
			before: Config{
				Database: "mydb",
				User:     "admin",
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Password is required and must not be empty",
						Path:    []string{"Config", "Password"},
					},
				},
			},
		},
		{
			name: "TLS with only CertPEM returns error",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "CertPEM and KeyPEM must be set together or neither must be set",
						Path:    []string{"Config", "TLS"},
					},
				},
			},
		},
		{
			name: "TLS with only KeyPEM returns error",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "CertPEM and KeyPEM must be set together or neither must be set",
						Path:    []string{"Config", "TLS"},
					},
				},
			},
		},
		{
			name: "TLS with both CertPEM and KeyPEM is valid",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			err: nil,
		},
		{
			name: "disabled TLS ignores invalid certs",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			err: nil,
		},
		{
			name: "TLS with InsecureSkipVerify is valid",
			before: Config{
				Database: "mydb",
				User:     "admin",
				Password: "secret",
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			after: Config{
				Address:  "127.0.0.1:5432",
				Database: "mydb",
				User:     "admin",
				Password: "secret",
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
