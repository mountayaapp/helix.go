# helix.go - GraphQL server integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/graphql)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/graphql)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/graphql)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The GraphQL server integration provides an opinionated way to build a GraphQL API
on top of [gqlgen](https://github.com/99designs/gqlgen). It handles the HTTP
server lifecycle, health endpoint, and integrates with OpenTelemetry for distributed
tracing. It is a **server** integration registered via `service.Serve()` — only
one server can be registered per service.

## About `gqlgen`

[`gqlgen`](https://gqlgen.com) is a Go library for building GraphQL servers. It
takes a schema-first approach: you define your GraphQL schema using the GraphQL
SchemaDefinition Language (SDL), and gqlgen generates the Go types, resolvers,
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

## Trace attributes

The `graphql` integration sets the following trace attributes via OpenTelemetry's
HTTP instrumentation:
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

Example:
```sh
$ curl --request GET \
    --url http://localhost:8080/health
```

The `graphql` integration retrieves the health status of each integration attached
to the service running the `graphql` integration, and returns the highest HTTP status
code returned. When APQ is enabled and backed by a Valkey integration, the Valkey
connection status is also taken into account. This means if all integrations are
healthy (status `200`) but Valkey is temporarily unavailable (status `503`), the
response body of the health check would be:
```json
{
  "status": "Service Unavailable"
}
```

## Usage

Install the Go module with:
```sh
$ go get github.com/mountayaapp/helix.go/integration/graphql
```

Simple example on how to import, configure, and use the integration:

```go
import (
  "github.com/mountayaapp/helix.go/integration/graphql"

  "example.com/app/graph"
)

cfg := graphql.Config{
  Address: ":8080",
  Path:    "/graphql",
  Schema:  graph.NewExecutableSchema(graph.Config{
    Resolvers: &graph.Resolver{},
  }),
  GraphiQL: graphql.ConfigGraphiQL{
    Enabled: true,
  },
}

err := graphql.New(cfg)
if err != nil {
  return err
}
```

With Automatic Persisted Queries enabled:

```go
import (
  "github.com/mountayaapp/helix.go/integration/graphql"
  "github.com/mountayaapp/helix.go/integration/valkey"

  "example.com/app/graph"
)

v, _ := valkey.Connect(valkey.Config{
  Address: "localhost:6379",
})

cfg := graphql.Config{
  Address: ":8080",
  Schema:  graph.NewExecutableSchema(graph.Config{
    Resolvers: &graph.Resolver{},
  }),
  APQ: graphql.ConfigAPQ{
    Enabled: true,
    Valkey:  v,
  },
}

err := graphql.New(cfg)
if err != nil {
  return err
}
```
