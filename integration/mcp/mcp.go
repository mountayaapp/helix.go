package mcp

import (
	"net/http"

	"github.com/mountayaapp/helix.go/service"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

/*
mcp represents the MCP server integration. It respects the integration.Server
interface.
*/
type mcp struct {

	// svc is the Service this integration belongs to.
	svc *service.Service

	// config holds the Config initially passed when creating a new MCP server.
	config *Config

	// mux is the HTTP serve mux used to route requests to the MCP transport, the
	// health endpoints, and (in OAuth mode) the protected resource metadata
	// endpoint.
	mux *http.ServeMux

	// server is the standard http.Server used to serve HTTP requests.
	server *http.Server
}

/*
New tries to build a new MCP server for Config. Returns an error if Config is not
valid. It is a server integration — on success the server is registered with the
Service via service.Serve and started during the Service lifecycle. Only one
server can be registered per Service.
*/
func New(svc *service.Service, cfg Config) error {

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return err
	}

	m := &mcp{
		svc:    svc,
		config: &cfg,
	}

	// Build the integration's serve mux (transport, health probes, OAuth metadata,
	// and fallbacks). Shared with the tests so both exercise the same routing.
	m.buildMux()

	// Try to register the server integration to the service.
	if err := service.Serve(svc, m); err != nil {
		return err
	}

	return nil
}

/*
buildMux builds the integration's HTTP serve mux: the health and readiness
probes, the MCP Streamable HTTP transport (GET + POST + DELETE), the
method-not-allowed and not-found fallbacks, and, in OAuth Resource Server mode,
the public Protected Resource Metadata endpoint. New and the tests share it so
both exercise the exact same routing.
*/
func (m *mcp) buildMux() {
	cfg := m.config

	// Build the MCP Streamable HTTP transport handler, then wrap it with the OAuth
	// bearer token middleware when OAuth Resource Server protection is enabled and
	// the consumer has not opted into composing it itself. When Compose is true,
	// the transport is left unwrapped here and the consumer applies BearerMiddleware
	// inside its own Middleware chain.
	transport := m.buildTransport()
	if cfg.OAuth.Enabled && !cfg.OAuth.Compose {
		transport = cfg.OAuth.BearerMiddleware()(transport)
	}

	m.mux = http.NewServeMux()
	m.mux.HandleFunc("GET /health", m.handlerLiveness)
	m.mux.HandleFunc("GET /ready", m.handlerReadiness)
	m.mux.Handle("GET "+cfg.Path, transport)
	m.mux.Handle("POST "+cfg.Path, transport)
	m.mux.Handle("DELETE "+cfg.Path, transport)

	// Register the method-not-allowed fallback for the MCP path. When the path is
	// the root ("/"), this is skipped: the method-typed handlers above still
	// outrank the bare "/" pattern, and registering "/" here as well would collide
	// with the catch-all not-found handler below, which makes http.ServeMux panic
	// on the duplicate pattern at startup.
	if cfg.Path != "/" {
		m.mux.HandleFunc(cfg.Path, m.handlerMethodNotAllowed)
	}

	// In OAuth Resource Server mode, always mount the public Protected Resource
	// Metadata endpoint (RFC 9728). It is intentionally never wrapped by the bearer
	// middleware (regardless of Compose) since it is the discovery document clients
	// fetch before authenticating.
	if cfg.OAuth.Enabled {
		path, handler := cfg.OAuth.metadataHandler()
		m.mux.Handle("GET "+path, handler)
	}

	m.mux.HandleFunc("/", m.handlerNotFound)
}

/*
buildServer builds a fresh MCP SDK server from the ServerInfo and lets the
consumer attach tools, resources, and prompts through Config.Register. In
stateless mode a new server is built per request, so this is called for every
incoming request.
*/
func (m *mcp) buildServer() *mcpsdk.Server {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    m.config.ServerInfo.Name,
		Version: m.config.ServerInfo.Version,
	}, nil)

	m.config.Register(server)
	return server
}

/*
buildTransport builds the MCP Streamable HTTP handler. It defaults to stateless
operation and always validates the Origin header as a defense against
DNS-rebinding attacks.
*/
func (m *mcp) buildTransport() http.Handler {
	getServer := func(_ *http.Request) *mcpsdk.Server {
		return m.buildServer()
	}

	var h http.Handler = mcpsdk.NewStreamableHTTPHandler(getServer, &mcpsdk.StreamableHTTPOptions{
		Stateless: !m.config.Stateful,
	})

	// Always validate the Origin header as a defense against DNS-rebinding, per the
	// MCP Streamable HTTP guidance. http.CrossOriginProtection rejects browser-issued
	// cross-origin requests while letting through same-origin and originless
	// (non-browser) requests. Consumers add CORS or other cross-cutting concerns
	// via Config.Middleware.
	return http.NewCrossOriginProtection().Handler(h)
}
