package mcp

import (
	"context"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is a re-export of the Go MCP SDK's *Server so that consumers only need to
// import this package to attach tools, resources, and prompts in Config.Register.
type Server = mcpsdk.Server

// Tool is a re-export of the Go MCP SDK's Tool so that consumers only need to
// import this package to define tools.
type Tool = mcpsdk.Tool

// ToolAnnotations is a re-export of the Go MCP SDK's ToolAnnotations so that
// consumers only need to import this package to annotate tools (ReadOnlyHint,
// Title, ...).
type ToolAnnotations = mcpsdk.ToolAnnotations

// ToolHandlerFor is a re-export of the Go MCP SDK's generic tool handler type. It
// binds a tool to a Go function with typed input and output, automatically
// populating the tool's input and output JSON schemas.
type ToolHandlerFor[In, Out any] = mcpsdk.ToolHandlerFor[In, Out]

// CallToolRequest is a re-export of the Go MCP SDK's CallToolRequest. Its Extra
// field exposes the incoming HTTP headers (Extra.Header) and bearer token info
// (Extra.TokenInfo) to a tool handler, so a consumer's auth middleware can pass
// per-request credentials through to tools.
type CallToolRequest = mcpsdk.CallToolRequest

// CallToolResult is a re-export of the Go MCP SDK's CallToolResult.
type CallToolResult = mcpsdk.CallToolResult

// AddTool is a re-export of the Go MCP SDK's generic AddTool function. It binds a
// tool to a Go function with typed input and output, automatically populating the
// tool's input and output JSON schemas and validating inputs.
//
//	mcp.AddTool(server, &mcp.Tool{Name: "greet"}, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
//	  ...
//	})
//
// Go 1.27 generic methods do not apply here. Server is an alias for the SDK's
// Server, and methods cannot be declared on a non-local type, so this stays a
// package-level function taking the server as its first argument.
func AddTool[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out]) {
	mcpsdk.AddTool(s, t, h)
}

// DefaultSessionTimeout is how long a stateful session may sit idle before it is
// closed, when Config.SessionTimeout is left unset.
const DefaultSessionTimeout = 30 * time.Minute

// ServerInfo describes the name and version of the MCP server, advertised to
// clients during initialization.
type ServerInfo struct {

	// Name is the programmatic name of the MCP server. Required.
	Name string `json:"name"`

	// Version is the version of the MCP server. Required.
	Version string `json:"version"`
}

