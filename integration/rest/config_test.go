package rest

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
				Address: ":8080",
			},
			err: nil,
		},
		{
			name: "custom address is preserved",
			before: Config{
				Address: ":9090",
			},
			after: Config{
				Address: ":9090",
			},
			err: nil,
		},
		{
			name: "OpenAPI enabled without description returns error",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled: true,
				},
			},
			after: Config{
				Address: ":8080",
				OpenAPI: ConfigOpenAPI{
					Enabled: true,
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Description must be set and not be empty",
						Path:    []string{"Config", "OpenAPI", "Description"},
					},
				},
			},
		},
		{
			name: "OpenAPI enabled with description is valid",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled:     true,
					Description: "./openapi.yaml",
				},
			},
			after: Config{
				Address: ":8080",
				OpenAPI: ConfigOpenAPI{
					Enabled:     true,
					Description: "./openapi.yaml",
				},
			},
			err: nil,
		},
		{
			name: "OpenAPI disabled is valid",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled: false,
				},
			},
			after: Config{
				Address: ":8080",
				OpenAPI: ConfigOpenAPI{
					Enabled: false,
				},
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
				Address: ":8080",
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
			name: "OpenAPI and TLS both invalid returns combined errors",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled: true,
				},
				TLS: integration.ConfigTLS{
					Enabled:  true,
					CertFile: "cert.crt",
				},
			},
			after: Config{
				Address: ":8080",
				OpenAPI: ConfigOpenAPI{
					Enabled: true,
				},
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
						Message: "Description must be set and not be empty",
						Path:    []string{"Config", "OpenAPI", "Description"},
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
