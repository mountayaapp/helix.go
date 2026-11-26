package rest

import (
	"net/http"
	"time"

	"github.com/mountaya/helix.go/errorstack"
	"github.com/mountaya/helix.go/integration"
)

// DefaultMaxRequestBodyBytes is the request body limit a server applies when
// Config.MaxRequestBodyBytes is left at zero: 10 MiB.
const DefaultMaxRequestBodyBytes int64 = 10 << 20

// Config is used to configure the HTTP REST integration.
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

	// PublicEndpoint marks a server reachable from outside the trust boundary of
	// the services it belongs to — from the internet, typically.
	//
	// Such a server does not continue the caller's trace: each request starts a new
	// one, linked to the caller's span so the two can still be navigated between.
	// And it ignores the caller's baggage, so an Event a client put in its headers
	// is never propagated onward as though a trusted upstream service had set it.
	// Leave it unset for a server only other services of yours call: continuing
	// their trace and their baggage is what makes a request crossing several
	// services one trace.
	//
	// Default:
	//
	//   false
	PublicEndpoint bool `json:"public_endpoint"`

	// Readiness allows to define custom logic for the readiness probe endpoint at:
	//
	//   GET /ready
	//
	// It should return 200 if service is ready, or 5xx if an error occurred.
	// Defaults to checking the health of every integration attached to the
	// Service.
	Readiness func(req *http.Request) int `json:"-"`

	// Middleware allows to wrap the built-in HTTP handler with a custom one, for
	// adding a chain of middlewares. The probes, GET /health and GET /ready, are
	// never wrapped by it.
	//
	// A request whose path names a route only once it is cleaned up, or once a
	// trailing slash is dropped or added, never reaches it: it is answered with a
	// 308 to the route's own path, for a GET or a HEAD, and with a 404 for any other
	// method. See the README's Routes section. That makes it the place for work
	// that should not be spent on such a request: authentication, rate limiting.
	//
	// The request's entry span is already open when it runs, so a response it
	// writes itself — a 401 from authentication, a 429 from a rate limiter — is
	// traced and measured like any other. An Event it puts in the request context
	// is stamped onto that span once it hands the request on.
	Middleware func(next http.Handler) http.Handler `json:"-"`

	// EdgeMiddleware wraps every response the server gives but the probes': the
	// ones Middleware and the routes write, and the ones this package writes before
	// Middleware runs — the 403 of CrossOriginProtection, the 413 of
	// MaxRequestBodyBytes, the 308 or 404 of a non-canonical path.
	//
	// It is the place for what every response must carry whoever writes it: CORS
	// headers, so a browser lets its script read a refusal too, and the headers an
	// API stamps on all of its responses. Work that should not be spent on a
	// request no route serves as written belongs in Middleware instead, which runs
	// inside it.
	//
	// The request's entry span is already open when it runs, so a response it
	// writes itself, a CORS preflight above all, is traced and measured like any
	// other. An Event it puts in the request context is stamped onto that span once
	// the request reaches the router.
	//
	// A panic in anything it wraps, Middleware and the routes included, is answered
	// just inside it with the 500 error envelope, so the headers it set before
	// handing the request on are on that answer too. A panic in EdgeMiddleware
	// itself is not answered: it reaches net/http, which aborts the response.
	EdgeMiddleware func(next http.Handler) http.Handler `json:"-"`

	// CrossOriginProtection, when set, rejects non-safe cross-origin requests made
	// by a browser — the shape of a cross-site request forgery against an API that
	// authenticates with cookies. A rejected request is answered 403 with the REST
	// error envelope, before Middleware runs; the protection's own deny handler is
	// not used. Configure trusted origins and bypass patterns on it as usual.
	//
	// Leave it nil for an API that authenticates with a header a browser does not
	// send on its own, such as Authorization: a forged request cannot carry it.
	//
	// Example:
	//
	//   http.NewCrossOriginProtection()
	CrossOriginProtection *http.CrossOriginProtection `json:"-"`

	// OpenAPI configures OpenAPI behavior within the REST API.
	OpenAPI ConfigOpenAPI `json:"openapi"`

	// RecordRouteParams records the value of every matched path parameter on the
	// request's span, as "rest.route.param.<name>". It is off by default because a
	// path routinely carries what a trace backend should not keep: an email
	// address, a token, a customer identifier.
	//
	// Default:
	//
	//   false
	RecordRouteParams bool `json:"record_route_params"`

	// NoImplicitHead stops a GET route from answering HEAD on its own.
	//
	// By default every GET route answers HEAD too, with the GET's handler, options
	// and validation: net/http sends the headers the handler writes and drops its
	// body. A route that must answer HEAD with a handler of its own registers it
	// before the GET of the same path, which then implies none; registering it after
	// panics, since the GET has already claimed it.
	//
	// Default:
	//
	//   false
	NoImplicitHead bool `json:"no_implicit_head"`

	// MaxRequestBodyBytes bounds the size of a request body. A request declaring a
	// larger Content-Length is answered 413 before anything reads it. A body sent
	// without one is cut off at the limit: reading past it fails with an
	// *http.MaxBytesError, which the OpenAPI validation answers with 413, and which
	// a handler reading the body itself should answer the same way.
	//
	// The limit applies to every route, and to Middleware.
	//
	// Default:
	//
	//   10 MiB
	MaxRequestBodyBytes int64 `json:"max_request_body_bytes"`

	// MaxHeaderBytes bounds the size of the request line and headers. Zero leaves
	// net/http's own default of 1 MiB.
	//
	// Default:
	//
	//   0
	MaxHeaderBytes int `json:"max_header_bytes"`

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

	// ReadTimeout bounds how long a client may take to send its whole request,
	// body included. Zero leaves it unbounded, which is what a route accepting a
	// large upload over a slow connection needs.
	//
	// Default:
	//
	//   0
	ReadTimeout time.Duration `json:"read_timeout"`

	// WriteTimeout is an absolute deadline on writing each response, counted from
	// the end of the request headers. It knows nothing of the request context, so
	// it severs every long-lived response — server-sent events above all — once it
	// is reached. Bounding how long a handler runs is RequestTimeout's job; leave
	// this at zero unless no route streams.
	//
	// Default:
	//
	//   0
	WriteTimeout time.Duration `json:"write_timeout"`

	// IdleTimeout bounds how long a keep-alive connection may sit idle between
	// requests before the server closes it.
	//
	// Default:
	//
	//   120s
	IdleTimeout time.Duration `json:"idle_timeout"`

	// TLS configures TLS for the HTTP server. When enabled, CertPEM and KeyPEM are
	// required: the PEM-encoded certificate and matching private key. If the
	// certificate is signed by a certificate authority, CertPEM should be the
	// concatenation of the server's certificate, any intermediates, and the CA's
	// certificate. Setting ClientCAPEMs turns on mutual TLS. The client-only fields
	// are ignored.
	TLS integration.ConfigTLS `json:"tls"`
}

