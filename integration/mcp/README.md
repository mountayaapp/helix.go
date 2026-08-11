# helix.go - MCP server integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/mcp)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The MCP server integration provides an opinionated way to build a
[Model Context Protocol](https://modelcontextprotocol.io/) server on top of the
official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk). It serves
arbitrary MCP tools, resources, and prompts over the Streamable HTTP transport
and handles the HTTP server lifecycle, health endpoints, TLS, and OpenTelemetry
tracing. It is a **server** integration — calling `mcp.New(svc, cfg)`
automatically registers the server via `service.Serve()`. Only one server can be
registered per Service.

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/mcp
```

## About the Go MCP SDK

The [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk) is the official
SDK for writing Model Context Protocol clients and servers. This integration
wraps it: you attach tools, resources, and prompts to the SDK server through the
`Register` hook, and the integration owns the transport, server lifecycle, and
observability.

Tools are bound to ordinary Go functions with `mcp.AddTool`. The input and output
types drive the tool's JSON schemas automatically, and inputs are validated
before your handler runs:

```go
type GreetInput struct {
  Name string `json:"name" jsonschema:"the person to greet"`
}

type GreetOutput struct {
  Greeting string `json:"greeting" jsonschema:"the rendered greeting"`
}

mcp.AddTool(server, &mcp.Tool{
  Name:        "greet",
  Description: "Greets a person by name.",
  Annotations: &mcp.ToolAnnotations{Title: "Greet", ReadOnlyHint: true},
}, func(ctx context.Context, req *mcp.CallToolRequest, in GreetInput) (*mcp.CallToolResult, GreetOutput, error) {
  return nil, GreetOutput{Greeting: "Hi " + in.Name}, nil
})
```

Refer to the [Go MCP SDK documentation](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp)
for the full tool, resource, and prompt API.

## Configuration

- `Address` (`string`) — HTTP address to listen on. Default: `":8080"`.
- `Path` (`string`) — URL path for the MCP Streamable HTTP transport. Default:
  `"/mcp"`. May be `"/"` to serve the transport at the host root (the connector URL
  is then just the bare host).
- `ServerInfo` (`ServerInfo`) — `Name` and `Version` advertised to clients during
  initialization. Both **required**.
- `Register` (`func(*Server)`) — invoked with the underlying MCP server so you can
  attach tools, resources, and prompts. **Required**. In stateless mode it is
  called per request, so it must not retain per-request state.
- `OAuth` (`OAuthResourceServer`) — when `Enabled`, protects the transport as an
  OAuth 2.0 Resource Server. The transport is **open by default**. See
  [OAuth 2.0 Resource Server](#oauth-20-resource-server).
- `Readiness` (`func(*http.Request) int`) — custom readiness probe handler for
  `GET /ready`. Should return `200` for ready, `5xx` for error. Default:
  aggregates the status of all attached dependencies.
- `Stateful` (`bool`) — when `false` (the default), the server is stateless: the
  `Mcp-Session-Id` header is not validated and a temporary session is used per
  request, the recommended mode for horizontally scaled HTTP deployments. Set to
  `true` only when a single instance must retain per-session state.
- `Middleware` (`func(http.Handler) http.Handler`) — wraps the built-in HTTP
  handler with a custom middleware chain (e.g. CORS). The `GET /health` and
  `GET /ready` endpoints always bypass it. The server always validates the
  `Origin` header as a defense against DNS-rebinding attacks regardless of this
  setting: browser-issued cross-origin requests are rejected, while same-origin
  and originless (non-browser) requests pass.
- `TLS` (`integration.ConfigTLS`) — TLS settings.

### OAuth 2.0 Resource Server

When `OAuth.Enabled` is `true`, the integration turns the server into a generic
OAuth 2.0 Resource Server. It serves Protected Resource Metadata (RFC 9728) at
`/.well-known/oauth-protected-resource`, rejects unauthenticated requests with
`401 Unauthorized` and a `WWW-Authenticate` header pointing at the metadata, and
verifies bearer tokens through your `ValidateToken` function. No authorization
provider specifics live in the integration — `ValidateToken` is responsible for
verifying the token against whichever provider issued it and returning the
resulting claims, which are exposed to tool handlers via
`CallToolRequest.Extra.TokenInfo`.

- `Enabled` (`bool`) — Enable OAuth 2.0 Resource Server protection. Default:
  `false` (open, no authentication).
- `ResourceId` (`string`) — This resource server's identifier, advertised in the
  Protected Resource Metadata. Typically the canonical URL of the MCP server.
  **Required** when enabled.
- `AuthorizationServers` (`[]string`) — Authorization server issuer URLs that
  issue tokens for this resource. At least one is **required** when enabled.
- `Scopes` (`[]string`) — Optional scopes required to access the resource.
- `Compose` (`bool`) — When `false` (default), the integration auto-wraps the
  transport with the bearer middleware. When `true`, it still mounts the public PRM
  endpoint but does NOT wrap the transport — instead it exposes
  `OAuth.BearerMiddleware()` so the consumer composes it inside its own `Middleware`
  (e.g. to branch between the bearer gate and another credential scheme on a single
  endpoint). No effect when `Enabled` is `false`.
- `ValidateToken` (`func(ctx, token) (claims any, expiration time.Time, err error)`) —
  Verifies a bearer token and returns its claims and expiration, or an error to
  reject it. The expiration MUST be non-zero and in the future (the MCP SDK rejects a
  token whose `TokenInfo` carries a zero or past expiration). The claims are exposed
  to tool handlers via `CallToolRequest.Extra.TokenInfo`. **Required** when enabled
  and `Compose` is `false`; in compose mode the integration never calls it, so it may
  be left nil (the bearer gate the consumer composes carries its own verifier).

## Usage

### Creating a server

```go
import (
  "context"

  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/mcp"
)

