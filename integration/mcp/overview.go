/*
Package mcp exposes an opinionated Model Context Protocol (MCP) server built on
top of the official Go MCP SDK (github.com/modelcontextprotocol/go-sdk). It
serves arbitrary MCP tools, resources, and prompts over the Streamable HTTP
transport. It handles the HTTP server lifecycle, health endpoints, TLS, and
integrates with OpenTelemetry for distributed tracing. It is a server
integration — calling mcp.New(svc, cfg) automatically registers the server via
service.Serve(). Only one server can be registered per Service.

Consumers attach their tools, resources, and prompts through the Register hook
in Config, which receives the underlying SDK *Server. The server is open by
default; setting Config.OAuth turns it into a generic OAuth 2.0 Resource Server
(see OAuthResourceServer) that serves protected resource metadata (RFC 9728) and
verifies bearer tokens. No authorization provider specifics live in this package
— the consumer supplies a ValidateToken function. Custom middleware chains (CORS,
credential extraction, ...) are wired through Config.Middleware.

Example:

	svc, _ := service.New()

	mcp.New(svc, mcp.Config{
	  ServerInfo: mcp.ServerInfo{Name: "my-server", Version: "v1.0.0"},
	  Register: func(server *mcp.Server) {
	    mcp.AddTool(server, &mcp.Tool{Name: "greet"}, greetHandler)
	  },
	})
*/
package mcp

/*
identifier represents the integration's unique identifier.
*/
const identifier = "mcp"

/*
humanized represents the integration's humanized name.
*/
const humanized = "MCP"