// Config is used to configure the MCP server integration.
type Config struct {

	// Name identifies this server in health output, error context and telemetry
	// (the "mcp.name" attribute). Defaults to ServerInfo.Name when left empty.
	Name string `json:"name"`

	// Address is the HTTP address to listen on. The default listens on every
	// interface, and the transport is open unless OAuth is enabled: set an
	// explicit loopback address ("127.0.0.1:8080") for a server only local clients
	// should reach.
	//
	// Default:
	//
	//   ":8080"
	Address string `json:"address"`

	// Path is the URL path where the MCP Streamable HTTP transport is served. It
	// must be a clean path starting with "/", without braces or whitespace, and
	// must not be one of the health probe paths. "/" serves the transport at the
	// root and nowhere else: other paths still answer 404.
	//
	// Default:
	//
	//   "/mcp"
	Path string `json:"path"`

	// ServerInfo describes the name and version of the MCP server, advertised to
	// clients during initialization. Name and Version are required.
	ServerInfo ServerInfo `json:"server_info"`

	// Register is invoked once, by New, with the underlying MCP server so that the
	// consumer can attach tools, resources, and prompts before the server starts
	// serving. The same server then answers every request and every session. It is
	// required.
	//
	// A panic inside Register — the SDK's AddTool panics on a tool whose schema
	// cannot be inferred — is recovered and returned by New as an error.
	Register func(server *Server) `json:"-"`

	// OAuth configures OAuth 2.0 Resource Server protection of the MCP transport.
	// When disabled (the default), the server is open (no authentication).
	OAuth OAuthResourceServer `json:"oauth"`

	// PublicEndpoint marks the server as reachable from outside the trust boundary
	// of the services it belongs to. Its requests then start a new trace, linked
	// to the caller's, and the caller's baggage is ignored rather than propagated
	// onward. See integration.HTTPServerOptions.
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
	// Defaults to aggregating the status of every integration registered with the
	// Service.
	Readiness func(req *http.Request) int `json:"-"`

	// Stateful controls whether the MCP server keeps server-side session state.
	// When false (the default), the server is stateless: the Mcp-Session-Id
	// header is not read or set and a temporary session is used for each request,
	// which is the recommended mode for horizontally scaled HTTP deployments. Set
	// it to true only when a single instance must retain per-session state.
	//
	// This flag also selects the protocol revision the server speaks. Stateless
	// servers serve revision 2026-07-28, which is POST-only: GET and DELETE on the
	// MCP path are answered with 405 Method Not Allowed, resumability
	// (Last-Event-ID, standalone GET) is gone, and ping, logging/setLevel,
	// resources/subscribe, and resources/unsubscribe are rejected as
	// MethodNotFound. Setting Stateful to true negotiates down to revision
	// 2025-11-25, where those methods and the session lifecycle remain available.
	//
	// Default:
	//
	//   false (stateless)
	Stateful bool `json:"stateful"`

	// SessionTimeout is how long a stateful session may receive no request before
	// the server closes it. Without it, a client that never sends DELETE leaves its
	// session open for the life of the process. It has no effect when Stateful is
	// false.
	//
	// Default:
	//
	//   30m (when Stateful)
	SessionTimeout time.Duration `json:"session_timeout"`

	// MaxRequestBodyBytes caps the size of a request body the MCP transport reads.
	// A larger one is answered with 413 Request Entity Too Large.
	//
	// Default:
	//
	//   4194304 (4 MiB, the Go MCP SDK's default)
	MaxRequestBodyBytes int64 `json:"max_request_body_bytes"`

	// AllowedHosts, when set, restricts the Host header the MCP transport accepts:
	// a request whose Host is not listed is answered with 403 Forbidden. An entry
	// without a port matches that host on any port; one with a port matches only
	// that port. Matching ignores case.
	//
	// This is the DNS-rebinding defense for a server listening on anything other
	// than loopback: a page that rebinds its own domain to this server's address
	// still sends its own domain as Host. The health probes and the protected
	// resource metadata are not restricted.
	AllowedHosts []string `json:"allowed_hosts,omitempty"`

	// DisableLocalhostProtection turns off the Go MCP SDK's DNS-rebinding defense
	// for loopback listeners, which answers 403 to a request that arrived on a
	// loopback address with a Host header that is not a loopback one. Behind a
	// proxy on the same host — a sidecar forwarding to 127.0.0.1 with the public
	// Host — every request looks like that, and this has to be set. Prefer
	// AllowedHosts to keep a Host check in place when it is.
	//
	// Default:
	//
	//   false
	DisableLocalhostProtection bool `json:"disable_localhost_protection"`

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

	// ReadTimeout bounds how long reading an entire request, body included, may
	// take. It is off by default because net/http keeps the deadline armed while
	// the handler runs: when it elapses during a response that streams — a
	// server-sent event stream, a long tool call — the request's context is
	// cancelled and the stream ends. Set it only when no response outlives it.
	//
	// There is deliberately no write timeout, for the same reason.
	//
	// Default:
	//
	//   0 (no limit)
	ReadTimeout time.Duration `json:"read_timeout"`

	// Middleware allows to wrap the built-in HTTP handler with a custom one, for
	// adding a chain of middlewares. It runs inside the request's entry span, so
	// a response it writes itself is traced like any other. The health probes and
	// the protected resource metadata bypass it; EdgeMiddleware does not.
	Middleware func(next http.Handler) http.Handler `json:"-"`

	// EdgeMiddleware wraps every response the server gives: the ones Middleware
	// and the MCP transport write, the health probes' and the protected resource
	// metadata's, which never reach Middleware, and the 404 and 405 this package
	// answers an unknown path or method with.
	//
	// It is the place for what every response must carry whoever writes it: a
	// request identifier, the security headers a server stamps on all of its
	// responses. Since the probes and the metadata cross it, it must not refuse a
	// request for carrying no credentials: authentication belongs in Middleware,
	// which runs inside it.
	//
	// The request's entry span is already open when it runs, so a response it
	// writes itself is traced and measured like any other. An Event it puts in the
	// request context is stamped onto that span once the request reaches the
	// routes.
	EdgeMiddleware func(next http.Handler) http.Handler `json:"-"`

	// ServerOptions is passed to the Go MCP SDK when the server is built, for the
	// options this Config does not cover (Instructions, KeepAlive, Capabilities,
	// handlers, ...). The integration sets none of its fields. An invalid option
	// the SDK panics on is returned by New as an error.
	ServerOptions *mcpsdk.ServerOptions `json:"-"`

	// TransportOptions is passed to the Go MCP SDK's Streamable HTTP handler, for
	// the options this Config does not cover (JSONResponse, EventStore, Logger,
	// ...). The integration owns four of its fields and overwrites them from this
	// Config, whatever TransportOptions sets them to: Stateless (from Stateful),
	// SessionTimeout, MaxRequestBodyBytes and DisableLocalhostProtection.
	TransportOptions *mcpsdk.StreamableHTTPOptions `json:"-"`

	// TLS configures TLS for the HTTP server. CertPEM and KeyPEM are required when
	// it is enabled: PEM-encoded certificate and matching private key for the
	// server. If the certificate is signed by a certificate authority, the CertPEM
	// should be the concatenation of the server's certificate, any intermediates,
	// and the CA's certificate. Setting ClientCAPEMs turns on mutual TLS. The
	// client-only fields are ignored.
	TLS integration.ConfigTLS `json:"tls"`
}

