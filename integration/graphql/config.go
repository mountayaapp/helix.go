package graphql

import (
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
)

// ExecutableSchema is a re-export of gqlgen's graphql.ExecutableSchema so that
// consumers only need to import this package.
type ExecutableSchema = gqlgen.ExecutableSchema

// Defaults applied by sanitize when Config leaves a field unset.
const (
	// defaultQueryCacheSize matches the size gqlgen's own default server uses, so
	// an integration that configures nothing gets the same behaviour the upstream
	// default would have given it.
	defaultQueryCacheSize = 1000

	// defaultParserTokenLimit bounds the lexer before anything else runs. A
	// realistic operation is a few hundred tokens; a generated one rarely reaches
	// a few thousand.
	defaultParserTokenLimit = 15000

	// defaultComplexityLimit is about a thousand selected fields under gqlgen's
	// default cost of one per field, which no hand-written operation comes close
	// to and an alias-multiplied one easily exceeds.
	defaultComplexityLimit = 1000

	// defaultDepthLimit leaves room for Relay-style connections, which spend two
	// levels (edges, node) on every hop, while refusing the self-referencing
	// friends { friends { friends ... } } shape that costs nothing to send.
	defaultDepthLimit = 15

	// defaultMaxRequestBodyBytes is 10 MiB.
	defaultMaxRequestBodyBytes = 10 << 20

	// defaultReadTimeout bounds how long a client may take to send a whole
	// request. GraphQL is served over POST only, with no streaming transport, so
	// nothing legitimate needs longer.
	defaultReadTimeout = 30 * time.Second

	// defaultAPQMaxQueryLength is 32 KiB.
	defaultAPQMaxQueryLength = 32 << 10
)

// ResolverTracing selects which resolvers get a span of their own.
type ResolverTracing string

// The resolver tracing modes Config.TraceResolvers accepts.
const (
	// ResolverTracingRoot traces the resolvers of root fields only: the fields of
	// Query, Mutation and Subscription. It is the default.
	//
	// A root field is where an operation reaches out to a database or another
	// service, and their number is bounded by the document the client sent. A
	// nested resolver runs once per parent item, so a list of a thousand items
	// with one resolver each is a thousand spans for one request.
	ResolverTracingRoot ResolverTracing = "root"

	// ResolverTracingAll traces every field backed by a resolver, at any depth.
	// Use it when a slow nested resolver is what needs finding, and mind the
	// span volume it produces.
	ResolverTracingAll ResolverTracing = "all"

	// ResolverTracingNone traces no resolver: the operation span is the finest
	// grain.
	ResolverTracingNone ResolverTracing = "none"
)

