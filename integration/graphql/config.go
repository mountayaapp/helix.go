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
defaultQueryCacheSize is the parsed-document cache size applied when Config
leaves QueryCacheSize unset. It matches the size gqlgen's own default server
uses, so an integration that configures nothing gets the same behaviour the
upstream default would have given it.
*/
const defaultQueryCacheSize = 1000

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

	// Introspection configures GraphQL introspection. When enabled, clients can
	// issue __schema and __type queries to discover the schema shape.
	Introspection ConfigIntrospection `json:"introspection"`

	// APQ configures Automatic Persisted Queries (APQ) caching backed by Valkey.
	// When enabled, clients can send a query hash instead of the full query string,
	// reducing bandwidth on subsequent requests.
	APQ ConfigAPQ `json:"apq"`

	// QueryCacheSize bounds how many PARSED query documents are kept in memory, so a
	// repeated operation is not re-lexed, re-parsed and re-validated against the
	// schema on every request. Defaults to 1000 when unset.
	//
	// This is a different cache from APQ, and the two do not substitute for each
	// other: APQ maps a hash to a query STRING so a client can avoid re-sending it,
	// and the string it resolves to still has to be parsed. An API serving a small
	// set of operations at volume — which is the usual shape — spends most of its
	// parsing budget on documents it has already seen.
	//
	// It is bounded rather than unbounded because the key is the query text, which
	// is caller-supplied: an unbounded map would let clients decide how much memory
	// the process holds.
	QueryCacheSize int `json:"query_cache_size,omitempty"`

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

	// TLS configures TLS for the HTTP server. Only CertPEM and KeyPEM are taken
	// into consideration. PEM-encoded certificate and matching private key for
	// the server must be provided. If the certificate is signed by a certificate
	// authority, the CertPEM should be the concatenation of the server's
	// certificate, any intermediates, and the CA's certificate.
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
ConfigIntrospection configures GraphQL introspection within the GraphQL API.
When enabled, clients can issue introspection queries (__schema, __type, ...)
to discover the schema.
*/
type ConfigIntrospection struct {

	// Enabled enables introspection within the GraphQL API.
	Enabled bool `json:"enabled"`
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

	if cfg.Path == "" {
		cfg.Path = "/graphql"
	}

	if cfg.Schema == nil {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "schema"},
		})
	}

	if cfg.GraphiQL.Enabled && cfg.GraphiQL.Path == "" {
		cfg.GraphiQL.Path = "/graphiql"
	}

	if cfg.QueryCacheSize <= 0 {
		cfg.QueryCacheSize = defaultQueryCacheSize
	}

	if cfg.APQ.Enabled {
		if cfg.APQ.Valkey == nil {
			entries = append(entries, errorstack.Entry{
				Message: "Must be set",
				Path:    []any{"config", "apq", "valkey"},
			})
		}

		if cfg.APQ.Prefix == "" {
			cfg.APQ.Prefix = "apq:"
		}

		if cfg.APQ.TTL == 0 {
			cfg.APQ.TTL = 1 * time.Hour
		}
	}

	entries = append(entries, cfg.TLS.Sanitize()...)
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}
