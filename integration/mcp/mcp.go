package mcp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/mountayaapp/helix.go/errorstack"
	"github.com/mountayaapp/helix.go/integration"
	"github.com/mountayaapp/helix.go/service"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcp represents the MCP server integration: the MCP server, its routes and the
// handler chain in front of them. Its lifecycle is not here: New hands the HTTP
// server to an integration.HTTPLifecycle, which the Service owns.
type mcp struct {

	// config holds the Config initially passed when creating a new MCP server.
	config *Config

	// inst names this server to the telemetry pipeline. Every span this
	// integration opens - the HTTP entry span and the per-method spans the SDK
	// middleware adds - is opened through it.
	inst *integration.Instrument

	// http instruments the inbound requests. It is built once, at construction,
	// since it registers the duration and size instruments.
	http *integration.HTTPServer

	// probes answer the liveness and readiness endpoints. They report on the
	// Service, so readiness covers every integration attached to it.
	probes integration.HTTPProbes

	// server is the MCP server every request and every session is served by. It
	// is built once: the SDK accepts the same server for any number of sessions,
	// and building one per request would reflect every tool's JSON schemas on
	// every request.
	server *mcpsdk.Server

	// metadataPath is the path the Protected Resource Metadata is served at, in
	// OAuth Resource Server mode. It is empty otherwise.
	metadataPath string

	// mux routes requests to the MCP transport, the health endpoints, and (in
	// OAuth mode) the protected resource metadata endpoint.
	mux *http.ServeMux

	// handler is the full handler chain the HTTP server serves: the entry span,
	// Config.EdgeMiddleware, Config.Middleware, event stamping, then mux.
	handler http.Handler
}

// New tries to build a new MCP server for Config, and registers it with svc
// under Config.Name (ServerInfo.Name when that is empty). The Service owns its
// lifecycle from there: Service.Run starts it, and Service.Stop drains it, ending
// open streams once half of the shutdown budget is spent. Returns an error if
// Config is not valid, or if Config.Register panics.
func New(ctx context.Context, svc *service.Service, cfg Config) error {
	if svc == nil {
		return errorstack.NewValidation(errorstack.Entry{
			Message: "Must be set",
			Path:    []any{"service"},
		})
	}

	// No need to continue if Config is not valid.
	err := cfg.sanitize()
	if err != nil {
		return err
	}

	// The key pair is parsed now so that a malformed one fails New rather than the
	// goroutine the Service runs Start on.
	tlsConfig, entries := cfg.TLS.ToServerTLS()
	if len(entries) > 0 {
		return errorstack.NewValidation(entries...)
	}

	inst, err := integration.NewInstrument(integration.InstrumentConfig{
		Identifier:     identifier,
		Humanized:      humanized,
		Name:           cfg.name(),
		TracerProvider: service.TracerProvider(svc),
		MeterProvider:  service.MeterProvider(svc),
		Propagator:     service.Propagator(svc),
	})
	if err != nil {
		return err
	}

	m, err := newMCP(&cfg, inst, integration.HTTPProbes{
		CheckHealth: svc.CheckHealth,
		Readiness:   cfg.Readiness,
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           m.handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	// Request contexts derive from the Service's, so that handlers log and trace
	// through its providers even when it was built with SkipGlobals.
	lifecycle := integration.NewHTTPLifecycle(service.Context(context.Background(), svc), server)

	return service.Serve(svc, cfg.name(), lifecycle)
}

// newMCP builds everything New serves short of the HTTP server itself: the MCP
// server, the routes, and the handler chain. New and the tests share it so both
// exercise the exact same construction. cfg must already be sanitized.
func newMCP(cfg *Config, inst *integration.Instrument, probes integration.HTTPProbes) (*mcp, error) {
	m := &mcp{
		config: cfg,
		inst:   inst,
		http:   inst.NewHTTPServer(integration.HTTPServerOptions{PublicEndpoint: cfg.PublicEndpoint}),
		probes: probes,
	}

	// sanitize validated ResourceID, which is the only way this fails.
	if cfg.OAuth.Enabled {
		_, m.metadataPath, _ = cfg.OAuth.metadataLocation()
	}

	server, err := m.buildServer()
	if err != nil {
		return nil, err
	}

	m.server = server
	m.buildMux()
	m.handler = m.buildHandler()

	return m, nil
}

// buildServer builds the MCP SDK server from the ServerInfo and lets the
// consumer attach tools, resources, and prompts through Config.Register.
//
// The SDK reports a mistake in what it is given by panicking: AddTool on a tool
// whose schema cannot be inferred, NewServer on inconsistent ServerOptions. Those
// are configuration errors, so they are recovered and returned as one rather than
// taking the process down.
func (m *mcp) buildServer() (server *mcpsdk.Server, err error) {
	defer func() {
		if v := recover(); v != nil {
			cause, ok := v.(error)
			if !ok {
				cause = fmt.Errorf("%v", v)
			}

			server = nil
			err = errorstack.Wrap(cause, "Failed to build MCP server",
				errorstack.WithExtension("reason", integration.NormalizeErrorMessage(cause)),
			)
		}
	}()

	server = mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    m.config.ServerInfo.Name,
		Version: m.config.ServerInfo.Version,
	}, m.config.ServerOptions)

	m.config.Register(server)

	// Answer a method whose handling panicked, on the goroutine the SDK handles it
	// on, rather than letting the panic end the process. Added after Register, it
	// covers any receiving middleware the consumer added.
	server.AddReceivingMiddleware(m.middlewareRecover())

	// Trace the MCP methods themselves, not just the POST that carried them. Every
	// call arrives at the same path, so the HTTP span is the one span that cannot
	// say which tool ran. Added last, it is the outermost receiving middleware, so
	// it also covers any the consumer added, and the recovery's answer.
	server.AddReceivingMiddleware(m.middlewareTelemetry())

	return server, nil
}