// ConfigOpenAPI configures OpenAPI behavior within the REST API. When enabled,
// HTTP requests and responses are validated against the descriptions passed. A
// request that is not valid is refused with a 400 — or a 413 when its body is
// over the limit — and never reaches its handler. A response that is not valid
// is still returned to the client, and the failure is recorded as an error on
// the request's trace.
type ConfigOpenAPI struct {

	// Enabled enables OpenAPI within the REST API.
	Enabled bool `json:"enabled"`

	// Descriptions lists the OpenAPI descriptions the API is validated against, each
	// a path to a local file or a URL. At least one must be set when Enabled.
	//
	// Several are merged into one document before the validation router is built,
	// which is how an API served from one process but described by more than one
	// document gets every route validated. A route the descriptions do not describe
	// is not validated at all, so a server that registers more routes than its
	// description declares is a hole rather than a stricter server.
	//
	// The first entry is the base: it carries the document's info, servers and
	// security, and the others contribute their paths and components to it. A path
	// item is merged operation by operation, so two descriptions may each declare a
	// different method on the same path. Two declaring the same operation, or the
	// same component name, is refused: a name belongs to exactly one description,
	// and a duplicate is a mistake to report rather than a precedence question to
	// resolve quietly. The merged document must be a valid OpenAPI description.
	//
	// The host of each server URL is ignored, so that a description naming its
	// production host still validates a server running anywhere else. Its path is
	// kept: with a server "https://api.example.com/v1", a path "/things" describes
	// requests to "/v1/things". A service behind a proxy that strips that prefix
	// should list a server for the path it actually sees — "/", for example.
	//
	// Examples:
	//
	//   []string{"./descriptions/openapi.yaml"}
	//   []string{"./descriptions/public.yaml", "./descriptions/internal.yaml"}
	//   []string{"http://domain.tld/openapi.yaml"}
	Descriptions []string `json:"descriptions,omitempty"`

	// RefuseUndeclaredQuery refuses a request carrying a query parameter its
	// operation does not declare, with a 400 entry at ["request", "query", name]
	// for each one, beside every other validation failure of the request.
	//
	// Off, a parameter the description does not declare is not looked at, which
	// is how OpenAPI validation behaves by default: a misspelled filter is then
	// silently ignored, and the caller reads an unfiltered answer as a filtered
	// one. On, an API whose description says "a query parameter the operation
	// does not declare is refused" holds every operation to it in one place.
	//
	// A parameter is declared by the operation, or by its path item, or as the
	// name of an apiKey security scheme carried in the query that one of the
	// operation's security requirements names, the document's when the operation
	// has none of its own. A route admits further names with WithUndeclaredQuery.
	// A route no description declares is not validated, and is left alone.
	RefuseUndeclaredQuery bool `json:"refuse_undeclared_query"`
}

