package httpclient

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
)

/*
Config is used to configure the HTTP client integration.
*/
type Config struct {

	// Name is a human-readable name for the client, used for logging, tracing,
	// and status/error context. It should be unique across attached dependencies.
	//
	// Required.
	Name string `json:"name"`

	// Endpoints is the list of interchangeable base URLs the client targets.
	// Requests are round-robined across them with automatic failover, and the
	// health of each one is reported individually through Status.
	//
	// At least one endpoint is required. Trailing slashes and surrounding
	// whitespace are trimmed.
	//
	// Examples:
	//
	//   []string{"https://api.tld"}
	//   []string{"https://api-1.tld", "https://api-2.tld"}
	Endpoints []string `json:"endpoints"`

	// Headers are set on every request issued by the client when not already
	// present on the request. Typically used for content negotiation or
	// authentication.
	Headers map[string]string `json:"-"`

	// Timeout bounds each individual request.
	//
	// Default:
	//
	//   10 * time.Second
	Timeout time.Duration `json:"timeout"`

	// HealthPath is the path probed on each endpoint by the default Status check.
	// An endpoint is healthy as long as it is reachable and responds with a status
	// below 500, so an API that exposes no health endpoint (returning 404) is still
	// healthy. Ignored when Status is set.
	//
	// Default:
	//
	//   "/health"
	HealthPath string `json:"health_path"`

	// Status overrides how the client's health is determined. It receives the
	// client so it can issue probe requests, and should return 200 when healthy,
	// or a 5xx status and an error otherwise. Use it for stricter checks (e.g.
	// requiring a 2xx from a real health endpoint) or to skip probing entirely
	// (return 200). When nil, every endpoint is probed with GET
	// {endpoint}{HealthPath} and considered healthy when it responds below 500.
	Status func(ctx context.Context, client HTTPClient) (int, error) `json:"-"`

	// TLS configures TLS for the underlying HTTP transport.
	TLS integration.ConfigTLS `json:"tls"`
}

/*
sanitize sets default values - when applicable - and validates the configuration.
Returns an error if configuration is not valid.
*/
func (cfg *Config) sanitize() error {
	var entries []errorstack.Entry

	if cfg.Name == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "name"},
		})
	}

	if len(cfg.Endpoints) == 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "endpoints"},
		})
	} else {
		for i, endpoint := range cfg.Endpoints {
			endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
			cfg.Endpoints[i] = endpoint

			if u, err := url.Parse(endpoint); err != nil || u.Scheme == "" || u.Host == "" {
				entries = append(entries, errorstack.Entry{
					Message: "Must be a valid absolute URL",
					Path:    []any{"config", "endpoints", i},
				})
			}
		}
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}

	if cfg.HealthPath == "" {
		cfg.HealthPath = "/health"
	}

	entries = append(entries, cfg.TLS.Sanitize()...)
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}