// OAuthResourceServer configures OAuth 2.0 Resource Server protection of the MCP
// transport. When enabled, the integration:
//
//   - serves OAuth 2.0 Protected Resource Metadata (RFC 9728), advertising
//     ResourceID and the AuthorizationServers that issue tokens for it, at the
//     well-known URL RFC 9728 derives from ResourceID: for
//     "https://api.example.com/mcp", that is
//     "https://api.example.com/.well-known/oauth-protected-resource/mcp";
//   - rejects a request without a valid bearer token with 401 Unauthorized and a
//     WWW-Authenticate header pointing clients at the metadata;
//   - rejects a valid token that does not grant every scope in Scopes with 403
//     Forbidden;
//   - answers a token ValidateToken could not verify for the volume of requests
//     (ErrTooManyRequests) with 429 Too Many Requests, and one it could not verify
//     because what verifies it failed (ErrServiceUnavailable) with 503 Service
//     Unavailable;
//   - verifies bearer tokens by calling ValidateToken.
//
// No authorization provider specifics live here. The consumer supplies
// ValidateToken, which is responsible for verifying the token against whichever
// provider issued it and returning what it grants (or an error).
type OAuthResourceServer struct {

	// Enabled turns on OAuth 2.0 Resource Server protection. When false (the
	// default), the MCP transport is served without authentication.
	Enabled bool `json:"enabled"`

	// Compose changes how the bearer token enforcement is applied. When false
	// (the default) and Enabled is true, the integration automatically wraps the
	// MCP transport with the bearer token middleware. When true and Enabled is
	// true, the integration still mounts the public Protected Resource Metadata
	// endpoint but does NOT wrap the transport itself.
	//
	// Compose hands the responsibility for authentication to the consumer: its
	// Middleware must apply BearerMiddleware (or an equivalent gate) on every path
	// to the transport, alongside any other request-level concerns it needs to run
	// in the same chain. A Middleware that forgets to serves an open transport
	// while the metadata advertises a protected one. Config.Middleware is required
	// when Compose is true. This has no effect when Enabled is false.
	Compose bool `json:"compose"`

	// ResourceID is this resource server's identifier, advertised in the
	// "resource" field of the Protected Resource Metadata. It is the canonical
	// URL of the MCP server — an absolute http or https URL with a clean path and
	// no fragment —
	// and its path is where the metadata is served under the well-known prefix,
	// so it must be the path this server receives requests on. Required when
	// Enabled.
	ResourceID string `json:"resource_id"`

	// AuthorizationServers is the list of OAuth authorization server issuer
	// identifiers that issue tokens accepted by this resource. Advertised in the
	// "authorization_servers" field of the Protected Resource Metadata. At least
	// one is required when Enabled.
	AuthorizationServers []string `json:"authorization_servers"`

	// Scopes is the optional list of scopes required to access the resource. When
	// set, they are advertised in the "scopes_supported" field of the metadata and
	// in the WWW-Authenticate challenge, and enforced by the bearer token
	// middleware: a token whose TokenInfo.Scopes lacks any of them is answered
	// with 403 Forbidden.
	Scopes []string `json:"scopes,omitempty"`

	// ValidateToken verifies a bearer token and returns what it grants. It is
	// called for every request reaching the MCP transport.
	//
	// An error rejects the request with 401 Unauthorized and a WWW-Authenticate
	// challenge. The error itself never reaches the client: it is recorded on the
	// request's span, and an error that does not wrap auth.ErrInvalidToken (an
	// unreachable key set, an identity provider outage) is logged as well. There
	// are three exceptions. An error wrapping auth.ErrOAuth (both in
	// github.com/modelcontextprotocol/go-sdk/auth) is answered with 400 Bad
	// Request and the error's own message, for a consumer reporting an OAuth
	// protocol error on purpose. An error wrapping ErrTooManyRequests, and not
	// auth.ErrInvalidToken, is answered with 429 Too Many Requests and a generic
	// body, for a token that could not be verified because what verifies it is
	// limiting how often it may be asked; a *TooManyRequestsError also sets the
	// Retry-After header. An error wrapping ErrServiceUnavailable, and neither
	// auth.ErrInvalidToken nor ErrTooManyRequests, is answered with 503 Service
	// Unavailable and a generic body, for a token that could not be verified
	// because what verifies it failed or could not be reached; a
	// *ServiceUnavailableError also sets the Retry-After header. Wrap it only
	// around a failure a retry can outlive: a 401 tells the client its token is
	// bad, a 503 that it should wait and send the same token again.
	//
	// Required when Enabled and Compose is false. In compose mode the integration
	// never calls it itself: BearerMiddleware does, so set it on the
	// OAuthResourceServer whose BearerMiddleware the consumer's Middleware applies.
	ValidateToken func(ctx context.Context, token string) (*TokenInfo, error) `json:"-"`
}

