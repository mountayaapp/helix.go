package httpclient

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"

	"github.com/stretchr/testify/assert"
)

// parsed returns raw parsed, as sanitize stores each endpoint.
func parsed(raw ...string) []*url.URL {
	out := make([]*url.URL, len(raw))
	for i, r := range raw {
		u, err := url.Parse(r)
		if err != nil {
			panic(err)
		}

		out[i] = u
	}

	return out
}

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
				Timeout:  10 * time.Second,
				Failover: FailoverIdempotent,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be set", Path: []any{"config", "endpoints"}},
			),
		},
		{
			// Name is optional here as it is in the other nine integrations: an
			// unnamed client registers under the integration's identifier.
			name: "missing only name is valid",
			before: Config{
				Endpoints: []string{"https://api.tld"},
			},
			after: Config{
				Endpoints: []string{"https://api.tld"},
				Timeout:   10 * time.Second,
				Failover:  FailoverIdempotent,
				urls:      parsed("https://api.tld"),
			},
			err: nil,
		},
		{
			name: "valid config applies defaults",
			before: Config{
				Name:      "api",
				Endpoints: []string{"https://api.tld/v1"},
			},
			after: Config{
				Name:      "api",
				Endpoints: []string{"https://api.tld/v1"},
				Timeout:   10 * time.Second,
				Failover:  FailoverIdempotent,
				urls:      parsed("https://api.tld/v1"),
			},
			err: nil,
		},
		{
			name: "endpoints are normalized",
			before: Config{
				Endpoints: []string{"  https://api.tld/  ", "http://api2.tld/v1///"},
			},
			after: Config{
				Endpoints: []string{"https://api.tld", "http://api2.tld/v1"},
				Timeout:   10 * time.Second,
				Failover:  FailoverIdempotent,
				urls:      parsed("https://api.tld", "http://api2.tld/v1"),
			},
			err: nil,
		},
		{
			name: "malformed endpoint returns indexed error",
			before: Config{
				Endpoints: []string{"://x"},
			},
			after: Config{
				Endpoints: []string{"://x"},
				Timeout:   10 * time.Second,
				Failover:  FailoverIdempotent,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be a valid absolute URL", Path: []any{"config", "endpoints", 0}},
			),
		},
		{
			name: "endpoint without scheme returns indexed error",
			before: Config{
				Endpoints: []string{"nohost"},
			},
			after: Config{
				Endpoints: []string{"nohost"},
				Timeout:   10 * time.Second,
				Failover:  FailoverIdempotent,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: "Must be a valid absolute URL", Path: []any{"config", "endpoints", 0}},
			),
		},
		{
			// Credentials, a query string and a fragment would be written into every
			// request URL, and from there into every error and log line naming one.
			name: "endpoints carrying more than a base URL return indexed errors",
			before: Config{
				Endpoints: []string{
					"https://api.tld",
					"ftp://api.tld",
					"https://user:secret@api.tld",
					"https://api.tld/v1?api_key=secret",
					"https://api.tld?",
					"https://api.tld/v1#section",
				},
			},
			after: Config{
				Endpoints: []string{
					"https://api.tld",
					"ftp://api.tld",
					"https://user:secret@api.tld",
					"https://api.tld/v1?api_key=secret",
					"https://api.tld?",
					"https://api.tld/v1#section",
				},
				Timeout:  10 * time.Second,
				Failover: FailoverIdempotent,
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: `Must use the "http" or "https" scheme`, Path: []any{"config", "endpoints", 1}},
				errorstack.Entry{Message: "Must not contain credentials; set them in headers instead", Path: []any{"config", "endpoints", 2}},
				errorstack.Entry{Message: "Must not contain a query string", Path: []any{"config", "endpoints", 3}},
				errorstack.Entry{Message: "Must not contain a query string", Path: []any{"config", "endpoints", 4}},
				errorstack.Entry{Message: "Must not contain a fragment", Path: []any{"config", "endpoints", 5}},
			),
		},
		{
			name: "headers are copied and canonicalized",
			before: Config{
				Endpoints: []string{"https://api.tld"},
				Headers:   map[string]string{"x-api-key": "key"},
			},
			after: Config{
				Endpoints: []string{"https://api.tld"},
				Headers:   map[string]string{"x-api-key": "key"},
				Timeout:   10 * time.Second,
				Failover:  FailoverIdempotent,
				urls:      parsed("https://api.tld"),
				headers:   http.Header{"X-Api-Key": {"key"}},
			},
			err: nil,
		},
		{
			name: "negative timeout is kept, to disable it",
			before: Config{
				Endpoints: []string{"https://api.tld"},
				Timeout:   -1,
			},
			after: Config{
				Endpoints: []string{"https://api.tld"},
				Timeout:   -1,
				Failover:  FailoverIdempotent,
				urls:      parsed("https://api.tld"),
			},
			err: nil,
		},
		{
			name: "custom timeout, failover and health path are preserved",
			before: Config{
				Endpoints:  []string{"https://api.tld"},
				Timeout:    5 * time.Second,
				Failover:   FailoverUnsent,
				HealthPath: "/healthz",
			},
			after: Config{
				Endpoints:  []string{"https://api.tld"},
				Timeout:    5 * time.Second,
				Failover:   FailoverUnsent,
				HealthPath: "/healthz",
				urls:       parsed("https://api.tld"),
				health:     &url.URL{Path: "/healthz"},
			},
			err: nil,
		},
		{
			name: "unknown failover policy returns error",
			before: Config{
				Endpoints: []string{"https://api.tld"},
				Failover:  "sometimes",
			},
			after: Config{
				Endpoints: []string{"https://api.tld"},
				Timeout:   10 * time.Second,
				Failover:  "sometimes",
				urls:      parsed("https://api.tld"),
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: `Must be one of "idempotent", "unsent" or "always"`, Path: []any{"config", "failover"}},
			),
		},
		{
			name: "health path leaving the endpoint returns error",
			before: Config{
				Endpoints:  []string{"https://api.tld"},
				HealthPath: "@evil.tld/health",
			},
			after: Config{
				Endpoints:  []string{"https://api.tld"},
				Timeout:    10 * time.Second,
				Failover:   FailoverIdempotent,
				HealthPath: "@evil.tld/health",
				urls:       parsed("https://api.tld"),
			},
			err: errorstack.NewValidation(
				errorstack.Entry{Message: `Must start with "/" or "?"`, Path: []any{"config", "health_path"}},
			),
		},
		{
			name: "TLS with a custom transport returns error",
			before: Config{
				Endpoints: []string{"https://api.tld"},
				Transport: http.DefaultTransport,
				TLS:       integration.ConfigTLS{Enabled: true},
			},
			after: Config{
				Endpoints: []string{"https://api.tld"},
				Timeout:   10 * time.Second,
				Failover:  FailoverIdempotent,
				Transport: http.DefaultTransport,
				TLS:       integration.ConfigTLS{Enabled: true},
				urls:      parsed("https://api.tld"),
			},
			err: errorstack.NewValidation(
				errorstack.Entry{
					Message: "Must not be enabled with a custom transport; configure TLS on the transport instead",
					Path:    []any{"config", "tls"},
				},
			),
		},
		{
			name: "TLS with only CertPEM returns error",
			before: Config{
				Endpoints: []string{"https://api.tld"},
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
				},
			},
			after: Config{
				Endpoints: []string{"https://api.tld"},
				Timeout:   10 * time.Second,
				Failover:  FailoverIdempotent,
				urls:      parsed("https://api.tld"),
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
				Endpoints: []string{"https://api.tld"},
				TLS: integration.ConfigTLS{
					Enabled: true,
					CertPEM: []byte("cert"),
					KeyPEM:  []byte("key"),
				},
			},
			after: Config{
				Endpoints: []string{"https://api.tld"},
				Timeout:   10 * time.Second,
				Failover:  FailoverIdempotent,
				urls:      parsed("https://api.tld"),
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

// TestConfig_SanitizeLeavesCallerUntouched pins that sanitize works on copies:
// Config is passed by value, but its slice and map are still the caller's.
func TestConfig_SanitizeLeavesCallerUntouched(t *testing.T) {
	endpoints := []string{"  https://api.tld/  "}
	headers := map[string]string{"x-api-key": "key"}

	cfg := Config{Endpoints: endpoints, Headers: headers}
	assert.NoError(t, cfg.sanitize())

	headers["x-api-key"] = "changed"

	assert.Equal(t, []string{"  https://api.tld/  "}, endpoints)
	assert.Equal(t, "key", cfg.Headers["x-api-key"])
	assert.Equal(t, "key", cfg.headers.Get("X-Api-Key"))
}
