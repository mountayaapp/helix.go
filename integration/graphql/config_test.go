package graphql

import (
	"testing"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	"github.com/stretchr/testify/assert"
)

var schemaEntry = errorstack.Entry{
	Message: "Must be set",
	Path:    []any{"config", "schema"},
}

var valkeyEntry = errorstack.Entry{
	Message: "Must be set",
	Path:    []any{"config", "apq", "valkey"},
}

var tlsPairEntry = errorstack.Entry{
	Message: "Must be set together; cert_pem and key_pem are required as a pair",
	Path:    []any{"config", "tls"},
}

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
			err: errorstack.NewValidation(schemaEntry),
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
			err: errorstack.NewValidation(schemaEntry),
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
			err: errorstack.NewValidation(schemaEntry, valkeyEntry),
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
			err: errorstack.NewValidation(schemaEntry, valkeyEntry),
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
			err: errorstack.NewValidation(schemaEntry),
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
			err: errorstack.NewValidation(schemaEntry),
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
			err: errorstack.NewValidation(schemaEntry),
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
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "GraphiQL disabled does not apply default path",
			before: Config{
				GraphiQL: ConfigGraphiQL{
					Enabled: false,
					Path:    "",
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				GraphiQL: ConfigGraphiQL{
					Enabled: false,
					Path:    "",
				},
			},
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "Introspection enabled only returns schema error",
			before: Config{
				Introspection: ConfigIntrospection{
					Enabled: true,
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				Introspection: ConfigIntrospection{
					Enabled: true,
				},
			},
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "Introspection disabled only returns schema error",
			before: Config{
				Introspection: ConfigIntrospection{
					Enabled: false,
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				Introspection: ConfigIntrospection{
					Enabled: false,
				},
			},
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "TLS with only CertPEM returns schema and TLS errors",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			err: errorstack.NewValidation(schemaEntry, tlsPairEntry),
		},
		{
			name: "TLS with only KeyPEM returns schema and TLS errors",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				TLS: integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
				},
			},
			err: errorstack.NewValidation(schemaEntry, tlsPairEntry),
		},
		{
			name: "TLS with both CertPEM and KeyPEM returns only schema error",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			err: errorstack.NewValidation(schemaEntry),
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
				Address: ":8080",
				Path:    "/graphql",
				TLS: integration.ConfigTLS{
					Enabled: false,
					CertPEM: []byte("cert"),
				},
			},
			err: errorstack.NewValidation(schemaEntry),
		},
		{
			name: "TLS with InsecureSkipVerify returns only schema error",
			before: Config{
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			after: Config{
				Address: ":8080",
				Path:    "/graphql",
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			err: errorstack.NewValidation(schemaEntry),
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