// TokenInfo is what ValidateToken learned from a bearer token it accepted. Tool
// handlers read it back through CallToolRequest.Extra.TokenInfo: Subject as
// UserID, Scopes and Expiration as themselves, and Claims as Extra["claims"].
type TokenInfo struct {

	// Subject identifies who the token was issued to: its "sub" claim, or the
	// client identifier of a token a client obtained on its own behalf. Required:
	// a token without one is rejected with 401. A stateful server binds each
	// session to the subject that created it, so that a different token cannot
	// take over a session whose identifier it learned.
	Subject string

	// Scopes are the scopes the token grants — not the ones the resource
	// requires. Every scope in OAuthResourceServer.Scopes must be among them.
	Scopes []string

	// Expiration is when the token stops being valid. It must be non-zero and in
	// the future: the Go MCP SDK rejects a token whose expiration is zero or past
	// with 401.
	Expiration time.Time

	// Claims carries whatever else the consumer wants tool handlers to see, such as
	// the token's decoded claims. It is optional.
	Claims any
}

// sanitize sets default values - when applicable - and validates the configuration.
// Returns an error if configuration is not valid.
func (cfg *Config) sanitize() error {
	var entries []errorstack.Entry

	if cfg.Address == "" {
		cfg.Address = ":8080"
	}

	if cfg.Path == "" {
		cfg.Path = "/mcp"
	}

	// The connection-level timeouts default to a value rather than to "off": left
	// unset, a client that never finishes sending its headers holds a goroutine for
	// as long as it likes, and keep-alive connections are never reclaimed.
	// ReadTimeout is deliberately not defaulted; see its documentation.
	if cfg.ReadHeaderTimeout == 0 {
		cfg.ReadHeaderTimeout = integration.DefaultReadHeaderTimeout
	}

	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = integration.DefaultIdleTimeout
	}

	if cfg.Stateful && cfg.SessionTimeout == 0 {
		cfg.SessionTimeout = DefaultSessionTimeout
	}

	if cfg.MaxRequestBodyBytes == 0 {
		cfg.MaxRequestBodyBytes = mcpsdk.DefaultMaxRequestBodyBytes
	}

	for _, timeout := range []struct {
		field string
		value time.Duration
	}{
		{"read_header_timeout", cfg.ReadHeaderTimeout},
		{"idle_timeout", cfg.IdleTimeout},
		{"read_timeout", cfg.ReadTimeout},
		{"session_timeout", cfg.SessionTimeout},
	} {
		if timeout.value < 0 {
			entries = append(entries, errorstack.Entry{
				Message: "Must be a positive duration",
				Path:    []any{"config", timeout.field},
			})
		}
	}

	if cfg.MaxRequestBodyBytes < 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must be a positive number",
			Path:    []any{"config", "max_request_body_bytes"},
		})
	}

	entries = append(entries, cfg.validatePath()...)

	if cfg.ServerInfo.Name == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "server_info", "name"},
		})
	}

	if cfg.ServerInfo.Version == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "server_info", "version"},
		})
	}

	if cfg.Register == nil {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "register"},
		})
	}

	for i, host := range cfg.AllowedHosts {
		if strings.TrimSpace(host) == "" {
			entries = append(entries, errorstack.Entry{
				Message: "Must not be empty",
				Path:    []any{"config", "allowed_hosts", i},
			})
		}
	}

	// Validate the OAuth Resource Server configuration only when enabled. When
	// disabled, the server is open and no authentication fields are required.
	if cfg.OAuth.Enabled {
		entries = append(entries, cfg.validateOAuth()...)
	}

	entries = append(entries, cfg.TLS.Sanitize()...)
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}