// sanitize sets default values - when applicable - and validates the configuration.
// Returns an error if configuration is not valid.
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
		cfg.ReadHeaderTimeout = integration.DefaultReadHeaderTimeout
	}

	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = integration.DefaultIdleTimeout
	}

	if cfg.MaxRequestBodyBytes == 0 {
		cfg.MaxRequestBodyBytes = DefaultMaxRequestBodyBytes
	}

	durations := []struct {
		value time.Duration
		key   string
	}{
		{cfg.RequestTimeout, "request_timeout"},
		{cfg.ReadHeaderTimeout, "read_header_timeout"},
		{cfg.ReadTimeout, "read_timeout"},
		{cfg.WriteTimeout, "write_timeout"},
		{cfg.IdleTimeout, "idle_timeout"},
	}

	for _, d := range durations {
		if d.value < 0 {
			entries = append(entries, errorstack.Entry{
				Message: "Must be a positive duration",
				Path:    []any{"config", d.key},
			})
		}
	}

	if cfg.MaxRequestBodyBytes < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a positive number of bytes",
			Path:    []any{"config", "max_request_body_bytes"},
		})
	}

	if cfg.MaxHeaderBytes < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a positive number of bytes",
			Path:    []any{"config", "max_header_bytes"},
		})
	}

	if cfg.OpenAPI.Enabled && len(cfg.OpenAPI.Descriptions) == 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "openapi", "descriptions"},
		})
	}

	for i, description := range cfg.OpenAPI.Descriptions {
		if description == "" {
			entries = append(entries, errorstack.Entry{
				Message: "Must not be empty",
				Path:    []any{"config", "openapi", "descriptions", i},
			})
		}
	}

	// The server's own requirements are checked only once the shared ones pass, so
	// that a half pair is reported once, as a half pair, rather than a second time
	// as a missing certificate.
	tlsEntries := cfg.TLS.Sanitize()
	if len(tlsEntries) == 0 {
		_, tlsEntries = cfg.TLS.ToServerTLS()
	}

	entries = append(entries, tlsEntries...)
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
