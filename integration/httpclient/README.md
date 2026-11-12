# helix.go - HTTP Client integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/httpclient)
[![Latest version](https://img.shields.io/github/v/tag/mountayaapp/helix.go?filter=v*&sort=semver&label=version)](https://github.com/mountayaapp/helix.go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The HTTP client integration provides an opinionated way to call an external HTTP
API. It is a **dependency** integration — calling `httpclient.Connect(ctx, svc, cfg)`
automatically registers it via `service.Attach()`.

A single client can target one or more interchangeable endpoints:

- **Requests** are round-robined across the endpoints, and failed over to the next
  one after a transport error or a `5xx` response when sending them twice is safe.
  A response below `500`, `4xx` included, is returned as is.
- **Every attempt is traced**, and carries the trace context to the service being
  called, so a request crossing several helix services is one trace.
- **Health** of every endpoint can be reported through the Service's readiness
  probe, or, for an upstream the Service can run without, on a gauge that never
  gates readiness. Both are opt-in.
- **Safe by default**: a request path cannot leave an endpoint's host, a redirect
  to another host is not followed, and no query string reaches a span or an error.

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/httpclient
```

## Configuration

- `Name` (`string`) — Identifies this client in health output, error context and
  telemetry, so two clients — one per upstream API — are told apart. Default:
  `"httpclient"`.
- `Endpoints` (`[]string`) — One or more interchangeable base URLs. **Required**.
  Each must be an absolute `http` or `https` URL, and may carry a base path every
  request path is appended to. Credentials, a query string or a fragment are
  refused: credentials belong in `Headers`. Trailing slashes and surrounding
  whitespace are trimmed.
- `Headers` (`map[string]string`) — Default headers applied to every request that
  does not set them already. Copied at `Connect`; only ever sent to the endpoints'
  own hosts.
- `Timeout` (`time.Duration`) — Bounds each attempt: connecting, following
  redirects, and reading the response body. It applies again to every endpoint
  failed over to. Negative disables it. Default: `10s`.
- `Failover` (`httpclient.FailoverPolicy`) — Which failed requests are sent again
  to the next endpoint: `FailoverIdempotent`, `FailoverUnsent` or `FailoverAlways`.
  Default: `FailoverIdempotent`.
- `FollowRedirects` (`bool`) — Follow redirects to another host, without the
  request's headers. Default: `false`.
- `Propagator` (`propagation.TextMapPropagator`) — Injects the trace context and
  the baggage into every request. Default: the Service's propagator.
- `Transport` (`http.RoundTripper`) — The transport requests are sent through,
  wrapped to trace every attempt. Default: an `*http.Transport` tuned for
  connection reuse, configured with `TLS`.
- `HealthPath` (`string`) — Path probed on each endpoint by the default health
  check, starting with `/`. Default: empty, which turns probing off.
- `HealthCheck` (`func(ctx, httpclient.HTTPClient) error`) — Optional override for
  the health check. Takes precedence over `HealthPath`.
- `Optional` (`bool`) — The upstream never gates the Service's readiness: the
  health check answers healthy at once, and a background probe reports the
  upstream on the `httpclient.reachable` gauge instead. Default: `false`.
- `TLS` (`integration.ConfigTLS`) — TLS settings for the default transport, for a
  custom certificate authority or a client certificate. Must not be enabled
  together with `Transport`.

## Usage

### Connecting

```go
import (
  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/httpclient"
)

svc, err := service.New(ctx, service.Config{})
if err != nil {
  return err
}

api, err := httpclient.Connect(ctx, svc, httpclient.Config{
  Name:      "upstream",
  Endpoints: []string{"https://api-1.tld/v1", "https://api-2.tld/v1"},
  Headers: map[string]string{
    "Authorization": "Bearer " + token,
  },
})
if err != nil {
  return err
}

// Run blocks until ctx is done, then closes the client along with every other
// dependency of the Service.
return svc.Run(ctx)
```

What `Connect` returns can send requests, not close: the Service owns the client's
lifecycle, and releases its idle connections when it stops.

### Requests

The client returns the raw `*http.Response`: the caller closes `resp.Body` and
decodes the payload.

```go
// GET, with an optional query string in the path. With the endpoints above, this
// is sent to https://api-1.tld/v1/items?status=open or its twin.
resp, err := api.Get(ctx, "/items?status=open")
if err != nil {
  // ...
}
defer resp.Body.Close()

var items []Item
if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
  // ...
}

// Any method with a body. Per-request headers take precedence over the defaults.
body, _ := json.Marshal(payload)
resp, err = api.Do(ctx, http.MethodPost, "/items", body,
  httpclient.WithHeader("Idempotency-Key", key),
)
if err != nil {
  // ...
}
defer resp.Body.Close()
```

A path is appended to the endpoint, base path included. It must be empty or start
with `/` or `?`: anything else is refused with an error, since `@other.tld/x`,
`.other.tld` or `:8443` appended to `https://api.tld` would name another host or
port — and send it the default `Authorization` header. A path is sent as written,
so escape any value interpolated into it with `url.PathEscape` or
`url.QueryEscape`.

### Full control

`DoRequest` sends an `*http.Request` you build, for a body streamed from an
`io.Reader` or anything `Do` has no option for. Its URL holds only the path and
the query; it goes through the same endpoints, failover, redirect policy and
tracing as `Do`, and its context is the request's own.

```go
req, err := http.NewRequestWithContext(ctx, http.MethodPut, "/files/report.csv", file)
if err != nil {
  // ...
}
req.Header.Set("Content-Type", "text/csv")

resp, err := api.DoRequest(req)
```

A body is replayed on another endpoint only when `req.GetBody` can produce it
again, which `http.NewRequestWithContext` sets up for a `*bytes.Buffer`,
`*bytes.Reader` or `*strings.Reader`. A request whose body cannot be replayed is
never failed over.

### Failover

Failing a request over means sending it twice, and an endpoint that answered a
`5xx` — or dropped the connection after the request was written — may already
have acted on it. `Failover` decides when that is acceptable:

- `FailoverIdempotent` (default) — after a transport error or a `5xx` when the
  method is idempotent (`GET`, `HEAD`, `OPTIONS`, `TRACE`, `PUT`, `DELETE`) or the
  request carries an `Idempotency-Key` or `X-Idempotency-Key` header; otherwise
  only when the request never reached the endpoint (the connection could not be
  established).
- `FailoverUnsent` — only when the request never reached the endpoint.
- `FailoverAlways` — after any transport error or `5xx`, whatever the method. Use
  it only when every endpoint deduplicates requests.

When every endpoint tried failed, the last `5xx` response is returned if there is
one, and the last transport error otherwise. A `429` or `503` carrying
`Retry-After` takes its endpoint out of the rotation for that long (capped at five
minutes, plus a little jitter), as long as another endpoint is left in it.

The client never waits on your behalf: failing over is immediate, and the
`Retry-After` of the response it returns is yours to act on. `Timeout` bounds each
attempt, so one call can take up to `len(Endpoints) × Timeout`; give the context a
deadline to bound a whole call, failover included.

### Redirects

A redirect is followed while it stays on the host of the endpoint it came from. A
redirect to another host is not followed unless `FollowRedirects` is set: the 3xx
response is returned as is, `Location` included. When it is set, the redirect is
followed without any of the request's headers except `Accept`,
`Accept-Language`, `Content-Language`, `Content-Type` and `User-Agent`, so the
credentials in `Headers` never reach a host they were not meant for. A redirect
from `https` to `http` is never followed.

### Errors

The error of a request that failed names the URL asked without its query string,
its fragment or the password of its credentials, so an API key passed as a query
parameter does not travel with it to a span or a log. The standard library also
quotes what an endpoint answered, which can repeat the request's query (re-encoded
or not), a signature an earlier redirect added, or a key in a path segment, which
nothing tells from a path, and none of it is kept:

