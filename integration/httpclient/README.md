# helix.go - HTTP Client integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/httpclient)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/httpclient)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/httpclient)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The HTTP client integration provides an opinionated way to call an external HTTP
API. It is a **dependency** integration — calling `httpclient.Connect(svc, cfg)`
automatically registers it via `service.Attach()`.

A single client can target one or more interchangeable endpoints:

- **Requests** are round-robined across the endpoints, with automatic failover on
  a transport error or a `5xx` response. A `4xx` response is returned as is.
- **Health** of every endpoint is reported individually through the Service's
  readiness status: an endpoint is healthy while it is reachable and not returning
  `5xx`, so a single endpoint going down is surfaced in `Service.Status` and
  `/ready`. An API with no health endpoint works without any configuration.

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/httpclient
```

## Configuration

- `Name` (`string`) — Human-readable name for logging, tracing, and status
  context. **Required**.
- `Endpoints` (`[]string`) — One or more interchangeable base URLs. **Required**.
  Trailing slashes and surrounding whitespace are trimmed.
- `Headers` (`map[string]string`) — Default headers applied to every request when
  not already set.
- `Timeout` (`time.Duration`) — Per-request timeout. Default: `10s`.
- `HealthPath` (`string`) — Path probed on each endpoint by the default health
  check. Default: `"/health"`. Ignored when `Status` is set.
- `TLS` (`integration.ConfigTLS`) — TLS settings for the underlying transport.
- `Status` (`func(ctx, HTTPClient) (int, error)`) — Optional override for the
  health check, useful when the upstream has no health endpoint. Should return
  `200` when healthy, or a `5xx` status and an error otherwise.

## Usage

### Connecting

```go
import (
  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/httpclient"
)

svc, err := service.New()
if err != nil {
  panic(err)
}

api, err := httpclient.Connect(svc, httpclient.Config{
  Name:      "billing",
  Endpoints: []string{"https://billing-1.internal", "https://billing-2.internal"},
  Headers: map[string]string{
    "Authorization": "Bearer " + token,
  },
})
if err != nil {
  panic(err)
}
```

### Requests

The client returns the raw `*http.Response`: the caller closes `resp.Body` and
decodes the payload.

```go
// GET, with an optional query string in the path.
resp, err := api.Get(ctx, "/v1/invoices?status=open")
if err != nil {
  // ...
}
defer resp.Body.Close()

var invoices []Invoice
if err := json.NewDecoder(resp.Body).Decode(&invoices); err != nil {
  // ...
}

// Any method with a body. The body is replayed if the request fails over to
// another endpoint.
body, _ := json.Marshal(payload)
resp, err = api.Do(ctx, http.MethodPost, "/v1/invoices", body)
if err != nil {
  // ...
}
defer resp.Body.Close()
```

### Health checks

When `Status` is not set, each endpoint is probed with `GET {endpoint}{HealthPath}`
(default `/health`). An endpoint is healthy as long as it is **reachable and
responds below 500** — so an API with no health endpoint (a `404`) is still
healthy, while a connection failure or a `5xx` marks it down. Provide a custom
`Status` for stricter checks (e.g. requiring `2xx`) or to skip probing entirely:

```go
api, err := httpclient.Connect(svc, httpclient.Config{
  Name:      "search",
  Endpoints: []string{"https://search.internal"},
  Status: func(ctx context.Context, client httpclient.HTTPClient) (int, error) {
    resp, err := client.Get(ctx, "/ping")
    if err != nil {
      return 503, err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
      return 200, nil
    }
    return 503, fmt.Errorf("search is unhealthy: %d", resp.StatusCode)
  },
})
```

## Trace attributes

The `httpclient` integration sets the following trace attributes:
- `httpclient.endpoint`
- `httpclient.method`
- `httpclient.status_code`

Example:
```
httpclient.endpoint: "https://billing-1.internal"
httpclient.method: "POST"
httpclient.status_code: 200
```