// buildMux builds the integration's HTTP serve mux: the health and readiness
// probes, the MCP Streamable HTTP transport (GET + POST + DELETE), the
// method-not-allowed and not-found fallbacks, and, in OAuth Resource Server mode,
// the public Protected Resource Metadata endpoint.
func (m *mcp) buildMux() {
	cfg := m.config
	transport := m.buildTransport()
	path := exactPattern(cfg.Path)

	m.mux = http.NewServeMux()
	m.mux.HandleFunc("GET "+integration.PathLiveness, m.probes.HandleLiveness)
	m.mux.HandleFunc("GET "+integration.PathReadiness, m.probes.HandleReadiness)
	m.mux.Handle("GET "+path, transport)
	m.mux.Handle("POST "+path, transport)
	m.mux.Handle("DELETE "+path, transport)
	m.mux.HandleFunc(path, m.handlerMethodNotAllowed)

	// In OAuth Resource Server mode, always mount the public Protected Resource
	// Metadata endpoint (RFC 9728). It is intentionally never wrapped by the bearer
	// middleware (regardless of Compose) since it is the discovery document clients
	// fetch before authenticating. It is mounted for every method: the SDK's
	// handler answers the CORS preflight a browser-based client sends first, and
	// 405 to anything but GET.
	if cfg.OAuth.Enabled {
		m.mux.Handle(exactPattern(m.metadataPath), cfg.OAuth.metadataHandler())
	}

	m.mux.HandleFunc("/", m.handlerNotFound)
}

// buildTransport builds the MCP Streamable HTTP handler and the protections in
// front of it, outermost first: the Host allow-list, cross-origin protection,
// then — unless the consumer composes it — the bearer token gate.
func (m *mcp) buildTransport() http.Handler {
	getServer := func(*http.Request) *mcpsdk.Server {
		return m.server
	}

	var h http.Handler = mcpsdk.NewStreamableHTTPHandler(getServer, m.transportOptions())

	// When Compose is true, the transport is left unwrapped here and the consumer
	// applies BearerMiddleware inside its own Middleware chain.
	if m.config.OAuth.Enabled && !m.config.OAuth.Compose {
		h = m.config.OAuth.BearerMiddleware()(h)
	}

	// Cross-origin protection rejects a state-changing request a browser sends on
	// behalf of a page from another origin, which is cross-site request forgery
	// against a server the browser can reach. Same-origin and originless
	// (non-browser) requests pass. It is no defense against DNS rebinding: a page
	// whose domain was rebound to this server is same-origin with it by
	// construction. That is what the SDK's Host check on loopback listeners and
	// Config.AllowedHosts are for. Consumers add CORS via Config.Middleware.
	h = http.NewCrossOriginProtection().Handler(h)

	return m.allowHosts(h)
}

// transportOptions returns the options of the Streamable HTTP handler:
// Config.TransportOptions, with the fields this Config owns set from it.
func (m *mcp) transportOptions() *mcpsdk.StreamableHTTPOptions {
	var opts mcpsdk.StreamableHTTPOptions
	if m.config.TransportOptions != nil {
		opts = *m.config.TransportOptions
	}

	opts.Stateless = !m.config.Stateful
	opts.SessionTimeout = m.config.SessionTimeout
	opts.MaxRequestBodyBytes = m.config.MaxRequestBodyBytes
	opts.DisableLocalhostProtection = m.config.DisableLocalhostProtection

	return &opts
}

// allowHosts wraps next so that a request whose Host is not in
// Config.AllowedHosts is answered with 403. It returns next unchanged when the
// list is empty.
func (m *mcp) allowHosts(next http.Handler) http.Handler {
	if len(m.config.AllowedHosts) == 0 {
		return next
	}

	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if !hostAllowed(req.Host, m.config.AllowedHosts) {
			integration.WriteError(rw, req, http.StatusForbidden)
			return
		}

		next.ServeHTTP(rw, req)
	})
}

// hostAllowed reports whether host, a Host header, matches one of allowed. An
// entry with a port must match host exactly; one without matches its host part
// on any port.
func hostAllowed(host string, allowed []string) bool {
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.Trim(name, "[]")

	for _, entry := range allowed {
		if strings.EqualFold(entry, host) || strings.EqualFold(strings.Trim(entry, "[]"), name) {
			return true
		}
	}

	return false
}

// exactPattern returns the http.ServeMux pattern matching path and nothing
// else. A path ending in a slash is otherwise a prefix pattern: "/" would catch
// every request no other route matches.
func exactPattern(path string) string {
	if strings.HasSuffix(path, "/") {
		return path + "{$}"
	}

	return path
}