type GreetInput struct {
  Name string `json:"name" jsonschema:"the person to greet"`
}

type GreetOutput struct {
  Greeting string `json:"greeting" jsonschema:"the rendered greeting"`
}

func main() {
  svc, err := service.New()
  if err != nil {
    panic(err)
  }

  err = mcp.New(svc, mcp.Config{
    ServerInfo: mcp.ServerInfo{Name: "greeter", Version: "v1.0.0"},
    Register: func(server *mcp.Server) {
      mcp.AddTool(server, &mcp.Tool{
        Name:        "greet",
        Description: "Greets a person by name.",
        Annotations: &mcp.ToolAnnotations{Title: "Greet", ReadOnlyHint: true},
      }, func(ctx context.Context, req *mcp.CallToolRequest, in GreetInput) (*mcp.CallToolResult, GreetOutput, error) {
        return nil, GreetOutput{Greeting: "Hi " + in.Name}, nil
      })
    },
  })
  if err != nil {
    panic(err)
  }

  ctx := context.Background()
  if err := svc.Start(ctx); err != nil {
    panic(err)
  }

  if err := svc.Stop(ctx); err != nil {
    panic(err)
  }
}
```

### With middleware

`Config.Middleware` wraps the handler with a consumer-provided middleware chain
(CORS, credential extraction, ...). It is the seam through which credential
headers can be moved into the request context; tool handlers then read them via
`CallToolRequest.Extra.Header`. The `GET /health` and `GET /ready` endpoints
always bypass it.

```go
mcp.New(svc, mcp.Config{
  ServerInfo: mcp.ServerInfo{Name: "my-server", Version: "v1.0.0"},
  Register:   register,
  Middleware: func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
      // Validate credentials, enrich the request context, then:
      next.ServeHTTP(rw, req)
    })
  },
})
```

### With OAuth 2.0

Set `Config.OAuth` with `Enabled: true` to require bearer tokens on the
transport. `ValidateToken` verifies the token against whichever provider issued
it and returns the resulting claims:

```go
mcp.New(svc, mcp.Config{
  ServerInfo: mcp.ServerInfo{Name: "my-server", Version: "v1.0.0"},
  Register:   register,
  OAuth: mcp.OAuthResourceServer{
    Enabled:              true,
    ResourceId:           "https://api.example.com",
    AuthorizationServers: []string{"https://auth.example.com"},
    ValidateToken: func(ctx context.Context, token string) (any, time.Time, error) {
      // Verify the token; return its claims, its expiration (non-zero, future),
      // or an error to reject it.
      return verify(ctx, token)
    },
  },
})
```

### Compose mode (mixed auth on one endpoint)

With `OAuth.Compose: true`, the integration mounts the public PRM endpoint but
leaves the transport unwrapped, exposing `OAuth.BearerMiddleware()` so you can place
the bearer gate inside your own `Middleware` — for example to accept either a bearer
token or another credential scheme on a single endpoint. The PRM endpoint stays
public even with `Middleware` set (it bypasses it, like the health probes), so
connectors can always discover it.

```go
oauth := mcp.OAuthResourceServer{
  Enabled:              true,
  Compose:              true,
  ResourceId:           "https://api.example.com",
  AuthorizationServers: []string{"https://auth.example.com"},
  ValidateToken:        verify,
}

