# helix.go - REST API integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/rest)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/rest)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/rest)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The REST API integration provides an opinionated way to build an HTTP REST API
with support for OpenAPI validations. It is a **server** integration registered
via `service.Serve()` — only one server can be registered per service.

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
http.route: "/anything"
network.peer.address: "127.0.0.1"
network.peer.port: 50643
network.protocol.version: "1.1"
server.address: "localhost"
server.port: 8080
url.path: "/anything"
url.scheme: "http"
user_agent.original: "insomnia/2023.2.2"
```

## Health check

The `rest` integration allows passing a custom HTTP handler function for health
check. It is exposed at `GET /health`.

Example:
```sh
$ curl --request GET \
    --url http://localhost:8080/health
```

By default if no custom function is passed, the `rest` integration retrieves the
health status of each integration attached to the service running the `rest`
integration, and returns the highest HTTP status code returned. This means if all
integrations are healthy (status `200`) but one is temporarily unavailable (status
`503`), the HTTP status code would be `503`, and therefore the response body of
the health check would be:
```json
{
  "status": "Service Unavailable"
}
```

## Usage

Install the Go module with:
```sh
$ go get github.com/mountayaapp/helix.go/integration/rest
```

Simple example on how to import, configure, and use the integration:

```go
import (
  "net/http"

  "github.com/mountayaapp/helix.go/integration/rest"
)

cfg := rest.Config{
  Address: ":8080",
  OpenAPI: rest.ConfigOpenAPI{
    Enabled:     true,
    Description: "./descriptions/openapi.yaml",
  },
}

router, err := rest.New(cfg)
if err != nil {
  return err
}

router.POST("/users/:id", func(rw http.ResponseWriter, req *http.Request) {
  params, ok := rest.ParamsFromContext(req.Context())
  if !ok {
    rest.NewResponseError[rest.NoMetadata](req).
      SetStatus(http.StatusInternalServerError).
      Write(rw)
    return
  }

  userID := params["id"]
  
  // ...
  
  rest.NewResponseSuccess[rest.NoMetadata, rest.NoData](req).
    SetStatus(http.StatusAccepted).
    Write(rw)
})
```
