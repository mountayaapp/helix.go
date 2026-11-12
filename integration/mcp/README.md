# helix.go - MCP server integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/mcp)
[![Latest version](https://img.shields.io/github/v/tag/mountayaapp/helix.go?filter=v*&sort=semver&label=version)](https://github.com/mountayaapp/helix.go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The MCP server integration provides an opinionated way to build a
[Model Context Protocol](https://modelcontextprotocol.io/) server on top of the
official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk). It serves
arbitrary MCP tools, resources, and prompts over the Streamable HTTP transport
and handles the HTTP server lifecycle, health endpoints, TLS, OAuth 2.0 bearer
tokens, and OpenTelemetry tracing and metrics. It is a **server** integration:
`mcp.New(ctx, svc, cfg)` registers the server with the `Service`, which starts it
in `svc.Run(ctx)` and drains it on shutdown. A `Service` may serve several
servers, each under its own `Name`.

> [!IMPORTANT]
> The server is **open by default** and listens on `:8080`, every interface. Set
> `OAuth` to require bearer tokens, and an explicit loopback `Address` for a
> server only local clients should reach. See [Security](#security).

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

`Register` is called **once**, by `New`: the same server then answers every
request and every session, so tool schemas are reflected once rather than per
request. A panic inside `Register` (the SDK's `AddTool` panics on a tool whose
schema cannot be inferred) is returned by `New` as an error.

Refer to the [Go MCP SDK documentation](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp)
for the full tool, resource, and prompt API.

## Usage

### Creating a server

```go
import (
  "context"
  "log/slog"
  "os"
  "os/signal"
  "syscall"

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
  if err := run(); err != nil {
    slog.Error("fatal", "err", err)
    os.Exit(1)
  }
}

func run() error {
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

  svc, err := service.New(ctx, service.Config{})
  if err != nil {
    return err
  }

  err = mcp.New(ctx, svc, mcp.Config{
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
    return err
  }

  // Serves until ctx is cancelled, then drains the server.
  return svc.Run(ctx)
}
```

### With middleware

`Config.Middleware` wraps the handler with a consumer-provided middleware chain
(CORS, credential extraction, rate limiting, ...). It runs inside the request's
entry span, so a response it writes itself is traced like any other. It is the
seam through which credential headers can be moved into the request context;
tool handlers then read them via `CallToolRequest.Extra.Header`. `GET /health`,
`GET /ready`, and the protected resource metadata always bypass it.

```go
err = mcp.New(ctx, svc, mcp.Config{
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

A browser-based client's CORS preflight (`OPTIONS` on the MCP path) has to be
answered by this middleware: the integration answers `OPTIONS` on the MCP path
with `405`.

### With edge middleware

`Config.EdgeMiddleware` wraps every response the server gives: the ones
`Middleware` and the transport write, the health probes' and the protected
resource metadata's, which never reach `Middleware`, and the `404` and `405` of an
unknown path or method. It is the place for what every response must carry
whoever writes it, such as a request identifier or security headers:

```go
err = mcp.New(ctx, svc, mcp.Config{
  ServerInfo: mcp.ServerInfo{Name: "my-server", Version: "v1.0.0"},
  Register:   register,
  EdgeMiddleware: func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
      rw.Header().Set("X-Content-Type-Options", "nosniff")
      next.ServeHTTP(rw, req)
    })
  },
})
```

It runs inside the request's entry span, outside `Middleware`. Since the probes
and the metadata cross it, it must not refuse a request for carrying no
credentials: authentication belongs in `Middleware`.

### With OAuth 2.0

Set `Config.OAuth` with `Enabled: true` to require bearer tokens on the
transport. `ValidateToken` verifies the token against whichever provider issued
it and returns what it grants:

```go
err = mcp.New(ctx, svc, mcp.Config{
  ServerInfo: mcp.ServerInfo{Name: "my-server", Version: "v1.0.0"},
  Register:   register,
  OAuth: mcp.OAuthResourceServer{
    Enabled:              true,
    ResourceID:           "https://api.example.com/mcp",
    AuthorizationServers: []string{"https://auth.example.com"},
    Scopes:               []string{"mcp:tools"},
    ValidateToken: func(ctx context.Context, token string) (*mcp.TokenInfo, error) {
      claims, err := verify(ctx, token) // your provider's verification
      if err != nil {
        return nil, err // answered 401; the error itself never reaches the client
      }

      return &mcp.TokenInfo{
        Subject:    claims.Subject,   // required
        Scopes:     claims.Scopes,    // the scopes the token GRANTS
        Expiration: claims.ExpiresAt, // required: non-zero, in the future
        Claims:     claims,           // optional, for tool handlers
      }, nil
    },
  },
})
```

Tool handlers read the accepted token through `CallToolRequest.Extra.TokenInfo`:
`UserID` is the `Subject`, `Scopes` and `Expiration` are as returned, and
`Extra["claims"]` holds `Claims`.

When the token cannot be verified because what verifies it is limiting how often
it may be asked (a provider answering 429, a quota of your own), say so rather
than calling the token invalid: a `401` sends the client to authenticate again,
which asks the same limit once more. Return a `*mcp.TooManyRequestsError`, or any
error wrapping `mcp.ErrTooManyRequests`, and the request is answered `429` with a
generic body, and a `Retry-After` header when the error names a wait:

```go
if errors.Is(err, errProviderLimited) {
  return nil, &mcp.TooManyRequestsError{RetryAfter: wait, Err: err} // answered 429 + Retry-After
}
```

When the token cannot be verified because what verifies it failed (an identity
provider that is down, a key set that cannot be fetched, a backend answering with a
failure of its own), say so as well: a `401` sends the client to authenticate
again, which meets the same failure. Return a `*mcp.ServiceUnavailableError`, or
any error wrapping `mcp.ErrServiceUnavailable`, and the request is answered `503`
with a generic body, and a `Retry-After` header when the error names a wait. The
error is logged, as any other failure of the validator is:

```go
if errors.Is(err, errProviderDown) {
  return nil, &mcp.ServiceUnavailableError{RetryAfter: 5 * time.Second, Err: err} // answered 503 + Retry-After
}
```

An error that also wraps `auth.ErrInvalidToken` is a verdict on the token, and is
still answered `401`. One wrapping both `mcp.ErrTooManyRequests` and
`mcp.ErrServiceUnavailable` is answered `429`.

### Compose mode (mixed auth on one endpoint)

With `OAuth.Compose: true`, the integration mounts the public metadata endpoint
but leaves the transport unwrapped, so you can place `OAuth.BearerMiddleware()`
inside your own `Middleware` — for example to accept either a bearer token or
another credential scheme on a single endpoint. Compose mode makes the
`Middleware` responsible for authentication: `Middleware` is required, and every
path it lets through to the transport must be authenticated, or the server is
open while its metadata advertises it as protected.

```go
oauth := mcp.OAuthResourceServer{
  Enabled:              true,
  Compose:              true,
  ResourceID:           "https://api.example.com/mcp",
  AuthorizationServers: []string{"https://auth.example.com"},
  ValidateToken:        validateToken, // called by BearerMiddleware
}

