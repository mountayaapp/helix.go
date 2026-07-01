package httpclient

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
			name:   "empty config returns required field errors",
			before: Config{},
			after: Config{
				Timeout:    10 * time.Second,
				HealthPath: "/health",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "name"}},
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "endpoints"}},
			),
		},
		{
			name: "missing only name returns name error",
			before: Config{
				Endpoints: []string{"https://api.tld"},
			},
			after: Config{
				Endpoints:  []string{"https://api.tld"},
				Timeout:    10 * time.Second,
				HealthPath: "/health",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "name"}},
			),
		},
		{
			name: "missing only endpoints returns endpoints error",
			before: Config{
				Name: "api",
			},
			after: Config{
				Name:       "api",
				Timeout:    10 * time.Second,
				HealthPath: "/health",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "endpoints"}},
			),
		},
		{
			name: "valid config applies defaults",
			before: Config{
				Name:      "api",
				Endpoints: []string{"https://api.tld"},
			},
			after: Config{
				Name:       "api",
				Endpoints:  []string{"https://api.tld"},
				Timeout:    10 * time.Second,
				HealthPath: "/health",
			},
			err: nil,
		},
		{
			name: "endpoints are normalized",
			before: Config{
				Name:      "api",
				Endpoints: []string{"  https://api.tld/  ", "https://api2.tld///"},
			},
			after: Config{
				Name:       "api",
				Endpoints:  []string{"https://api.tld", "https://api2.tld"},
				Timeout:    10 * time.Second,
				HealthPath: "/health",
			},
			err: nil,
		},
		{
			name: "malformed endpoint returns indexed error",
			before: Config{
				Name:      "api",
				Endpoints: []string{"://x"},
			},
			after: Config{
				Name:       "api",
				Endpoints:  []string{"://x"},
				Timeout:    10 * time.Second,
				HealthPath: "/health",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be a valid absolute URL", Path: []any{"config", "endpoints", 0}},
			),
		},
		{
			name: "endpoint without scheme returns indexed error",
			before: Config{
				Name:      "api",
				Endpoints: []string{"nohost"},
			},
			after: Config{
				Name:       "api",
				Endpoints:  []string{"nohost"},
				Timeout:    10 * time.Second,
				HealthPath: "/health",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be a valid absolute URL", Path: []any{"config", "endpoints", 0}},
			),
		},
		{
			name: "only the invalid endpoint of a mixed list errors",
			before: Config{
				Name:      "api",
				Endpoints: []string{"https://api.tld", "://x"},
			},
			after: Config{
				Name:       "api",
				Endpoints:  []string{"https://api.tld", "://x"},
				Timeout:    10 * time.Second,
				HealthPath: "/health",
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be a valid absolute URL", Path: []any{"config", "endpoints", 1}},
			),
		},
		{
			name: "non-positive timeout is defaulted",
			before: Config{
				Name:      "api",
				Endpoints: []string{"https://api.tld"},
				Timeout:   -1,
			},
			after: Config{
				Name:       "api",
				Endpoints:  []string{"https://api.tld"},
				Timeout:    10 * time.Second,
				HealthPath: "/health",
			},
			err: nil,
		},
		{
			name: "custom timeout and health path are preserved",
			before: Config{
				Name:       "api",
				Endpoints:  []string{"https://api.tld"},
				Timeout:    5 * time.Second,
				HealthPath: "/healthz",
			},
			after: Config{
				Name:       "api",
				Endpoints:  []string{"https://api.tld"},
				Timeout:    5 * time.Second,
				HealthPath: "/healthz",
			},
			err: nil,
		},
		{
			name: "TLS with only CertPEM returns error",
			before: Config{
				Name:      "api",
				Endpoints: []string{"https://api.tld"},
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Name:       "api",
				Endpoints:  []string{"https://api.tld"},
				Timeout:    10 * time.Second,
				HealthPath: "/health",
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
			name: "TLS with both CertPEM and KeyPEM is valid",
			before: Config{
				Name:      "api",
				Endpoints: []string{"https://api.tld"},
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Name:       "api",
				Endpoints:  []string{"https://api.tld"},
				Timeout:    10 * time.Second,
				HealthPath: "/health",
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
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
