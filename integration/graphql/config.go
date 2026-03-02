package graphql

import (
	"net/http"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/integration/valkey"

	gqlgen "github.com/99designs/gqlgen/graphql"
)

/*
ExecutableSchema is a re-export of gqlgen's graphql.ExecutableSchema so that
consumers only need to import this package.
*/
type ExecutableSchema = gqlgen.ExecutableSchema

/*
Config is used to configure the GraphQL integration.
*/
type Config struct {

	// Address is the HTTP address to listen on.
	//
	// Default:
	//
	//   ":8080"
	Address string `json:"address"`

	// Path is the URL path where the GraphQL endpoint is served.
	//
	// Default:
	//
	//   "/graphql"
	Path string `json:"path"`

	// Schema is the gqlgen executable schema to serve.
	Schema gqlgen.ExecutableSchema `json:"-"`

	// GraphiQL configures GraphiQL, a browser-based IDE for exploring and testing
	// the GraphQL API.
	GraphiQL ConfigGraphiQL `json:"graphiql"`

	// APQ configures Automatic Persisted Queries (APQ) caching backed by Valkey.
  // When enabled, clients can send a query hash instead of the full query string,
  // reducing bandwidth on subsequent requests.
	APQ ConfigAPQ `json:"apq"`

	// Healthcheck allows to define custom logic for the healthcheck endpoint at:
	//
	//   GET /health
	//
	// It should return 200 if service is healthy, or 5xx if an error occurred.
	// Returns 200 by default.
	Healthcheck func(req *http.Request) int `json:"-"`

	// Middleware allows to wrap the built-in HTTP handler with a custom one, for
	// adding a chain of middlewares.
	Middleware func(next http.Handler) http.Handler `json:"-"`

	// TLS configures TLS for the HTTP server. Only CertFile and KeyFile are took
	// into consideration. Filenames containing a certificate and matching private
	// key for the server must be provided. If the certificate is signed by a
	// certificate authority, the CertFile should be the concatenation of the
	// server's certificate, any intermediates, and the CA's certificate.
	TLS integration.ConfigTLS `json:"tls"`
}

/*
ConfigGraphiQL configures GraphiQL within the GraphQL API. When enabled, a
browser-based IDE for exploring and testing the GraphQL API is served at the
configured path.
*/
type ConfigGraphiQL struct {

	// Enabled enables GraphiQL within the GraphQL API.
	Enabled bool `json:"enabled"`

	// Path is the URL path where GraphiQL is served.
	//
	// Default:
	//
	//   "/graphiql"
	Path string `json:"path,omitempty"`
}

/*
ConfigAPQ configures Automatic Persisted Queries (APQ) within the GraphQL API.
When enabled, query hashes are cached in Valkey so that clients can send only a
SHA-256 hash instead of the full query string on subsequent requests.
*/
type ConfigAPQ struct {

	// Enabled enables Automatic Persisted Queries within the GraphQL API.
	Enabled bool `json:"enabled"`

	// Prefix is the key prefix used when storing cached queries in Valkey.
	//
	// Default:
	//
	//   "apq:"
	Prefix string `json:"prefix,omitempty"`

	// TTL is the time-to-live for cached queries in Valkey.
	//
	// Default:
	//
	//   1h
	TTL time.Duration `json:"ttl,omitempty"`

	// Valkey is the Valkey integration instance used to store cached queries.
	Valkey valkey.Valkey `json:"-"`
}

/*
sanitize sets default values - when applicable - and validates the configuration.
Returns an error if configuration is not valid.
*/
func (cfg *Config) sanitize() error {
	stack := errorstack.New("Failed to validate configuration", errorstack.WithIntegration(identifier))

	if cfg.Address == "" {
		cfg.Address = ":8080"
	}

	if cfg.Path == "" {
		cfg.Path = "/graphql"
	}

	if cfg.Schema == nil {
		stack.WithValidations(errorstack.Validation{
			Message: "Schema must be set and not be nil",
			Path:    []string{"Config", "Schema"},
		})
	}

	if cfg.GraphiQL.Enabled {
		if cfg.GraphiQL.Path == "" {
			cfg.GraphiQL.Path = "/graphiql"
		}
	}

	if cfg.APQ.Enabled {
		if cfg.APQ.Valkey == nil {
			stack.WithValidations(errorstack.Validation{
				Message: "Valkey must be set and not be nil when APQ is enabled",
				Path:    []string{"Config", "APQ", "Valkey"},
			})
		}

		if cfg.APQ.Prefix == "" {
			cfg.APQ.Prefix = "apq:"
		}

		if cfg.APQ.TTL == 0 {
			cfg.APQ.TTL = 1 * time.Hour
		}
	}

	stack.WithValidations(cfg.TLS.Sanitize()...)
	if stack.HasValidations() {
		return stack
	}

	return nil
}