mcp.New(svc, mcp.Config{
  ServerInfo: mcp.ServerInfo{Name: "my-server", Version: "v1.0.0"},
  Register:   register,
  OAuth:      oauth,
  Middleware: func(next http.Handler) http.Handler {
    bearer := oauth.BearerMiddleware()
    return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
      if hasOtherCredential(req) {
        next.ServeHTTP(rw, req) // your other scheme
        return
      }
      bearer(next).ServeHTTP(rw, req) // OAuth bearer path (401 + WWW-Authenticate)
    })
  },
})
```

## Error responses

HTTP-layer errors raised before the MCP transport is reached (404, 405, …) and
an unavailable readiness probe (`503`) follow the
[GraphQL spec error envelope](https://spec.graphql.org/draft/#sec-Errors), the
same wire shape used by the REST and GraphQL integrations:

```json
{
  "errors": [
    {"message": "…", "extensions": {"code": "…"}}
  ]
}
```

The localized message is selected via the `Accept-Language` header and the
canonical machine code comes from `errorstack.HTTPStatusToCode`. Errors raised
*inside* a tool handler are surfaced through the MCP tool result by the Go MCP
SDK and are not reshaped by this integration.

## Trace attributes

The `mcp` integration sets the following trace attributes:
- `client.address`
- `http.request.body.size`
- `http.request.method`
- `http.response.body.size`
- `http.response.status_code`
- `http.route`
- `network.peer.address`
- `network.peer.port`
- `network.protocol.version`
- `server.address`
- `server.port`
- `url.path`
- `url.scheme`
- `user_agent.original`

Example:
```
client.address: "127.0.0.1"
http.request.body.size: 128
http.request.method: "POST"
http.response.body.size: 256
http.response.status_code: 200
http.route: "/mcp"
network.peer.address: "127.0.0.1"
network.peer.port: 50643
network.protocol.version: "1.1"
server.address: "localhost"
server.port: 8080
url.path: "/mcp"
url.scheme: "http"
user_agent.original: "node"
```

## Health probes

The `mcp` integration exposes two health probe endpoints following Kubernetes
conventions. Both bypass `Config.OAuth` and `Config.Middleware`, so they are
never blocked by authentication or other service-level middleware.

### Liveness — `GET /health`

```sh
$ curl --request GET \
    --url http://localhost:8080/health
```

Returns `200` immediately. No dependency checks are performed. Use this as a
liveness probe to verify the process is running and able to serve traffic.

### Readiness — `GET /ready`

```sh
$ curl --request GET \
    --url http://localhost:8080/ready
```

Aggregates the health status of all dependencies attached to the service,
returning the highest HTTP status code. When all dependencies are healthy,
the response is:

```json
{"data":null}
```

When at least one dependency is temporarily unavailable (`503`), the
response uses the canonical error envelope:

```json
{
  "errors": [
    {
      "message": "Service is temporarily unavailable",
      "extensions": {"code": "SERVICE_UNAVAILABLE"}
    }
  ]
}
```

Pass a custom `Readiness` function in the config to override this behavior.
