package mcp

import (
	"context"
	"net/http"
	"time"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

/*
Server is a re-export of the Go MCP SDK's *Server so that consumers only need to
import this package to attach tools, resources, and prompts in Config.Register.
*/
type Server = mcpsdk.Server

/*
Tool is a re-export of the Go MCP SDK's Tool so that consumers only need to
import this package to define tools.
*/
type Tool = mcpsdk.Tool

/*
ToolAnnotations is a re-export of the Go MCP SDK's ToolAnnotations so that
consumers only need to import this package to annotate tools (ReadOnlyHint,
Title, ...).
*/
type ToolAnnotations = mcpsdk.ToolAnnotations

/*
ToolHandlerFor is a re-export of the Go MCP SDK's generic tool handler type. It
binds a tool to a Go function with typed input and output, automatically
populating the tool's input and output JSON schemas.
*/
type ToolHandlerFor[In, Out any] = mcpsdk.ToolHandlerFor[In, Out]

/*
CallToolRequest is a re-export of the Go MCP SDK's CallToolRequest. Its Extra
field exposes the incoming HTTP headers (Extra.Header) and bearer token info
(Extra.TokenInfo) to a tool handler, so a consumer's auth middleware can pass
per-request credentials through to tools.
*/
type CallToolRequest = mcpsdk.CallToolRequest

/*
CallToolResult is a re-export of the Go MCP SDK's CallToolResult.
*/
type CallToolResult = mcpsdk.CallToolResult

/*
AddTool is a re-export of the Go MCP SDK's generic AddTool function. It binds a
tool to a Go function with typed input and output, automatically populating the
tool's input and output JSON schemas and validating inputs.

	mcp.AddTool(server, &mcp.Tool{Name: "greet"}, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
	  ...
	})
*/
func AddTool[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out]) {
	mcpsdk.AddTool(s, t, h)
}

/*
ServerInfo describes the name and version of the MCP server, advertised to
clients during initialization.
*/
type ServerInfo struct {

	// Name is the programmatic name of the MCP server. Required.
	Name string `json:"name"`

	// Version is the version of the MCP server. Required.
	Version string `json:"version"`
}

/*
Config is used to configure the MCP server integration.
*/
type Config struct {

	// Address is the HTTP address to listen on.
	//
	// Default:
	//
	//   ":8080"
	Address string `json:"address"`

	// Path is the URL path where the MCP Streamable HTTP transport is served.
	//
	// Default:
	//
	//   "/mcp"
	Path string `json:"path"`

	// ServerInfo describes the name and version of the MCP server, advertised to
	// clients during initialization. Name and Version are required.
	ServerInfo ServerInfo `json:"server_info"`

	// Register is invoked with the underlying MCP server so that the consumer can
	// attach tools, resources, and prompts before the server starts serving. It
	// is required.
	//
	// In stateless mode a fresh server is built for every incoming request, so
	// Register may be called many times and must not retain per-request state.
	Register func(server *Server) `json:"-"`

	// OAuth configures OAuth 2.0 Resource Server protection of the MCP transport.
	// When disabled (the default), the server is open (no authentication).
	OAuth OAuthResourceServer `json:"oauth"`

	// Readiness allows to define custom logic for the readiness probe endpoint at:
	//
	//   GET /ready
	//
	// It should return 200 if service is ready, or 5xx if an error occurred.
	// Defaults to aggregating the status of all attached dependencies.
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
OAuthResourceServer configures OAuth 2.0 Resource Server protection of the MCP
transport. When enabled, the integration:

  - serves OAuth 2.0 Protected Resource Metadata (RFC 9728) at
    "/.well-known/oauth-protected-resource", advertising ResourceId and the
    AuthorizationServers that issue tokens for it;
  - rejects unauthenticated requests with 401 Unauthorized and a
    WWW-Authenticate header pointing clients at the metadata endpoint;
  - verifies bearer tokens by calling ValidateToken.

No authorization provider specifics live here. The consumer supplies
ValidateToken, which is responsible for verifying the token against whichever
provider issued it and returning the resulting claims (or an error).
*/
type OAuthResourceServer struct {

	// Enabled turns on OAuth 2.0 Resource Server protection. When false (the
	// default), the MCP transport is served without authentication.
	Enabled bool `json:"enabled"`

	// Compose changes how the bearer token enforcement is applied. When false
	// (the default) and Enabled is true, the integration automatically wraps the
	// MCP transport with the bearer token middleware. When true and Enabled is
	// true, the integration still mounts the public Protected Resource Metadata
	// endpoint but does NOT wrap the transport itself; the consumer is then
	// responsible for composing BearerMiddleware inside its own Middleware,
	// alongside any other request-level concerns it needs to run in the same
	// chain. This has no effect when Enabled is false.
	Compose bool `json:"compose"`

	// ResourceId is this resource server's identifier, advertised in the
	// "resource" field of the Protected Resource Metadata. It is typically the
	// canonical URL of the MCP server. Required when Enabled.
	ResourceId string `json:"resource_id"`

	// AuthorizationServers is the list of OAuth authorization server issuer
	// identifiers that issue tokens accepted by this resource. Advertised in the
	// "authorization_servers" field of the Protected Resource Metadata. At least
	// one is required when Enabled.
	AuthorizationServers []string `json:"authorization_servers"`

	// Scopes is the optional list of scopes required to access the resource. When
	// set, they are advertised in the "scopes_supported" field of the metadata
	// and enforced by the bearer token middleware.
	Scopes []string `json:"scopes,omitempty"`

	// ValidateToken verifies a bearer token and returns its claims and expiration.
	// It is called for every request reaching the MCP transport. Returning a non-nil
	// error results in a 401 Unauthorized response. The returned claims are made
	// available to tool handlers via CallToolRequest.Extra.TokenInfo.Extra under the
	// "claims" key. The expiration is the time the token stops being valid; it must
	// be non-zero and in the future, since the MCP SDK rejects a token whose
	// TokenInfo carries a zero or past expiration. Required when Enabled and Compose
	// is false; in compose mode the consumer supplies its own verifier through
	// BearerMiddleware, so this may be left nil.
	ValidateToken func(ctx context.Context, token string) (claims any, expiration time.Time, err error) `json:"-"`
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
		cfg.Path = "/mcp"
	}

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

	// Validate the OAuth Resource Server configuration only when enabled. When
	// disabled, the server is open and no authentication fields are required.
	if cfg.OAuth.Enabled {
		if cfg.OAuth.ResourceId == "" {
			entries = append(entries, errorstack.Entry{
				Message: "Must be set",
				Path:    []any{"config", "oauth", "resource_id"},
			})
		}

		if len(cfg.OAuth.AuthorizationServers) == 0 {
			entries = append(entries, errorstack.Entry{
				Message: "Must contain at least one authorization server",
				Path:    []any{"config", "oauth", "authorization_servers"},
			})
		}

		// ValidateToken is only required when the integration enforces the bearer
		// gate itself. In compose mode the consumer wraps BearerMiddleware in its own
		// Middleware chain with its own verifier, so the integration never calls this
		// ValidateToken and does not require it to be set here.
		if !cfg.OAuth.Compose && cfg.OAuth.ValidateToken == nil {
			entries = append(entries, errorstack.Entry{
				Message: "Must be set",
				Path:    []any{"config", "oauth", "validate_token"},
			})
		}
	}

	entries = append(entries, cfg.TLS.Sanitize()...)
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	return nil
}