err = mcp.New(ctx, svc, mcp.Config{
  ServerInfo: mcp.ServerInfo{Name: "my-server", Version: "v1.0.0"},
  Register:   register,
  OAuth:      oauth,
  Middleware: func(next http.Handler) http.Handler {
    bearer := oauth.BearerMiddleware()(next)
    return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
      if hasOtherCredential(req) {
        next.ServeHTTP(rw, req) // your other scheme, verified
        return
      }
      bearer.ServeHTTP(rw, req) // OAuth bearer path (401 + WWW-Authenticate)
    })
  },
})
```

## Configuration

- `Name` (`string`) — Identifies the server in health output, error context and
  telemetry (`mcp.name`). Default: `ServerInfo.Name`.
- `Address` (`string`) — HTTP address to listen on. Default: `":8080"` (every
  interface).
- `Path` (`string`) — URL path for the MCP Streamable HTTP transport. Default:
  `"/mcp"`. Must be a clean path starting with `/` (no empty, `.` or `..`
  segments), without braces or whitespace, and must not be `/health` or `/ready`. May be `"/"` to serve the transport at the host
  root and nowhere else (other paths still answer `404`).
- `ServerInfo` (`ServerInfo`) — `Name` and `Version` advertised to clients during
  initialization. Both **required**.
- `Register` (`func(*Server)`) — Invoked once with the underlying MCP server so you
  can attach tools, resources, and prompts. **Required**.
- `OAuth` (`OAuthResourceServer`) — When `Enabled`, protects the transport as an
  OAuth 2.0 Resource Server. See [OAuth 2.0 Resource Server](#oauth-20-resource-server).
- `PublicEndpoint` (`bool`) — Marks the server as reachable from outside your
  trust boundary: requests start a new trace (linked to the caller's) and inbound
  baggage is ignored. Default: `false`.
- `Readiness` (`func(*http.Request) int`) — Custom readiness probe handler for
  `GET /ready`. Should return `200` for ready, `5xx` for error. Default: the
  health of every integration registered with the `Service`.
- `Stateful` (`bool`) — Keep server-side session state. Default: `false`
  (stateless). See [Stateless and stateful](#stateless-and-stateful).
- `SessionTimeout` (`time.Duration`) — How long a stateful session may receive no
  request before it is closed. Default: `30m` when `Stateful`; no effect
  otherwise.
- `MaxRequestBodyBytes` (`int64`) — Largest request body the transport reads;
  larger bodies get `413`. Default: `4194304` (4 MiB, the SDK's default).
- `AllowedHosts` (`[]string`) — When set, the transport answers `403` to a request
  whose `Host` header is not listed. An entry without a port matches any port.
  Default: unset (no restriction beyond the SDK's loopback check).
- `DisableLocalhostProtection` (`bool`) — Turns off the SDK's DNS-rebinding check
  for loopback listeners. Needed behind a proxy on the same host. Default:
  `false`.
- `ReadHeaderTimeout` (`time.Duration`) — Bound on reading request headers.
  Default: `10s`.
- `IdleTimeout` (`time.Duration`) — Bound on an idle keep-alive connection.
  Default: `120s`.
- `ReadTimeout` (`time.Duration`) — Bound on reading a whole request. Default: `0`
  (no limit). Opt-in only: net/http keeps the deadline armed while the handler
  runs, so it also ends any response stream that outlives it. There is no write
  timeout for the same reason.
- `Middleware` (`func(http.Handler) http.Handler`) — Wraps the built-in handler.
  The probes and the protected resource metadata bypass it.
- `EdgeMiddleware` (`func(http.Handler) http.Handler`) — Wraps every response,
  the probes' and the protected resource metadata's included, outside
  `Middleware`. See [With edge middleware](#with-edge-middleware).
- `ServerOptions` (`*mcp.ServerOptions` from the SDK) — Passed to the SDK server as
  is, for the options this `Config` does not cover (`Instructions`, `KeepAlive`,
  `Capabilities`, handlers, ...). Options the SDK rejects by panicking make `New`
  return an error.
- `TransportOptions` (`*mcp.StreamableHTTPOptions` from the SDK) — Passed to the
  SDK's Streamable HTTP handler (`JSONResponse`, `EventStore`, `Logger`, ...).
  **The `Config` wins** for the four fields it owns, which are overwritten
  whatever `TransportOptions` sets: `Stateless` (from `Stateful`),
  `SessionTimeout`, `MaxRequestBodyBytes`, and `DisableLocalhostProtection`.
- `TLS` (`integration.ConfigTLS`) — TLS settings. `CertPEM` and `KeyPEM` are
  required when enabled; `ClientCAPEMs` turns on mutual TLS. TLS 1.2 minimum.

### OAuth 2.0 Resource Server

When `OAuth.Enabled` is `true`, the integration turns the server into a generic
OAuth 2.0 Resource Server. No authorization provider specifics live in the
integration: `ValidateToken` verifies the token against whichever provider issued
it.

- `Enabled` (`bool`) — Enable OAuth 2.0 Resource Server protection. Default:
  `false` (open, no authentication).
- `ResourceID` (`string`) — This resource server's identifier, advertised in the
  Protected Resource Metadata: the canonical URL of the MCP server, an absolute
  `http` or `https` URL with a clean path and no fragment. **Required** when
  enabled.
- `AuthorizationServers` (`[]string`) — Authorization server issuer URLs that
  issue tokens for this resource. At least one is **required** when enabled.
- `Scopes` (`[]string`) — Scopes a token must grant. Advertised in the metadata
  and the `WWW-Authenticate` challenge, and enforced: a token lacking one of them
  is answered `403`.
- `Compose` (`bool`) — When `true`, the transport is not wrapped and your
  `Middleware` (then required) applies `OAuth.BearerMiddleware()`. Default:
  `false`.
- `ValidateToken` (`func(ctx, token string) (*TokenInfo, error)`) — Verifies a
  bearer token and returns what it grants. **Required** when enabled and `Compose`
  is `false`; in compose mode it is called by `BearerMiddleware`.

`TokenInfo` is what `ValidateToken` returns for an accepted token:

- `Subject` (`string`) — Who the token was issued to (its `sub`, or the client ID
  of a client-credentials token). **Required**: a token without one is answered
  `401`. A stateful session is bound to the subject that created it.
- `Scopes` (`[]string`) — The scopes the token **grants**. Every scope in
  `OAuth.Scopes` must be among them.
- `Expiration` (`time.Time`) — When the token stops being valid. **Required**:
  non-zero and in the future, or the SDK answers `401`.
- `Claims` (`any`) — Anything else for tool handlers, under
  `CallToolRequest.Extra.TokenInfo.Extra["claims"]`.

The Protected Resource Metadata (RFC 9728) is served at the well-known URL derived
from `ResourceID` by inserting `/.well-known/oauth-protected-resource` between the
host and the path:

| `ResourceID`                  | Metadata URL                                                     |
|-------------------------------|------------------------------------------------------------------|
| `https://api.example.com`     | `https://api.example.com/.well-known/oauth-protected-resource`     |
| `https://api.example.com/mcp` | `https://api.example.com/.well-known/oauth-protected-resource/mcp` |

