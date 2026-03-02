package graphql

import (
	"testing"
	"time"

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
			name:   "empty config applies defaults and returns schema error",
			before: Config{},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Schema must be set and not be nil",
						Path:    []string{"Config", "Schema"},
					},
				},
			},
		},
		{
			name: "custom address and path are preserved",
			before: Config{
				Address: ":9090",
				Path:    "/api/graphql",
			},
			after: Config{
				Address: ":9090",
				Path:    "/api/graphql",
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Schema must be set and not be nil",
						Path:    []string{"Config", "Schema"},
					},
				},
			},
		},
		{
			name: "APQ enabled without valkey returns schema and valkey errors",
			before: Config{
				APQ: ConfigAPQ{
					Enabled: true,
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				APQ: ConfigAPQ{
					Enabled: true,
					Prefix:  "apq:",
					TTL:     1 * time.Hour,
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Schema must be set and not be nil",
						Path:    []string{"Config", "Schema"},
					},
					{
						Message: "Valkey must be set and not be nil when APQ is enabled",
						Path:    []string{"Config", "APQ", "Valkey"},
					},
				},
			},
		},
		{
			name: "APQ enabled with custom prefix and TTL preserves values",
			before: Config{
				APQ: ConfigAPQ{
					Enabled: true,
					Prefix:  "custom:",
					TTL:     30 * time.Minute,
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				APQ: ConfigAPQ{
					Enabled: true,
					Prefix:  "custom:",
					TTL:     30 * time.Minute,
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Schema must be set and not be nil",
						Path:    []string{"Config", "Schema"},
					},
					{
						Message: "Valkey must be set and not be nil when APQ is enabled",
						Path:    []string{"Config", "APQ", "Valkey"},
					},
				},
			},
		},
		{
			name: "APQ disabled only returns schema error",
			before: Config{
				APQ: ConfigAPQ{
					Enabled: false,
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				APQ: ConfigAPQ{
					Enabled: false,
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Schema must be set and not be nil",
						Path:    []string{"Config", "Schema"},
					},
				},
			},
		},
		{
			name: "GraphiQL enabled applies default path",
			before: Config{
				GraphiQL: ConfigGraphiQL{
					Enabled: true,
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				GraphiQL: ConfigGraphiQL{
					Enabled: true,
					Path:    "/graphiql",
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Schema must be set and not be nil",
						Path:    []string{"Config", "Schema"},
					},
				},
			},
		},
		{
			name: "GraphiQL enabled with custom path preserves path",
			before: Config{
				GraphiQL: ConfigGraphiQL{
					Enabled: true,
					Path:    "/custom/graphiql",
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				GraphiQL: ConfigGraphiQL{
					Enabled: true,
					Path:    "/custom/graphiql",
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Schema must be set and not be nil",
						Path:    []string{"Config", "Schema"},
					},
				},
			},
		},
		{
			name: "GraphiQL disabled only returns schema error",
			before: Config{
				GraphiQL: ConfigGraphiQL{
					Enabled: false,
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				GraphiQL: ConfigGraphiQL{
					Enabled: false,
				},
			},
			err: &errorstack.Error{
				Integration: identifier,
				Message:     "Failed to validate configuration",
				Validations: []errorstack.Validation{
					{
						Message: "Schema must be set and not be nil",
						Path:    []string{"Config", "Schema"},
					},
				},
			},
		},
		{
			name: "TLS with only CertFile returns schema and TLS errors",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled:  true,
					CertFile: "cert.crt",
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
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
						Message: "Schema must be set and not be nil",
						Path:    []string{"Config", "Schema"},
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
