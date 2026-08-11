package rest

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
			name:   "empty config applies default address",
			before: Config{},
			after: Config{
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
			},
			err: nil,
		},
		{
			name: "custom address is preserved",
			before: Config{
				Address: ":9090",
			},
			after: Config{
				Address:           ":9090",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
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
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
				OpenAPI: ConfigOpenAPI{
					Enabled: true,
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "openapi", "description"}},
			),
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
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
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
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
				OpenAPI: ConfigOpenAPI{
					Enabled: false,
				},
			},
			err: nil,
		},
		{
			name: "OpenAPI disabled ignores empty description",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled:     false,
					Description: "",
				},
			},
			after: Config{
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
				OpenAPI: ConfigOpenAPI{
					Enabled:     false,
					Description: "",
				},
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
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be set together; cert_pem and key_pem are required as a pair",
					Path:    []any{"config", "tls"},
				},
			),
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
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
				TLS: integration.ConfigTLS{
					Enabled: true,
					KeyPEM:  []byte("key"),
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must be set together; cert_pem and key_pem are required as a pair",
					Path:    []any{"config", "tls"},
				},
			),
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
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
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
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
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
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
				TLS: integration.ConfigTLS{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			err: nil,
		},
		{
			name: "OpenAPI and TLS both invalid returns combined errors",
			before: Config{
				OpenAPI: ConfigOpenAPI{
					Enabled: true,
				},
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
				OpenAPI: ConfigOpenAPI{
					Enabled: true,
				},
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "openapi", "description"}},
				errorstack.Entry{
					Message: "Must be set together; cert_pem and key_pem are required as a pair",
					Path:    []any{"config", "tls"},
				},
			),
		},
		{
			name: "custom timeouts are preserved",
			before: Config{
				IdleTimeout:       30 * time.Second,
				ReadHeaderTimeout: 5 * time.Second,
				RequestTimeout:    45 * time.Second,
			},
			after: Config{
				Address:           ":8080",
				IdleTimeout:       30 * time.Second,
				ReadHeaderTimeout: 5 * time.Second,
				RequestTimeout:    45 * time.Second,
			},
			err: nil,
		},
		{
			name: "zero RequestTimeout is left alone rather than defaulted",
			before: Config{
				RequestTimeout: 0,
			},
			after: Config{
				Address:           ":8080",
				IdleTimeout:       120 * time.Second,
				ReadHeaderTimeout: 10 * time.Second,
			},
			err: nil,
		},
		{
			name: "negative timeouts return errors",
			before: Config{
				IdleTimeout:       -1 * time.Second,
				ReadHeaderTimeout: -1 * time.Second,
				RequestTimeout:    -1 * time.Second,
			},
			after: Config{
				Address:           ":8080",
				IdleTimeout:       -1 * time.Second,
				ReadHeaderTimeout: -1 * time.Second,
				RequestTimeout:    -1 * time.Second,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "request_timeout"}},
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "read_header_timeout"}},
				errorstack.Entry{Message: "Must be a positive duration", Path: []any{"config", "idle_timeout"}},
			),
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