- the `Location` header of a redirect that does not parse is left out whole: the
  error reads `failed to parse Location header: <the parser's reason>`, a string
  the reason quotes kept only when it is a character or an escape;
- a response net/http refuses to read, its status line, a header line, a
  `Content-Length` or `Transfer-Encoding` value or, over HTTP/2, a header's name
  malformed, is cut where the quote begins, and reads as the sentence naming what
  was refused (`… malformed MIME header line`, `… bad Content-Length`).

The URL of the request itself keeps its path, which is the caller's. The error
stays a `*url.Error`, and `errors.Is` and `errors.As` still tell what the failure
was.

### Calling an API outside your services

By default the client propagates the Service's trace context **and baggage**. The
baggage carries the six `Event` fields helix propagates — `event.id`,
`event.name`, `event.organization_id`, `event.tenant_id`, `event.app.name` and
`event.app.version` — along with any baggage an upstream service sent. That is what
a request crossing your own services needs, and more than a third party should
receive:

```go
api, err := httpclient.Connect(ctx, svc, httpclient.Config{
  Name:      "third-party",
  Endpoints: []string{"https://api.tld"},

  // The trace context alone. Use propagation.NewCompositeTextMapPropagator() to
  // send nothing at all.
  Propagator: propagation.TraceContext{},
})
```

Leave health probing off for such an API: see below.

### Health checks

Probing is **opt-in**. The client's health is part of the Service's readiness, so
probing an API outside your own services makes every replica of yours unready
whenever that API is down — taking your service down with it. With neither
`HealthPath` nor `HealthCheck` set, the client is always healthy and sends
nothing.