// validatePath validates Path. The rules are what keeps the routes from
// overlapping: a Path equal to a probe path would also bypass Config.Middleware,
// since the probes do, and one carrying ServeMux pattern syntax would either
// panic at construction or route more than the transport's own path to it.
func (cfg *Config) validatePath() []errorstack.Entry {
	path := []any{"config", "path"}

	switch {
	case !strings.HasPrefix(cfg.Path, "/"):
		return []errorstack.Entry{{Message: `Must start with "/"`, Path: path}}
	case !isLiteralPath(cfg.Path):
		return []errorstack.Entry{{Message: "Must be a clean path without braces or whitespace", Path: path}}
	case cfg.Path == integration.PathLiveness || cfg.Path == integration.PathReadiness:
		return []errorstack.Entry{{Message: "Must not be a health probe path", Path: path}}
	}

	return nil
}

// validateOAuth validates the OAuth Resource Server configuration.
func (cfg *Config) validateOAuth() []errorstack.Entry {
	var entries []errorstack.Entry

	if cfg.OAuth.ResourceID == "" {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "oauth", "resource_id"},
		})
	} else if _, path, err := cfg.OAuth.metadataLocation(); err != nil {
		entries = append(entries, errorstack.Entry{
			Message: "Must be an absolute http or https URL with a clean path and no fragment",
			Path:    []any{"config", "oauth", "resource_id"},
		})
	} else if path == cfg.Path {
		entries = append(entries, errorstack.Entry{
			Message: "Must not be the protected resource metadata path",
			Path:    []any{"config", "path"},
		})
	}

	if len(cfg.OAuth.AuthorizationServers) == 0 {
		entries = append(entries, errorstack.Entry{
			Message: "Must contain at least one authorization server",
			Path:    []any{"config", "oauth", "authorization_servers"},
		})
	}

	// ValidateToken is only required when the integration enforces the bearer gate
	// itself. In compose mode the consumer's Middleware applies BearerMiddleware,
	// which is what calls it, so the integration does not require it here.
	if !cfg.OAuth.Compose && cfg.OAuth.ValidateToken == nil {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"config", "oauth", "validate_token"},
		})
	}

	// In compose mode the transport is left unwrapped for the consumer's
	// Middleware to gate. Without one, nothing gates it: the server would answer
	// every request while its metadata tells clients they need a token.
	if cfg.OAuth.Compose && cfg.Middleware == nil {
		entries = append(entries, errorstack.Entry{
			Message: "Must be set when OAuth compose mode is enabled",
			Path:    []any{"config", "middleware"},
		})
	}

	return entries
}

// name returns the name the server registers under. Config.Name wins; the MCP
// server's programmatic name is the fallback, and the integration's identifier
// the last resort.
func (cfg *Config) name() string {
	switch {
	case cfg.Name != "":
		return cfg.Name
	case cfg.ServerInfo.Name != "":
		return cfg.ServerInfo.Name
	default:
		return identifier
	}
}

// patternChars are the characters http.ServeMux reads as pattern syntax: braces
// open a wildcard, and whitespace separates the method from the path.
const patternChars = "{} \t\r\n"

// isLiteralPath reports whether p is a path http.ServeMux takes literally as a
// pattern: clean, as the mux requires of every pattern it registers, and free of
// pattern syntax. Any other path is refused rather than turned into a pattern that
// routes more than the path, or one the mux panics on.
func isLiteralPath(p string) bool {
	if strings.ContainsAny(p, patternChars) {
		return false
	}

	// path.Clean drops a trailing slash, which the mux keeps.
	clean := path.Clean(p)
	if strings.HasSuffix(p, "/") && clean != "/" {
		clean += "/"
	}

	return clean == p
}
