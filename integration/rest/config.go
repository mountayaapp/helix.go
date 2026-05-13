package rest

import (
	"net/http"

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

	// TLS configures TLS for the HTTP server. Only CertPEM and KeyPEM are took
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
