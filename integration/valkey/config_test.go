package valkey

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
				Address: "127.0.0.1:6379",
			},
			err: nil,
		},
		{
			name: "custom address and credentials are preserved",
			before: Config{
				Address:  "valkey.example.com:6379",
				User:     "admin",
				Password: "secret",
			},
			after: Config{
				Address:  "valkey.example.com:6379",
				User:     "admin",
				Password: "secret",
			},
			err: nil,
		},
		{
			name: "custom address preserves address",
			before: Config{
				Address: "valkey.custom.com:6380",
			},
			after: Config{
				Address: "valkey.custom.com:6380",
			},
			err: nil,
		},
		{
			name: "custom user and password without address applies default address",
			before: Config{
				User:     "myuser",
				Password: "mypassword",
			},
			after: Config{
				Address:  "127.0.0.1:6379",
				User:     "myuser",
				Password: "mypassword",
			},
			err: nil,
		},
		{
			name: "TLS with only CertPEM returns error",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address: "127.0.0.1:6379",
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
				TLS: integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Address: "127.0.0.1:6379",
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
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Address: "127.0.0.1:6379",
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
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address: "127.0.0.1:6379",
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
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			after: Config{
				Address: "127.0.0.1:6379",
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