// Config is used to configure the GraphQL integration.
type Config struct {
	// Name identifies this server in health output and error context. Defaults to
	// the integration's identifier when left empty.
	Name string `json:"name"`

	// Address is the HTTP address to listen on.
	//
	// Default:
	//
	//   ":8080"
	Address string `json:"address"`

	// Path is the URL path where the GraphQL endpoint is served. It must start
	// with "/", be a clean path without wildcards, and be neither "/" nor one of
	// the health probe paths: the probes bypass Middleware, so a GraphQL endpoint
	// served on one of their paths would bypass it too.
	//
	// Default:
	//
	//   "/graphql"
	Path string `json:"path"`

	// Schema is the gqlgen executable schema to serve. Required.
	Schema gqlgen.ExecutableSchema `json:"-"`

	// PublicEndpoint marks a server reachable from outside the trust boundary of
	// the services it belongs to — from the internet, typically. Such a server
	// does not continue the caller's trace, but starts a new one linked to it,
	// and it ignores the caller's baggage, so that nothing a client puts in its
	// headers is propagated onward as though a trusted service had set it.
	//
	// Leave it unset for an API only other services of yours call.
	PublicEndpoint bool `json:"public_endpoint"`

	// GraphiQL configures GraphiQL, a browser-based IDE for exploring and testing
	// the GraphQL API.
	GraphiQL ConfigGraphiQL `json:"graphiql"`

	// Introspection configures GraphQL introspection. When enabled, clients can
	// issue __schema and __type queries to discover the schema shape.
	Introspection ConfigIntrospection `json:"introspection"`

	// APQ configures Automatic Persisted Queries. When enabled, clients can send a
	// query hash instead of the full query string, reducing bandwidth on
	// subsequent requests.
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
	QueryCacheSize int `json:"query_cache_size,omitzero"`

	// ParserTokenLimit bounds how many tokens the parser reads from a query
	// before refusing it, so that a client cannot make the server lex megabytes
	// of document it will never execute.
	//
	// Default:
	//
	//   15000
	ParserTokenLimit int `json:"parser_token_limit,omitzero"`

	// ComplexityLimit refuses, before it runs, an operation whose complexity
	// exceeds it. Complexity is computed by gqlgen: one per selected field unless
	// the schema declares a cost of its own through gqlgen's complexity
	// functions. A refused operation is answered with the code
	// COMPLEXITY_LIMIT_EXCEEDED.
	//
	// Default:
	//
	//   1000
	ComplexityLimit int `json:"complexity_limit,omitzero"`

	// DepthLimit refuses, before it runs, an operation whose selections nest
	// deeper than it, fragments included. A root field is at depth one.
	// Introspection fields are not counted: they are bounded by gqlparser's own
	// introspection depth rule, and only served when Introspection is enabled. A
	// refused operation is answered with the code DEPTH_LIMIT_EXCEEDED.
	//
	// Default:
	//
	//   15
	DepthLimit int `json:"depth_limit,omitzero"`

	// MaxRequestBodyBytes bounds the size of a request body. A larger request is
	// refused with 413 before the GraphQL layer sees any of it.
	//
	// Default:
	//
	//   10485760 (10 MiB)
	MaxRequestBodyBytes int64 `json:"max_request_body_bytes,omitzero"`

	// TraceResolvers selects which resolvers get a span of their own, under the
	// operation's. See ResolverTracing for why root fields only is the default.
	//
	// Default:
	//
	//   ResolverTracingRoot
	TraceResolvers ResolverTracing `json:"trace_resolvers,omitempty"`

	// RequestTimeout bounds how long a GraphQL request may take, from the moment
	// its body has been read. The budget rides the request context, so every call
	// a resolver derives from it is cancelled once it is spent. Zero leaves
	// requests unbounded: how long a resolver legitimately needs is the API's own
	// business.
	//
	// Default:
	//
	//   0
	RequestTimeout time.Duration `json:"request_timeout,omitzero"`

	// ReadHeaderTimeout bounds how long a client may take to send its request
	// headers, so a connection that stalls mid-request cannot hold a goroutine
	// open indefinitely.
	//
	// Default:
	//
	//   10s
	ReadHeaderTimeout time.Duration `json:"read_header_timeout,omitzero"`

	// ReadTimeout bounds how long a client may take to send a whole request,
	// headers and body.
	//
	// Default:
	//
	//   30s
	ReadTimeout time.Duration `json:"read_timeout,omitzero"`

	// IdleTimeout bounds how long a keep-alive connection may sit idle between
	// requests before the server closes it.
	//
	// Default:
	//
	//   120s
	IdleTimeout time.Duration `json:"idle_timeout,omitzero"`

	// Readiness allows to define custom logic for the readiness probe endpoint at:
	//
	//   GET /ready
	//
	// It should return 200 if service is ready, or 5xx if an error occurred.
	// Defaults to aggregating the status of all attached dependencies.
	Readiness func(req *http.Request) int `json:"-"`

	// Middleware allows to wrap the built-in HTTP handler with a custom one, for
	// adding a chain of middlewares. The health probes bypass it.
	Middleware func(next http.Handler) http.Handler `json:"-"`

	// Configure is called with the gqlgen server once this integration has
	// configured it, as the last step of New. It is the escape hatch for what
	// Config does not expose: an extension of your own, a custom validation rule.
	//
	// Whatever it changes is changed on top of this integration's choices, and
	// replacing them gives up what they provide: SetErrorPresenter replaces the
	// error masking, SetRecoverFunc the panic logging. Only POST and OPTIONS
	// requests are routed to the server, so adding another transport has no
	// effect.
	Configure func(srv *handler.Server) `json:"-"`

	// TLS configures TLS for the HTTP server. Only CertPEM, KeyPEM and
	// ClientCAPEMs are taken into consideration. PEM-encoded certificate and
	// matching private key for the server must be provided. If the certificate is
	// signed by a certificate authority, the CertPEM should be the concatenation
	// of the server's certificate, any intermediates, and the CA's certificate.
	// ClientCAPEMs, when set, requires clients to present a certificate signed by
	// one of them.
	TLS integration.ConfigTLS `json:"tls"`
}