The server serves it at that path, so `ResourceID`'s path must be the path this
server receives requests on (mind a proxy that rewrites paths). It answers `GET`
and the `OPTIONS` CORS preflight, needs no credentials, and bypasses
`Middleware`.

## Stateless and stateful

`Stateful` also selects the protocol revision the server speaks.

- **Stateless** (default) — Revision `2026-07-28`. The `Mcp-Session-Id` header is
  neither read nor set, and every request gets a temporary session: the mode for
  horizontally scaled deployments. The transport is POST-only: `GET` and `DELETE`
  on the MCP path are answered `405` with `Allow: POST`. Resumability
  (`Last-Event-ID`, standalone `GET` stream) does not exist, and `ping`,
  `logging/setLevel`, `resources/subscribe`, and `resources/unsubscribe` are
  rejected as `MethodNotFound`.
- **Stateful** — Negotiates down to revision `2025-11-25`, where the session
  lifecycle (`GET` stream, `DELETE`) and those methods remain available. Sessions
  idle for `SessionTimeout` are closed. With OAuth, a session only answers the
  `Subject` that created it (`403` otherwise).

On shutdown, open streams (a stateful session's `GET` stream, a long tool call)
get half of the shutdown budget to finish, then their request contexts are
cancelled.

## Security

- **Authentication.** Off by default. With `OAuth` enabled, a request without a
  valid token is answered `401` with a `WWW-Authenticate` challenge pointing at the
  metadata, and a valid token lacking a required scope `403`. A `ValidateToken`
  error never reaches the client: it is recorded on the request's span, and logged
  when it does not wrap `auth.ErrInvalidToken` (an identity provider outage rather
  than a bad token). Three exceptions: an error wrapping `auth.ErrOAuth` (both
  from `github.com/modelcontextprotocol/go-sdk/auth`), answered `400` with its
  message, one wrapping `mcp.ErrTooManyRequests`, answered `429` with a
  generic body and the `Retry-After` a `*mcp.TooManyRequestsError` names, and one
  wrapping `mcp.ErrServiceUnavailable`, answered `503` with a generic body and the
  `Retry-After` a `*mcp.ServiceUnavailableError` names.
- **DNS rebinding.** A page that rebinds its own domain to this server's address
  is same-origin with it, so origin checks cannot stop it; the `Host` header can.
  The SDK refuses (`403`) a request that arrived on a loopback address with a
  non-loopback `Host`. That covers loopback listeners only: a server reachable on
  any other address should set `AllowedHosts`. Behind a proxy on the same host
  (a sidecar forwarding to `127.0.0.1` with the public `Host`), set
  `DisableLocalhostProtection` and, ideally, `AllowedHosts`.
- **Cross-site requests.** Always on: a state-changing request a browser sends
  for a page of another origin is refused (`403`, `http.CrossOriginProtection`).
  Same-origin and non-browser requests pass.
- **Trust boundary.** Set `PublicEndpoint` on a server reachable from the
  internet, so that a client cannot continue your traces, decide their sampling,
  or inject baggage.
- **Resource limits.** Request bodies are capped by `MaxRequestBodyBytes`; slow
  clients by `ReadHeaderTimeout` and `IdleTimeout`; abandoned stateful sessions by
  `SessionTimeout`.

## Error responses

HTTP-layer errors the integration raises itself follow the
[GraphQL spec error envelope](https://spec.graphql.org/draft/#sec-Errors), the
same wire shape used by the REST and GraphQL integrations:

```json
{
  "errors": [
    {"message": "…", "extensions": {"code": "…"}}
  ]
}
```

They are: `404` for an unknown path, `405` for a method the MCP path does not
route (anything but `GET`, `POST`, `DELETE`), `403` for a `Host` not in
`AllowedHosts`, the readiness probe's `503`, and the `500` a panic is answered
with (see [Panics](#panics)). The localized message is selected via the
`Accept-Language` header (see `mcp.AddOrEditLanguage`) and the canonical machine
code comes from `errorstack.HTTPStatusToCode`.

Responses written by the Go MCP SDK and by net/http are **plain text**, not the
envelope:

- the bearer token gate: `401` (missing, invalid or expired token), `403`
  (insufficient scope), `400` (`auth.ErrOAuth`), `429` (`mcp.ErrTooManyRequests`),
  `503` (`mcp.ErrServiceUnavailable`) — with `WWW-Authenticate` on `401` and `403`,
  and `Retry-After` on a `429` or a `503` whose error names a wait;
- the transport: `405` on `GET`/`DELETE` in stateless mode, `413` for a body over
  `MaxRequestBodyBytes`, `415` for a `Content-Type` other than JSON, `400` for a
  missing `Accept`, an unsupported `MCP-Protocol-Version` or a missing session
  header, `404` for an unknown session, `403` for a session of another subject or
  for the loopback `Host` check;
- the cross-origin protection: `403`.

Errors raised *inside* a tool handler are returned through the MCP tool result
(`IsError: true`) by the SDK, as the protocol requires, and JSON-RPC errors in the
JSON-RPC response; neither is reshaped by this integration.

### Panics

A panic on the goroutine serving the HTTP request (in `Middleware`, in a token
validator, in a probe, in the transport's own handling of the request) is
answered with a `500` error envelope, `INTERNAL_ERROR`, rather than a dropped
connection and a stack on the process's standard error. The answer is written
just inside `EdgeMiddleware`, so it carries the headers `EdgeMiddleware` set
before handing the request on, and none of the ones set further in for the answer
that never completed. It says nothing of the panic, which is logged with its
stack against the request's context and recorded on its span.

A panic once the response has started, as on a stream already flushed, cannot be
answered any more: it is logged and recorded the same way, and the response is
aborted with `http.ErrAbortHandler`. A handler that panics with
`http.ErrAbortHandler` itself aborts as it always did.

The SDK handles each MCP method on a goroutine of its own, outside the HTTP
chain, and recovers nothing there. A panic in a tool's, a prompt's or a
resource's handler, or in a receiving middleware, is recovered on that goroutine
rather than ending the process. A tool call is answered with a tool error
(`IsError`, one text: `Internal server error`), as a handler returning an error
is, so the model reads that the tool failed; any other method with the JSON-RPC
internal error (`-32603`, message `Internal server error`). Neither carries
anything of the panic, whose value and stack are logged and recorded on the
method's span, and the session goes on serving its other calls. A handler that
wants its own answer to a panic, in the shape of its other errors, recovers for
itself: this one then never sees it.

## Telemetry

### Spans

| Span                 | Kind     | When                                                     |
|----------------------|----------|----------------------------------------------------------|
| `MCP: <HTTP method>` | server   | Every HTTP request, e.g. `MCP: POST`, `MCP: GET`.        |
| `MCP: <MCP method>`  | internal | Every MCP method served, e.g. `MCP: tools/call`, nested under the HTTP span. |

MCP method spans are named for the method only — never for the tool, prompt or
resource, which the client chooses — and carry it as an attribute instead. A tool
call whose result reports an error (`IsError`) is recorded as an error, as is a
JSON-RPC error.

Attributes of the HTTP span:
- `mcp.name`
- `mcp.method`
- `mcp.path`
- `mcp.status_code`
- `mcp.request.size` (when the request declares its length)
- `mcp.response.size`
- the attributes of the request's `Event`, when one is in the request context
  (`event.*`, `enduser.id`, `client.address`, `user_agent.original`)

Attributes of the MCP method span:
- `mcp.name`
- `mcp.rpc.method`
- `mcp.tool` (`tools/call`)
- `mcp.prompt` (`prompts/get`)
- `mcp.uri` (`resources/read`)

Example:
```
MCP: POST
  mcp.name: "greeter"
  mcp.method: "POST"
  mcp.path: "/mcp"
  mcp.status_code: 200
  mcp.request.size: 128
  mcp.response.size: 256

  MCP: tools/call
    mcp.name: "greeter"
    mcp.rpc.method: "tools/call"
    mcp.tool: "greet"
```

### Metrics

| Metric                   | Unit | Attributes                                 |
|--------------------------|------|--------------------------------------------|
| `mcp.request.duration`   | `s`  | `mcp.name`, `mcp.method`, `mcp.status_code` |
| `mcp.request.size`       | `By` | `mcp.name`, `mcp.method`, `mcp.status_code` |
| `mcp.response.size`      | `By` | `mcp.name`, `mcp.method`, `mcp.status_code` |
| `mcp.operation.duration` | `s`  | `mcp.name`, `mcp.operation` (the MCP method), `mcp.outcome` (`ok` or `error`) |

## Health probes

The `mcp` integration exposes two health probe endpoints following Kubernetes
conventions. Both bypass `Config.OAuth`, `Config.Middleware` and
`Config.AllowedHosts`, so they are never blocked by authentication or other
service-level checks. `Config.EdgeMiddleware` wraps them like every other
response.

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

Reports the health of every integration registered with the `Service` —
dependencies and servers alike. When all of them are healthy, the response is:

```json
{"data":null}
```

Otherwise it is a generic `503`, whatever failed and why:

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

The probe is served to whoever asks, and the reason a dependency is unhealthy
routinely names internal hosts, so which integration failed, and why, goes to the
log and to the probe's span instead. Pass a custom `Readiness` function in the
config to override this behavior.
