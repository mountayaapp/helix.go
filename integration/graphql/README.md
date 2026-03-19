# helix.go - GraphQL server integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/graphql)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/graphql)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/graphql)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The GraphQL server integration provides an opinionated way to build a GraphQL API
on top of [gqlgen](https://github.com/99designs/gqlgen). It handles the HTTP
server lifecycle, health endpoint, and integrates with OpenTelemetry for
distributed tracing. It is a **server** integration — calling `graphql.New(svc, cfg)`
automatically registers the server via `service.Serve()`. Only one server can be
registered per Service.

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

- `Address` (`string`) — HTTP address to listen on. Default: `":8080"`.
- `Path` (`string`) — URL path for the GraphQL endpoint. Default: `"/graphql"`.
- `Schema` (`gqlgen.ExecutableSchema`) — gqlgen executable schema to serve.
  **Required**.
- `GraphiQL` (`ConfigGraphiQL`) — GraphiQL IDE settings. See [GraphiQL](#graphiql).
- `APQ` (`ConfigAPQ`) — Automatic Persisted Queries settings. See [APQ](#apq).
- `Healthcheck` (`func(*http.Request) int`) — Custom health check handler for
  `GET /health`. Default: aggregates the status of all attached dependencies.
- `Middleware` (`func(http.Handler) http.Handler`) — Wraps the built-in HTTP
  handler, useful for adding a middleware chain. The `GET /health` endpoint is
  excluded from this middleware so it always responds without requiring
  authentication or other service-level checks.
- `TLS` (`integration.ConfigTLS`) — TLS settings.

### GraphiQL

- `Enabled` (`bool`) — Enable the GraphiQL browser IDE. Default: `false`.
- `Path` (`string`) — URL path for GraphiQL. Default: `"/graphiql"`.

When GraphiQL is enabled, schema introspection is also enabled.

### APQ

Automatic Persisted Queries allow clients to send a SHA-256 hash instead of the
full query string on subsequent requests, reducing bandwidth. Queries are cached
in Valkey.

- `Enabled` (`bool`) — Enable Automatic Persisted Queries. Default: `false`.
- `Prefix` (`string`) — Key prefix for cached queries in Valkey. Default: `"apq:"`.
- `TTL` (`time.Duration`) — Time-to-live for cached queries. Default: `1h`.
- `Valkey` (`valkey.Valkey`) — Valkey integration instance for cache storage.
  **Required** when enabled.

## Usage

### Basic setup

```go
import (
  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/graphql"

  "example.com/app/graph"
)

svc, err := service.New()
if err != nil {
  panic(err)
}

err = graphql.New(svc, graphql.Config{
  Address: ":8080",
  Schema: graph.NewExecutableSchema(graph.Config{
    Resolvers: &graph.Resolver{},
  }),
  GraphiQL: graphql.ConfigGraphiQL{
    Enabled: true,
  },
})
if err != nil {
  panic(err)
}
```

### With Automatic Persisted Queries

APQ requires a Valkey integration for cache storage:

```go
import (
  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/graphql"
  "github.com/mountayaapp/helix.go/integration/valkey"

  "example.com/app/graph"
)

svc, err := service.New()
if err != nil {
  panic(err)
}

v, err := valkey.Connect(svc, valkey.Config{
  Address: "127.0.0.1:6379",
})
if err != nil {
  panic(err)
}

err = graphql.New(svc, graphql.Config{
  Address: ":8080",
  Schema: graph.NewExecutableSchema(graph.Config{
    Resolvers: &graph.Resolver{},
  }),
  APQ: graphql.ConfigAPQ{
    Enabled: true,
    Valkey:  v,
  },
})
if err != nil {
  panic(err)
}
```

## Trace attributes

The `graphql` integration sets the following trace attributes:
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
http.request.body.size: 64
http.request.method: "POST"
http.response.body.size: 128
http.response.status_code: 200
http.route: "/graphql"
network.peer.address: "127.0.0.1"
network.peer.port: 50643
network.protocol.version: "1.1"
server.address: "localhost"
server.port: 8080
url.path: "/graphql"
url.scheme: "http"
user_agent.original: "insomnia/2023.2.2"
```

## Health check

The `graphql` integration exposes a health check endpoint at `GET /health`.

```sh
$ curl --request GET \
    --url http://localhost:8080/health
```

The health endpoint bypasses the `Middleware` configured in `Config`, so it is
never blocked by authentication or other service-level middleware.

The endpoint aggregates the health status of all dependencies attached to the
service, returning the highest HTTP status code. When APQ is enabled, the Valkey
connection status is also taken into account. If all dependencies are healthy
(`200`) but Valkey is temporarily unavailable (`503`), the response is:

```json
{
  "status": "Service Unavailable"
}
```

Pass a custom `Healthcheck` function in the config to override this behavior.