// ConfigGraphiQL configures GraphiQL within the GraphQL API. When enabled, a
// browser-based IDE for exploring and testing the GraphQL API is served at the
// configured path.
//
// GraphiQL loads the schema through introspection, so it is of little use unless
// Introspection is enabled too. Enabling one does not enable the other.
type ConfigGraphiQL struct {

	// Enabled enables GraphiQL within the GraphQL API.
	Enabled bool `json:"enabled"`

	// Path is the URL path where GraphiQL is served. It follows the rules of
	// Config.Path, and must differ from it.
	//
	// Default:
	//
	//   "/graphiql"
	Path string `json:"path,omitempty"`
}

// ConfigAPQ configures Automatic Persisted Queries (APQ) within the GraphQL API.
// When enabled, queries are stored in Cache keyed by their SHA-256 hash, so that
// clients can send only the hash instead of the full query string on subsequent
// requests.
//
// A query is stored only once it has parsed, validated and passed the depth and
// complexity limits, and only when it is no longer than MaxQueryLength: the hash
// a client sends is whatever it chose, and the store is shared by every client.
type ConfigAPQ struct {

	// Enabled enables Automatic Persisted Queries within the GraphQL API.
	Enabled bool `json:"enabled"`

	// Cache stores the persisted queries. Required when enabled.
	Cache Cache `json:"-"`

	// MaxQueryLength is the length, in bytes, of the longest query stored. A
	// longer query is still executed; it is simply not persisted, and a client
	// that later sends only its hash is asked for the full query again.
	//
	// Default:
	//
	//   32768 (32 KiB)
	MaxQueryLength int `json:"max_query_length,omitzero"`
}

// ConfigIntrospection configures GraphQL introspection within the GraphQL API.
// When enabled, clients can issue introspection queries (__schema, __type, ...)
// to discover the schema.
//
// When disabled, field suggestions ("Did you mean ...?") are disabled too: they
// would otherwise reveal the schema one validation error at a time.
type ConfigIntrospection struct {

	// Enabled enables introspection within the GraphQL API.
	Enabled bool `json:"enabled"`
}

