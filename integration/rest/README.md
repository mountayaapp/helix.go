# helix.go - REST API integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/rest)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/rest)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/rest)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The REST API integration provides an opinionated way to build an HTTP REST API
with support for OpenAPI validations. It is a **server** integration — calling
`rest.New(svc, cfg)` automatically registers the server via `service.Serve()`.
Only one server can be registered per Service.

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/rest
```

## Configuration

- `Address` (`string`) — HTTP address to listen on. Default: `":8080"`.
- `Readiness` (`func(*http.Request) int`) — Custom readiness probe handler for
  `GET /ready`. Should return `200` for ready, `5xx` for error. Default:
  aggregates the status of all attached dependencies.
- `Middleware` (`func(http.Handler) http.Handler`) — Wraps the built-in HTTP
  handler, useful for adding a middleware chain. The `GET /health` and
  `GET /ready` endpoints are excluded from this middleware so they always respond
  without requiring authentication or other service-level checks.
- `OpenAPI` (`ConfigOpenAPI`) — OpenAPI validation settings. See [OpenAPI](#openapi).
- `TLS` (`integration.ConfigTLS`) — TLS settings.

### OpenAPI

- `Enabled` (`bool`) — Enable request/response validation against an OpenAPI
  description. Default: `false`.
- `Description` (`string`) — Path to a local file or a URL containing the
  OpenAPI spec. **Required** when enabled.

When enabled, invalid requests return `400` with structured validation errors;
invalid responses are logged but still sent to the client.

## Usage

### Creating a server

```go
import (
  "net/http"

  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/rest"
)

svc, err := service.New()
if err != nil {
  panic(err)
}

router, err := rest.New(svc, rest.Config{
  Address: ":8080",
})
if err != nil {
  panic(err)
}

router.GET("/hello", func(rw http.ResponseWriter, req *http.Request) {
  rest.NewResponseSuccess[rest.NoMetadata, rest.NoData](req).
    SetStatus(http.StatusOK).
    Write(rw)
})
```

### Path parameters

Use `:param` syntax in route paths. Extract them with `rest.ParamsFromContext`:

```go
router.GET("/users/:id", func(rw http.ResponseWriter, req *http.Request) {
  params, _ := rest.ParamsFromContext(req.Context())
  userID := params["id"]

  // ...
  _ = userID
})
```

Multiple parameters are supported: `/orgs/:org_id/users/:user_id`.

### Success responses

`ResponseSuccess[Metadata, Data]` is a generic type for `2xx` responses. The JSON
body has the shape `{"status":"...","metadata":{...},"data":{...}}`.

```go
type Pagination struct {
  Page  int `json:"page"`
  Total int `json:"total"`
}

type User struct {
  ID   string `json:"id"`
  Name string `json:"name"`
}

router.GET("/users/:id", func(rw http.ResponseWriter, req *http.Request) {
  rest.NewResponseSuccess[Pagination, User](req).
    SetStatus(http.StatusOK).
    SetMetadata(Pagination{Page: 1, Total: 42}).
    SetData(User{ID: "usr_123", Name: "Alice"}).
    Write(rw)
})
```

Response JSON:
```json
{
  "status": "OK",
  "metadata": {
    "page": 1,
    "total": 42
  },
  "data": {
    "id": "usr_123",
    "name": "Alice"
  }
}
```

Use `rest.NoMetadata` and `rest.NoData` when you don't need those fields:

```go
rest.NewResponseSuccess[rest.NoMetadata, rest.NoData](req).
  SetStatus(http.StatusCreated).
  Write(rw)
```

Response JSON:
```json
{
  "status": "Created"
}
```

### Error responses

`ResponseError[Metadata]` is a generic type for `3xx`/`4xx`/`5xx` responses. The
error message is automatically localized based on the `Accept-Language` header.

```go
router.GET("/users/:id", func(rw http.ResponseWriter, req *http.Request) {
  rest.NewResponseError[rest.NoMetadata](req).
    SetStatus(http.StatusNotFound).
    Write(rw)
})
```

Response JSON:
```json
{
  "status": "Not Found",
  "error": {
    "message": "The requested resource could not be found"
  }
}
```

Add structured validation errors with `SetErrorValidations`:

```go
rest.NewResponseError[rest.NoMetadata](req).
  SetStatus(http.StatusBadRequest).
  SetErrorValidations([]errorstack.Validation{
    {
      Message: "must be a valid email address", 
      Path: []string{"request", "body", "email"},
    },
    {
      Message: "is required", 
      Path: []string{"request", "body", "name"},
    },
  }).
  Write(rw)
```

Response JSON:
```json
{
  "status": "Bad Request",
  "error": {
    "message": "The request was invalid or malformed",
    "validations": [
      {
        "message": "must be a valid email address",
        "path": ["request", "body", "email"]
      },
      {
        "message": "is required",
        "path": ["request", "body", "name"]
      }
    ]
  }
}
```

### OpenAPI validation

Enable automatic request/response validation against an OpenAPI spec:

```go
router, err := rest.New(svc, rest.Config{
  Address: ":8080",
  OpenAPI: rest.ConfigOpenAPI{
    Enabled:     true,
    Description: "./descriptions/openapi.yaml",
  },
})
```

Invalid requests are rejected with `400` and structured validation errors.
Invalid responses are traced as errors but still returned to the client.

## Trace attributes

The `rest` integration sets the following trace attributes:
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
http.request.body.size: 18
http.request.method: "POST"
http.response.body.size: 21
http.response.status_code: 202
http.route: "/users/:id"
network.peer.address: "127.0.0.1"
network.peer.port: 50643
network.protocol.version: "1.1"
server.address: "localhost"
server.port: 8080
url.path: "/users/usr_123"
url.scheme: "http"
user_agent.original: "insomnia/2023.2.2"
```

## Health probes

The `rest` integration exposes two health probe endpoints following Kubernetes
conventions. Both bypass the `Middleware` configured in `Config`, so they are
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
returning the highest HTTP status code. If all dependencies are healthy (`200`)
but one is temporarily unavailable (`503`), the response is:

```json
{
  "status": "Service Unavailable"
}
```

Pass a custom `Readiness` function in the config to override this behavior.