With `HealthPath` set, each endpoint is probed with `GET {endpoint}{HealthPath}`,
with the default `Headers`. An endpoint is healthy as long as it is **reachable and
responds below 500** — so an API with no health endpoint (a `404`) is still
healthy, while a connection failure or a `5xx` marks it down. A failing endpoint is
reported as one `SERVICE_UNAVAILABLE` entry whose path is its index in
`Endpoints`; the reason goes to the log and the readiness span, never to the
`/ready` response.

Provide a custom `HealthCheck` for stricter checks, e.g. requiring a `2xx`. The
error it returns is reported as unhealthy the same way:

```go
api, err := httpclient.Connect(ctx, svc, httpclient.Config{
  Name:      "search",
  Endpoints: []string{"https://search.internal"},
  HealthCheck: func(ctx context.Context, client httpclient.HTTPClient) error {
    resp, err := client.Get(ctx, "/ping")
    if err != nil {
      return err
    }
    defer resp.Body.Close()

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
      return fmt.Errorf("unexpected status %d", resp.StatusCode)
    }

    return nil
  },
})
```

Health probes — the default ones and the requests a `HealthCheck` sends through
the client it receives — are neither traced nor measured, and propagate nothing:
a readiness probe polled every few seconds is not traffic.

### Upstreams the Service can run without

An upstream that serves part of the Service's work, and that the Service can keep
running without, should not gate readiness: when it is down, every replica would
turn unready, and a rollout whose new replicas must turn ready first would stall,
though each could serve everything else. Mark such a client `Optional`:

```go
routing, err := httpclient.Connect(ctx, svc, httpclient.Config{
  Name:       "routing",
  Endpoints:  []string{"https://routing-1.internal", "https://routing-2.internal"},
  HealthPath: "/health",
  Optional:   true,
})
```

Its health check then answers healthy at once, without a probe, so an upstream
that hangs cannot hold the readiness probe either. Its requests fail as they fail
against any upstream that is down.

Whether the upstream answers is reported instead on the `httpclient.reachable`
gauge, which a background probe keeps current every 15 seconds, each probe
bounded to 5 seconds, until the client is closed. The first probe comes 15
seconds after `Connect`, not at it: a client a short-lived process connects and
closes probes nothing, and replicas started together do not all probe at once.
The probe is the health check the client would otherwise run: per endpoint with
`HealthPath`, reported under `httpclient.endpoint`, or for the client as a whole
with a `HealthCheck`. Without either, nothing is probed and nothing is reported.

## Telemetry

### Spans

Each request opens two kinds of span:

- `HTTP Client: <METHOD>` (internal) — the logical request, across every endpoint
  it was sent to. A request ending in a `5xx` is recorded as an error.
- `HTTP Client: <METHOD> / Attempt` (client) — one per attempt, the call that
  actually left the process, and the span the trace context is propagated from.

A method outside the HTTP specification is named `_OTHER`.

### Attributes

On `HTTP Client: <METHOD>`:
- `httpclient.name`
- `httpclient.attempts` — how many endpoints were tried
- `httpclient.endpoint` — `<scheme>://<host>` of the endpoint the result came from
- `httpclient.status_code` — the final status, when one was received

On `HTTP Client: <METHOD> / Attempt`:
- `httpclient.name`
- `httpclient.method`
- `httpclient.endpoint`
- `httpclient.path` — without the query string
- `httpclient.status_code`
- `httpclient.request.size`
- `httpclient.response.size`

Example:
```
httpclient.name: "upstream"
httpclient.method: "POST"
httpclient.endpoint: "https://api-1.tld"
httpclient.path: "/v1/items"
httpclient.status_code: 201
```

### Metrics

- `httpclient.operation.duration` (`s`) — duration of each logical request, by
  `httpclient.name`, `httpclient.operation` (the method) and `httpclient.outcome`
  (`ok` or `error`; a final `5xx` is an `error`).
- `httpclient.request.duration` (`s`) — duration of each attempt, by
  `httpclient.name`, `httpclient.method`, `httpclient.endpoint` and
  `httpclient.status_code`.
- `httpclient.request.size` (`By`) — size of each request body, by the same
  attributes.
- `httpclient.response.size` (`By`) — size of each response body, from
  `Content-Length`, by the same attributes.
- `httpclient.reachable` (`1`) — for an `Optional` client, whether the upstream
  answered its last background probe: `1` when it did, `0` when it did not, by
  `httpclient.name`, and `httpclient.endpoint` when the client probes `HealthPath`.
  Nothing is reported before the first probe ends, 15 seconds after `Connect`, nor
  once the client is closed.