// sanitize sets default values - when applicable - and validates the configuration.
// Returns an error if configuration is not valid.
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

	// A path the serve mux cannot parse makes it panic when New registers it, and
	// one shared with a probe would put the GraphQL endpoint outside Middleware.
	if msg := checkPath(cfg.Path); msg != "" {
		entries = append(entries, errorstack.Entry{
			Message: msg,
			Path:    []any{"config", "path"},
		})
	}

	if cfg.GraphiQL.Enabled {
		if cfg.GraphiQL.Path == "" {
			cfg.GraphiQL.Path = "/graphiql"
		}

		msg := checkPath(cfg.GraphiQL.Path)
		if msg == "" && cfg.GraphiQL.Path == cfg.Path {
			msg = "Must differ from path"
		}

		if msg != "" {
			entries = append(entries, errorstack.Entry{
				Message: msg,
				Path:    []any{"config", "graphiql", "path"},
			})
		}
	}

	if cfg.QueryCacheSize <= 0 {
		cfg.QueryCacheSize = defaultQueryCacheSize
	}

	if cfg.APQ.Enabled {
		if cfg.APQ.Cache == nil {
			entries = append(entries, errorstack.Entry{
				Message: "Must be set",
				Path:    []any{"config", "apq", "cache"},
			})
		}

		entries = append(entries, defaultPositive(&cfg.APQ.MaxQueryLength, defaultAPQMaxQueryLength, "config", "apq", "max_query_length")...)
	}

	entries = append(entries, defaultPositive(&cfg.ParserTokenLimit, defaultParserTokenLimit, "config", "parser_token_limit")...)
	entries = append(entries, defaultPositive(&cfg.ComplexityLimit, defaultComplexityLimit, "config", "complexity_limit")...)
	entries = append(entries, defaultPositive(&cfg.DepthLimit, defaultDepthLimit, "config", "depth_limit")...)
	entries = append(entries, defaultPositive(&cfg.MaxRequestBodyBytes, defaultMaxRequestBodyBytes, "config", "max_request_body_bytes")...)

	switch cfg.TraceResolvers {
	case "":
		cfg.TraceResolvers = ResolverTracingRoot
	case ResolverTracingRoot, ResolverTracingAll, ResolverTracingNone:
	default:
		entries = append(entries, errorstack.Entry{
			Message: `Must be one of "root", "all" or "none"`,
			Path:    []any{"config", "trace_resolvers"},
		})
	}

	// The connection-level timeouts default to a value rather than to "off": left
	// unset, a client that never finishes sending its request holds a goroutine
	// for as long as it likes. RequestTimeout is deliberately not defaulted.
	if cfg.RequestTimeout < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must not be negative",
			Path:    []any{"config", "request_timeout"},
		})
	}

	entries = append(entries, defaultPositive(&cfg.ReadHeaderTimeout, integration.DefaultReadHeaderTimeout, "config", "read_header_timeout")...)
	entries = append(entries, defaultPositive(&cfg.ReadTimeout, defaultReadTimeout, "config", "read_timeout")...)
	entries = append(entries, defaultPositive(&cfg.IdleTimeout, integration.DefaultIdleTimeout, "config", "idle_timeout")...)

	entries = append(entries, cfg.TLS.Sanitize()...)
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}

// name returns the name the server registers under, falling back to the
// integration's identifier when the Config leaves it empty.
func (cfg *Config) name() string {
	if cfg.Name != "" {
		return cfg.Name
	}

	return identifier
}

// defaultPositive sets *v to def when it is zero, and returns the validation entry
// for a negative value. A negative limit or timeout is a mistake rather than a
// way to turn it off: an unset one already means the default.
func defaultPositive[T ~int | ~int64](v *T, def T, at ...any) []errorstack.Entry {
	switch {
	case *v == 0:
		*v = def
	case *v < 0:
		return []errorstack.Entry{{
			Message: "Must not be negative",
			Path:    at,
		}}
	}

	return nil
}

// checkPath returns what is wrong with p as the path of an endpoint this
// integration serves, or the empty string when nothing is.
func checkPath(p string) string {
	switch {
	case !strings.HasPrefix(p, "/"):
		return `Must start with "/"`
	case p == "/":
		return `Must not be "/"`
	case path.Clean(p) != p, strings.ContainsAny(p, "{}?#"), strings.ContainsFunc(p, unicode.IsSpace):
		return "Must be a clean path, without wildcards, query, fragment or whitespace"
	case p == integration.PathLiveness, p == integration.PathReadiness:
		return "Must not be a health probe path"
	}

	return ""
}
