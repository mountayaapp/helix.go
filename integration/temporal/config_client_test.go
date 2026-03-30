package temporal

import (
	"testing"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"github.com/stretchr/testify/assert"
)

func TestConfigClient_Sanitize(t *testing.T) {
	testcases := []struct {
		name   string
		before ConfigClient
		after  ConfigClient
		err    error
	}{
		{
			name:   "empty config applies default address and namespace",
			before: ConfigClient{},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
			},
			err: nil,
		},
		{
			name: "custom namespace preserves namespace and applies default address",
			before: ConfigClient{
				Namespace: "fake",
			},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "fake",
			},
			err: nil,
		},
		{
			name: "custom address and namespace are preserved",
			before: ConfigClient{
				Address:   "temporal.example.com:7233",
				Namespace: "production",
			},
			after: ConfigClient{
				Address:   "temporal.example.com:7233",
				Namespace: "production",
			},
			err: nil,
		},
		{
			name: "TLS with only CertPEM returns error",
			before: ConfigClient{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
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
			before: ConfigClient{
				TLS: integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
				},
			},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
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
			before: ConfigClient{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
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
			before: ConfigClient{
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			err: nil,
		},
		{
			name: "TLS with InsecureSkipVerify is valid",
			before: ConfigClient{
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
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
