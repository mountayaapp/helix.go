# helix.go - GraphQL server integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/graphql)
[![Latest version](https://img.shields.io/github/v/tag/mountayaapp/helix.go?filter=v*&sort=semver&label=version)](https://github.com/mountayaapp/helix.go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The GraphQL server integration provides an opinionated way to build a GraphQL API
on top of [gqlgen](https://github.com/99designs/gqlgen). It handles the HTTP
server lifecycle, the health endpoints, request limits and error masking, and
integrates with OpenTelemetry for distributed tracing. It is a **server**
integration: calling `graphql.New(ctx, svc, cfg)` builds the server and registers
it with the Service through `service.Serve()`. The Service starts it in `Run` and
drains it in `Stop`; `New` returns no handle to it. Several servers can be
registered on one Service, each under its own `Name`.

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/graphql
```

## About gqlgen

[`gqlgen`](https://gqlgen.com) is a Go library for building GraphQL servers. It
takes a schema-first approach: you define your GraphQL schema using the GraphQL
Schema Definition Language (SDL), and gqlgen generates the Go types, resolvers,
and server boilerplate for you.

To generate your executable schema, install the gqlgen CLI and run:
```sh
$ go install github.com/99designs/gqlgen@latest
$ gqlgen init
$ gqlgen generate
```

The generated `ExecutableSchema` is then passed to this integration via `Config.Schema`.
Refer to the [gqlgen documentation](https://gqlgen.com/getting-started/) for more
details on schema design, resolver implementation, and code generation.

## Configuration

- `Name` (`string`) — Identifies the server in health output, error context and
  telemetry (`graphql.name`). Two servers registered on the same Service must have
  different names. Default: `"graphql"`.
- `Address` (`string`) — HTTP address to listen on. Default: `":8080"`.
- `Path` (`string`) — URL path of the GraphQL endpoint. It must start with `/`, be
  a clean path without wildcards, query, fragment or whitespace, and be neither
  `/` nor one of the health probe paths (`/health`, `/ready`), which bypass
  `Middleware`. Default: `"/graphql"`.
- `Schema` (`graphql.ExecutableSchema`) — gqlgen executable schema to serve.
  **Required**.
- `PublicEndpoint` (`bool`) — Marks a server reachable from outside the trust
  boundary of your services. See [Security](#security). Default: `false`.
- `GraphiQL` (`ConfigGraphiQL`) — GraphiQL IDE settings. See [GraphiQL](#graphiql).
- `Introspection` (`ConfigIntrospection`) — Introspection settings. See
  [Introspection](#introspection).
- `APQ` (`ConfigAPQ`) — Automatic Persisted Queries settings. See [APQ](#apq).
- `QueryCacheSize` (`int`) — Number of parsed and validated query documents kept
  in memory, so a repeated operation is not parsed again. A value below one falls
  back to the default. Default: `1000`.
- `ParserTokenLimit` (`int`) — Number of tokens the parser reads from a query
  before refusing it (`GRAPHQL_PARSE_FAILED`). Default: `15000`.
- `ComplexityLimit` (`int`) — Highest complexity an operation may have, as
  computed by gqlgen: one per selected field unless the schema declares costs of
  its own. A more complex operation is refused before it runs
  (`COMPLEXITY_LIMIT_EXCEEDED`). Default: `1000`.
- `DepthLimit` (`int`) — Deepest an operation's selections may nest, fragments
  included; a root field is at depth one. Introspection fields are not counted. A
  deeper operation is refused before it runs (`DEPTH_LIMIT_EXCEEDED`). Default:
  `15`.
- `MaxRequestBodyBytes` (`int64`) — Largest request body accepted. A larger one is
  refused with `413` before the GraphQL layer reads it. Default: `10485760`
  (10 MiB).
- `TraceResolvers` (`graphql.ResolverTracing`) — Which resolvers get a span of
  their own: `ResolverTracingRoot` (`"root"`), `ResolverTracingAll` (`"all"`) or
  `ResolverTracingNone` (`"none"`). See [Traces](#traces). Default: `"root"`.
- `RequestTimeout` (`time.Duration`) — Budget of a GraphQL request once its body
  has been read. It rides the request context, so everything a resolver derives
  from it is cancelled once it is spent. `0` leaves requests unbounded. Default:
  `0`.
- `ReadHeaderTimeout` (`time.Duration`) — How long a client may take to send its
  request headers. Default: `10s`.
- `ReadTimeout` (`time.Duration`) — How long a client may take to send a whole
  request, headers and body. Default: `30s`.
- `IdleTimeout` (`time.Duration`) — How long a keep-alive connection may stay idle
  between requests. Default: `120s`.
- `Readiness` (`func(*http.Request) int`) — Custom readiness probe for
  `GET /ready`. Should return `200` for ready, `5xx` otherwise. Default:
  aggregates the health of every integration attached to the Service.
- `Middleware` (`func(http.Handler) http.Handler`) — Wraps the built-in HTTP
  handler, for adding a middleware chain (authentication, CORS, rate limiting).
  The `GET /health` and `GET /ready` endpoints bypass it, so they always respond
  without authentication or other service-level checks.
- `Configure` (`func(*handler.Server)`) — Called with gqlgen's server as the last
  step of `New`, for what `Config` does not expose: an extension of your own, a
  validation rule. It changes the server on top of this integration's settings;
  replacing the error presenter or the recover function gives up the masking and
  logging described below. Only `POST` and `OPTIONS` requests are routed to the
  server, so adding a transport has no effect.
- `TLS` (`integration.ConfigTLS`) — TLS settings. When `Enabled`, `CertPEM` and
  `KeyPEM` are required: the server's certificate (followed by any intermediates)
  and its private key. `ClientCAPEMs` turns on mutual TLS: clients must then
  present a certificate signed by one of these authorities. TLS 1.2 is the
  minimum version.

A negative limit or timeout is a validation error: an unset one already means the
default.

### GraphiQL

- `Enabled` (`bool`) — Serves the GraphiQL browser IDE. Default: `false`.
- `Path` (`string`) — URL path of GraphiQL. It follows the rules of `Path`, and
  must differ from it. Default: `"/graphiql"`.

GraphiQL loads the schema through introspection. Enabling GraphiQL does **not**
enable introspection: without `Introspection.Enabled`, GraphiQL cannot load the
schema, and `New` logs a warning.

### Introspection

- `Enabled` (`bool`) — Answers introspection queries (`__schema`, `__type`).
  Default: `false`.

When introspection is disabled, field suggestions in validation errors ("Did you
mean …?") are disabled too: they would otherwise reveal the schema one typo at a
time.

### APQ

Automatic Persisted Queries let clients send a SHA-256 hash instead of the full
query string on subsequent requests, reducing bandwidth.

- `Enabled` (`bool`) — Enables Automatic Persisted Queries. Default: `false`.
- `Cache` (`graphql.Cache`) — Where queries are stored. **Required** when enabled.
- `MaxQueryLength` (`int`) — Length in bytes of the longest query stored. A longer
  query is still executed, but not persisted: a client sending only its hash is
  asked for the full query again. Default: `32768` (32 KiB).

`Cache` is a two-method interface, keyed by the query's hash:

```go
type Cache interface {
  Get(ctx context.Context, key string) (string, bool)
  Add(ctx context.Context, key, query string)
}
```

It is the method set of gqlgen's `graphql.Cache[string]`, so gqlgen's in-process
LRU cache satisfies it as is — `lru.New[string](1000)` from
`github.com/99designs/gqlgen/graphql/handler/lru`. To share persisted queries
between the replicas of a service, the [valkey integration](../valkey) provides
`valkey.NewQueryCache`. This module does not depend on the valkey module: only an
application that uses it imports it.

A query is stored only once it has parsed, validated and passed the depth and
complexity limits: a client cannot fill the shared store with documents the
server would never run.

## Usage

### Creating a server

```go
import (
  "context"
  "os"
  "os/signal"
  "syscall"

  "github.com/mountayaapp/helix.go/integration/graphql"
  "github.com/mountayaapp/helix.go/service"

  "example.com/app/graph"
)

func run() error {
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

  svc, err := service.New(ctx, service.Config{})
  if err != nil {
    return err
  }

  err = graphql.New(ctx, svc, graphql.Config{
    Address: ":8080",
    Schema: graph.NewExecutableSchema(graph.Config{
      Resolvers: &graph.Resolver{},
    }),
    GraphiQL: graphql.ConfigGraphiQL{
      Enabled: true,
    },
    Introspection: graphql.ConfigIntrospection{
      Enabled: true,
    },
  })
  if err != nil {
    return err
  }

  // Run starts the server and blocks until ctx is cancelled, then drains it
  // along with the rest of the Service.
  return svc.Run(ctx)
}
```

`New` returns a `VALIDATION_FAILED` error when `svc` is nil or the `Config` is not
valid, including a TLS key pair that does not parse.

### With Automatic Persisted Queries

Shared between replicas, through the valkey integration:

```go
import (
  "github.com/mountayaapp/helix.go/integration/graphql"
  "github.com/mountayaapp/helix.go/integration/valkey"
)

v, err := valkey.Connect(ctx, svc, valkey.Config{
  Address: "127.0.0.1:6379",
})
if err != nil {
  return err
}

err = graphql.New(ctx, svc, graphql.Config{
  Schema: graph.NewExecutableSchema(graph.Config{
    Resolvers: &graph.Resolver{},
  }),
  APQ: graphql.ConfigAPQ{
    Enabled: true,
    Cache:   valkey.NewQueryCache(v, valkey.OptionsQueryCache{}),
  },
})
```

The key prefix and the time-to-live of stored queries are options of the valkey
cache; see the valkey integration.

In process, one cache per replica:

```go
import "github.com/99designs/gqlgen/graphql/handler/lru"

err = graphql.New(ctx, svc, graphql.Config{
  Schema: graph.NewExecutableSchema(graph.Config{
    Resolvers: &graph.Resolver{},
  }),
  APQ: graphql.ConfigAPQ{
    Enabled: true,
    Cache:   lru.New[string](1000),
  },
})
```

## Error responses

GraphQL responses follow the [GraphQL spec error envelope](https://spec.graphql.org/draft/#sec-Errors),
both for the GraphQL execution layer and for HTTP-layer errors (`400`, `404`,
`405`, `413`) returned by this integration before the GraphQL layer is reached:

```json
{
  "errors": [
    {"message": "…", "path": [...], "extensions": {"code": "…"}}
  ]
}
```

HTTP-layer error messages are localized from the request's `Accept-Language`
header or `lang` cookie; see `AddOrEditLanguage`.

### What reaches the client

The integration wires a gqlgen error presenter that decides what of an error the
client sees:

- An `*errorstack.Error` in the error chain is rendered entry by entry, with its
  messages and `extensions.code`. Its messages are written for the client by
  construction.
- A `*gqlerror.Error` that wraps no other error is passed through. That is what
  gqlgen builds for a parse or validation failure or a refused limit, and what a
  resolver builds when it means its message for the client. One that refused the
  request before execution without a code of its own — gqlgen's APQ extension
  returns a few — is given `BAD_REQUEST`.
- **Anything else is masked** as `"Internal server error"` with the code
  `INTERNAL_ERROR`: a driver error, a wrapped `fmt.Errorf`, the error a custom
  scalar's `UnmarshalGQL` returns. Such errors are written for logs, and routinely
  name hosts, users and tables. The real error is recorded on the operation span.

A resolver that panics is answered the same way; the panic is logged through the
Service's logger, with its stack, and recorded on the spans.

The body of a request that is not valid JSON, or not a GraphQL request, is never
echoed back: the request is answered with a `400` `BAD_REQUEST` envelope. So is a
`POST` whose `Content-Type` is not `application/json`.

### Single vs. multi-entry errors

Returning `*errorstack.Error` from a resolver always produces one element of
`errors[]` per Entry — single- and multi-entry are handled the same way:

```go
return nil, errorstack.New("Resource does not exist", errorstack.WithCode(errorstack.CodeNotFound))
```

```go
func (r *mutationResolver) CreateUser(ctx context.Context, input UserInput) (*User, error) {
  return nil, errorstack.NewValidation(
    errorstack.Entry{
      Message: "Must be a valid email address",
      Path:    []any{"input", "email"},
    },
    errorstack.Entry{
      Message: "Must be set",
      Path:    []any{"input", "name"},
    },
  )
}
```

```json
{
  "errors": [
    {
      "message": "Must be a valid email address",
      "path": ["createUser"],
      "extensions": {"code": "VALIDATION_FAILED", "input_path": ["input", "email"]}
    },
    {
      "message": "Must be set",
      "path": ["createUser"],
      "extensions": {"code": "VALIDATION_FAILED", "input_path": ["input", "name"]}
    }
  ]
}
```

`errors[].path` is the path of the response field that errored, which the GraphQL
spec reserves it for. An entry's `Path` names a part of the request — an input
field — so it is rendered as `extensions.input_path`. The other per-entry
extensions are copied as they are. The response-level `Extensions` of an
`*errorstack.Error` (set with `SetExtension`) go to the response's top-level
`extensions`; when several errors set the same key, the first one wins.

For the rare case where a resolver wants to surface validation errors *and*
return a partial successful payload, call `AddErrors(ctx, err)` and return the
payload (and a `nil` error) from the resolver:

```go
graphql.AddErrors(ctx, validation)
return partial, nil
```

## Health probes

The `graphql` integration exposes two health probe endpoints following Kubernetes
conventions. Both bypass the `Middleware` configured in `Config`, so they are
never blocked by authentication or other service-level middleware.

### Liveness — `GET /health`

```sh
$ curl --request GET \
    --url http://localhost:8080/health
```

Returns `200` immediately, with `{"data":null}`. No dependency checks are
performed. Use this as a liveness probe to verify the process is running and able
to serve traffic.

### Readiness — `GET /ready`

```sh
$ curl --request GET \
    --url http://localhost:8080/ready
```

Checks the health of every integration attached to the Service. When all are
healthy, the response is `200` with:

```json
{"data":null}
```

When one is not, the response is a generic `503`, whatever the cause:

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

The probe is served on the same listener as the API, to whoever asks, so the body
never says what failed or why: that goes to the log and to the request's span.
The APQ cache is not probed by this server: a valkey cache's connection is
attached to the Service by `valkey.Connect`, and checked in its own right.

Pass a custom `Readiness` function in the config to override this behavior. A
status outside `100`–`999` is answered as `500`.

## Telemetry

### Traces

Every request is a `server` span named after its method, such as `GraphQL: POST`
or `GraphQL: GET`. It carries:
- `graphql.name`
- `graphql.method`
- `graphql.path`
- `graphql.status_code`
- `graphql.request.size` — when the request declares its length
- `graphql.response.size`

plus the `event.*` attributes of the request's Event, when the application's
middleware put one in the request context.

Under it, every GraphQL operation is an `internal` span named after the operation
type: `GraphQL: Query`, `GraphQL: Mutation` or `GraphQL: Subscription`. The
operation name is chosen by the client, so it is an attribute rather than part of
the span name. It carries:
- `graphql.name`
- `graphql.operation.type` — `query`, `mutation` or `subscription`
- `graphql.operation.name` — only for a named operation
- `graphql.errors` — the number of errors in the response, when there are any

A GraphQL response can fail partially while its HTTP status is `200`, so the
operation span is where a failure shows. It is marked as an error only for the
errors the server is responsible for. Errors whose `extensions.code` is the
client's doing leave it unset, the way a `4xx` leaves an HTTP span unset:
`BAD_REQUEST`, `UNAUTHORIZED`, `PAYMENT_REQUIRED`, `FORBIDDEN`, `NOT_FOUND`,
`METHOD_NOT_ALLOWED`, `CONFLICT`, `PAYLOAD_TOO_LARGE`, `TOO_MANY_REQUESTS`,
`VALIDATION_FAILED`, `GRAPHQL_PARSE_FAILED`, `GRAPHQL_VALIDATION_FAILED`,
`PERSISTED_QUERY_NOT_FOUND`, `COMPLEXITY_LIMIT_EXCEEDED` and
`DEPTH_LIMIT_EXCEEDED`. A request whose document does not parse has no operation,
and no operation span.

Under the operation, resolvers selected by `TraceResolvers` are `internal` spans
named `GraphQL: <Object>.<field>`, such as `GraphQL: Query.users`. They carry:
- `graphql.name`
- `graphql.object`
- `graphql.field`

By default only root fields are traced: they are where an operation reaches out
to a database or another service, and their number is bounded by the document. A
nested resolver runs once per parent item, so tracing every resolver
(`ResolverTracingAll`) turns a list of a thousand items into a thousand spans.
Fields gqlgen resolves by reading a struct member never get a span. A resolver that
panics is recorded as an error.

Example:
```
GraphQL: POST
  graphql.name: "graphql"
  graphql.method: "POST"
  graphql.path: "/graphql"
  graphql.status_code: 200
  graphql.request.size: 64
  graphql.response.size: 128
└── GraphQL: Query
      graphql.name: "graphql"
      graphql.operation.type: "query"
      graphql.operation.name: "ListUsers"
    └── GraphQL: Query.users
          graphql.name: "graphql"
          graphql.object: "Query"
          graphql.field: "users"
```

### Metrics

- `graphql.request.duration` (histogram, seconds) — duration of inbound HTTP
  requests.
- `graphql.request.size` (histogram, bytes) — size of request bodies, when
  declared.
- `graphql.response.size` (histogram, bytes) — size of response bodies.

These three carry the attributes `graphql.name`, `graphql.method` and
`graphql.status_code`.

- `graphql.operation.duration` (histogram, seconds) — duration of GraphQL
  operations, with the attributes `graphql.name`, `graphql.operation` (`Query`,
  `Mutation` or `Subscription`) and `graphql.outcome` (`ok` or `error`, following
  the same classification as the span).

## Security

- **Limits.** Request bodies, parser tokens, operation depth and complexity are
  bounded by default (see [Configuration](#configuration)), and an over-limit
  operation is refused before any resolver runs. So are slow clients:
  `ReadHeaderTimeout`, `ReadTimeout` and `IdleTimeout` all default to a bound.
- **Errors.** Only errors written for a client reach one; see
  [What reaches the client](#what-reaches-the-client).
- **Schema exposure.** Introspection, GraphiQL and field suggestions are off by
  default.
- **`PublicEndpoint`.** By default the server continues the caller's trace and
  reads its baggage, which is what makes a request crossing several of your
  services one trace. A server reachable from the internet should set
  `PublicEndpoint: true`: it then starts a new trace for every request, linked to
  the caller's, and ignores inbound baggage, so that a client can neither decide
  whether your spans are sampled nor have an Event propagated onward as though a
  trusted service had set it.
- **APQ.** Persisted queries are shared by every client. Only queries that pass
  validation and the limits, and are no longer than `MaxQueryLength`, are stored.
