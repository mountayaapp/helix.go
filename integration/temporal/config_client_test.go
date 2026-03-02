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
			name:   "empty config applies default address and namespace",
			before: ConfigClient{},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
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
			name: "TLS with only CertFile returns error",
			before: ConfigClient{
				TLS: integration.ConfigTLS{
					Enabled:  true,
					CertFile: "cert.crt",
				},
			},
			after: ConfigClient{
				Address:   "127.0.0.1:7233",
				Namespace: "default",
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
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.before.sanitize()

			assert.Equal(t, tc.after, tc.before)
			assert.Equal(t, tc.err, err)
		})
	}
}
