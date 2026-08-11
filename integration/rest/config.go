package rest

import (
	"net/http"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
)

/*
Config is used to configure the HTTP REST integration.
*/
type Config struct {

	// Address is the HTTP address to listen on.
	//
	// Default:
	//
	//   ":8080"
	Address string `json:"address"`

	// Readiness allows to define custom logic for the readiness probe endpoint at:
	//
	//   GET /ready
	//
	// It should return 200 if service is ready, or 5xx if an error occurred.
	// Defaults to aggregating the status of all attached dependencies.
	Readiness func(req *http.Request) int `json:"-"`

	// Middleware allows to wrap the built-in HTTP handler with a custom one, for
	// adding a chain of middlewares.
	Middleware func(next http.Handler) http.Handler `json:"-"`

	// OpenAPI configures OpenAPI behavior within the REST API.
	OpenAPI ConfigOpenAPI `json:"openapi"`

	// RequestTimeout bounds how long any route may spend handling a request. The
	// budget rides the request context, so every call derived from it is cancelled
	// once it is spent, and the handler's own error path reports the failure.
	//
	// A route deviates from it with WithTimeout, or waives it with WithoutTimeout
	// when its response is long-lived by design. Zero leaves every route unbounded
	// unless it sets a budget of its own.
	//
	// Default:
	//
	//   0
	RequestTimeout time.Duration `json:"request_timeout"`

	// ReadHeaderTimeout bounds how long a client may take to send its request
	// headers, so a connection that stalls mid-handshake cannot hold a goroutine
	// open indefinitely.
	//
	// Default:
	//
	//   10s
	ReadHeaderTimeout time.Duration `json:"read_header_timeout"`

	// IdleTimeout bounds how long a keep-alive connection may sit idle between
	// requests before the server closes it.
	//
	// Default:
	//
	//   120s
	IdleTimeout time.Duration `json:"idle_timeout"`

	// TLS configures TLS for the HTTP server. Only CertPEM and KeyPEM are taken
	// into consideration. PEM-encoded certificate and matching private key for
	// the server must be provided. If the certificate is signed by a certificate
	// authority, the CertPEM should be the concatenation of the server's
	// certificate, any intermediates, and the CA's certificate.
	TLS integration.ConfigTLS `json:"tls"`
}

/*
ConfigOpenAPI configures OpenAPI behavior within the REST API. When enabled, HTTP
requests and responses are automatically validated againt the description passed.
If a request is not valid, a 4xx error is returned to the client. If a response
is not valid, an error is logged but the response is still returned to the client.
*/
type ConfigOpenAPI struct {

	// Enabled enables OpenAPI within the REST API.
	Enabled bool `json:"enabled"`

	// Description is a path to a local file or a URL containing the OpenAPI
	// description.
	//
	// Examples:
	//
	//   "./descriptions/openapi.yaml"
	//   "http://domain.tld/openapi.yaml"
	Description string `json:"description,omitempty"`
}

/*
sanitize sets default values - when applicable - and validates the configuration.
Returns an error if configuration is not valid.
*/
func (cfg *Config) sanitize() error {
	var entries []errorstack.Entry

	if cfg.Address == "" {
		cfg.Address = ":8080"
	}

	// The two connection-level timeouts default to a value rather than to "off":
	// left unset, a client that never finishes sending its headers holds a
	// goroutine for as long as it likes, and keep-alive connections are never
	// reclaimed. RequestTimeout is deliberately not defaulted — how long a handler
	// legitimately needs is the API's own business, not this package's.
	if cfg.ReadHeaderTimeout == 0 {
		cfg.ReadHeaderTimeout = 10 * time.Second
	}

	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 120 * time.Second
	}

	if cfg.RequestTimeout < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a positive duration",
			Path:    []any{"config", "request_timeout"},
		})
	}

	if cfg.ReadHeaderTimeout < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a positive duration",
			Path:    []any{"config", "read_header_timeout"},
		})
	}

	if cfg.IdleTimeout < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a positive duration",
			Path:    []any{"config", "idle_timeout"},
		})
	}

	if cfg.OpenAPI.Enabled && cfg.OpenAPI.Description == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "openapi", "description"},
		})
	}

	entries = append(entries, cfg.TLS.Sanitize()...)
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}
