# helix.go - REST API integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/rest)
[![Latest version](https://img.shields.io/github/v/tag/mountayaapp/helix.go?filter=v*&sort=semver&label=version)](https://github.com/mountayaapp/helix.go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The REST API integration provides an opinionated way to build an HTTP REST API
with support for OpenAPI validation. It is a **server** integration: calling
`rest.New(ctx, svc, cfg)` registers the server with the `Service`, which starts
it with `svc.Run` and drains it on shutdown. A `Service` can run several servers
— a public API and an internal one, say — as long as each has its own `Name`.

What `rest.New` returns registers routes, and exposes the handler it serves for
tests. It carries no lifecycle: only the `Service` starts and stops the server.

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/rest
```

## About bunrouter

[`bunrouter`](https://github.com/uptrace/bunrouter) is a fast, flexible HTTP
router for Go that keeps the standard `net/http` handler signature. This
integration builds on it for routing and path parameters, while owning the HTTP
server lifecycle, health endpoints, and observability. The router stays an
implementation detail — routes are registered through the value returned by
`rest.New`, not against bunrouter directly, so the underlying router can be
swapped without changing consumer code.

Refer to the [bunrouter documentation](https://bunrouter.uptrace.dev/) for the
underlying routing semantics.

## Configuration

- `Name` (`string`) — Identifies the server in health output, error context, and
  the `rest.name` attribute of its spans and metrics. Default: `"rest"`.
- `Address` (`string`) — HTTP address to listen on. Default: `":8080"`.
- `PublicEndpoint` (`bool`) — Marks a server reachable from outside your trust
  boundary. See [Trust boundary](#trust-boundary). Default: `false`.
- `Readiness` (`func(*http.Request) int`) — Replaces the readiness probe's check
  of `GET /ready`, returning the status code to answer with. Default: checks the
  health of every integration attached to the `Service`.
- `Middleware` (`func(http.Handler) http.Handler`) — Wraps the routes with the
  application's own middleware: authentication, rate limiting. It runs inside
  the request's span, so a response it writes itself is traced. It never wraps
  the probes, nor a request for a [non-canonical path](#non-canonical-paths).
- `EdgeMiddleware` (`func(http.Handler) http.Handler`) — Wraps every response
  but the probes', including the ones answered before `Middleware` runs: CORS,
  and the headers every response carries. See [Middleware](#middleware).
- `CrossOriginProtection` (`*http.CrossOriginProtection`) — Rejects non-safe
  cross-origin browser requests with a `403`, before `Middleware` runs. See
  [Cross-site request forgery](#cross-site-request-forgery). Default: `nil`.
- `OpenAPI` (`ConfigOpenAPI`) — OpenAPI validation settings. See
  [OpenAPI validation](#openapi-validation).
- `NoImplicitHead` (`bool`) — Stops a `GET` route from answering `HEAD` on its
  own. See [Routes](#routes). Default: `false`.
- `RecordRouteParams` (`bool`) — Records the value of every matched path
  parameter on the request's span, as `rest.route.param.<name>`. Off by default:
  a path routinely carries an email address, a token or a customer identifier.
  Default: `false`.
- `MaxRequestBodyBytes` (`int64`) — Bounds the size of a request body, for every
  route and for `Middleware`. See [Request body limit](#request-body-limit).
  Default: `10 MiB` (`rest.DefaultMaxRequestBodyBytes`).
- `MaxHeaderBytes` (`int`) — Bounds the size of the request line and headers.
  Default: `0`, which is net/http's own `1 MiB`.
- `RequestTimeout` (`time.Duration`) — Bounds how long any route may spend
  handling a request: the budget rides the request context. A route overrides it
  with `rest.WithTimeout`, or waives it with `rest.WithoutTimeout`. Default: `0`
  (no bound).
- `ReadHeaderTimeout` (`time.Duration`) — Bounds how long a client may take to
  send its request headers. Default: `10s`.
- `ReadTimeout` (`time.Duration`) — Bounds how long a client may take to send its
  whole request, body included. Default: `0` (no bound).
- `WriteTimeout` (`time.Duration`) — An absolute deadline on writing each
  response. It severs every long-lived response, server-sent events above all:
  leave it at `0` unless no route streams, and bound handlers with
  `RequestTimeout` instead. Default: `0` (no bound).
- `IdleTimeout` (`time.Duration`) — Bounds how long a keep-alive connection may
  sit idle between requests. Default: `120s`.
- `TLS` (`integration.ConfigTLS`) — TLS settings. When `Enabled`, `CertPEM` and
  `KeyPEM` are required, and `New` fails without them rather than serving
  plaintext. Setting `ClientCAPEMs` turns on mutual TLS. TLS 1.2 is the minimum.

### OpenAPI

- `Enabled` (`bool`) — Enables request/response validation against the OpenAPI
  descriptions. Default: `false`.
- `Descriptions` (`[]string`) — Paths to local files or URLs of the OpenAPI
  descriptions to validate against. **Required** when enabled. Several are merged
  into one document; see [OpenAPI validation](#openapi-validation).
- `RefuseUndeclaredQuery` (`bool`) — Refuses a query parameter the operation
  does not declare with a `400`. Default: `false`, where such a parameter is not
  looked at; see [OpenAPI validation](#openapi-validation).

## Usage

### Creating a server

```go
package main

import (
  "context"
  "net/http"
  "os"
  "os/signal"
  "syscall"

  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/rest"
)

func main() {
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

  svc, err := service.New(ctx, service.Config{})
  if err != nil {
    panic(err)
  }

  api, err := rest.New(ctx, svc, rest.Config{
    Address: ":8080",
  })
  if err != nil {
    panic(err)
  }

  api.GET("/hello", func(rw http.ResponseWriter, req *http.Request) {
    rest.NewResponseSuccess[rest.NoMetadata, rest.NoData](req).Write(rw)
  })

  // Run blocks until SIGINT/SIGTERM, then drains the server.
  if err := svc.Run(ctx); err != nil {
    panic(err)
  }
}
```

### Routes

`GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE` and `OPTIONS` register a handler
function for their method. `Handle` takes the method as a value and an
`http.Handler`. `Group` registers routes under a prefix, and groups nest:

```go
api.Handle(http.MethodGet, "/assets/*path", http.StripPrefix("/assets", http.FileServerFS(assets)))

v1 := api.Group("/v1")
v1.POST("/users", createUser)  // POST /v1/users
v1.GET("/users/:id", getUser)  // GET /v1/users/:id
```

A `GET` route answers `HEAD` too, with the same handler, options and validation:
net/http sends the headers the handler writes and drops its body. A route that
must answer `HEAD` differently registers its `HEAD` handler before the `GET` of
the same path, which then implies none; `Config.NoImplicitHead` turns the
behavior off. Registering the same route twice, a `HEAD` after the `GET` that
already implied it, a malformed path, or a method other than these seven panics,
the first time the program starts.

A request for a method the path does not take is answered `405`, with an `Allow`
header listing the methods it does take:

```http
HTTP/1.1 405 Method Not Allowed
Allow: GET, HEAD, POST
```

#### Non-canonical paths

A path that names a route only once it is cleaned up (`/things/./42`), or once a
trailing slash is dropped or added (`/things/42/`), is answered before
`Middleware` runs, so no credential or other work is spent on it, and inside
`EdgeMiddleware`, so the answer carries its CORS headers:

- a `GET` or a `HEAD` with `308 Permanent Redirect` to the route's own path, the
  query kept;
- any other method with the `404` error envelope. A `301`, which the underlying
  router would answer, is followed by sending a `GET`, and would turn a write into
  a read of the same path.

A path a route serves as written, a wildcard route's above all, is left alone.

### Route options

Every route takes `RouteOption`s, and `Group` applies its own to each of its
routes before the route's:

```go
api, err := rest.New(ctx, svc, rest.Config{RequestTimeout: 30 * time.Second})
if err != nil {
  return err
}

api.POST("/reports", generateReport, rest.WithTimeout(2*time.Minute))

stream := api.Group("/stream", rest.WithoutTimeout())
stream.GET("/events", events) // unbounded: a stream is long-lived by design
```

`rest.WithRedactedPath()` keeps a route's concrete paths out of its telemetry, for
a path that carries a credential or a one-time token: the span records the route
template as `rest.path`, and no `rest.route.param.<name>`, even with
`RecordRouteParams`. It holds for every request matched to the route's path,
whatever its method, and from the moment the route matches, before validation:

```go
api.GET("/downloads/:token", download, rest.WithRedactedPath())
```

`rest.WithPreValidation(mw...)` runs middlewares once the route has matched and
before its request is validated against the description, in the order given and
a group's before the route's. Authentication belongs there: a request it refuses
is answered before anything has judged its parameters or read its body, so a
caller who may not call the route learns nothing about what it accepts. The
middlewares run under the route's budget, `rest.ParamFromContext` answers in
them, and a response they write is validated like the handler's:

```go
api.POST("/things", createThing, rest.WithPreValidation(authenticate))

admin := api.Group("/admin", rest.WithPreValidation(authenticate, requireAdmin))
```

### Path parameters

Use `:param` for one segment and `*param` for the rest of the path. Read one
value with `rest.ParamFromContext`, or all of them with `rest.ParamsFromContext`,
which builds a new map on each call:

```go
api.GET("/users/:id", func(rw http.ResponseWriter, req *http.Request) {
  id, _ := rest.ParamFromContext(req.Context(), "id")

  // ...
  _ = id
})
```

### Middleware

`Middleware` wraps every route. It is where authentication and rate limiting
belong, and where an `event.Event` describing the request is put in its context:
the integration stamps it on the request's span once the middleware hands the
request on.

```go
api, err := rest.New(ctx, svc, rest.Config{
  Middleware: func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
      if req.Header.Get("Authorization") == "" {
        rest.NewResponseError[rest.NoMetadata](req).
          SetStatus(http.StatusUnauthorized).
          Write(rw)
        return
      }

      next.ServeHTTP(rw, req)
    })
  },
})
```

`EdgeMiddleware` wraps `Middleware` and everything this integration answers
before it: the `403` of `CrossOriginProtection`, the `413` of
`MaxRequestBodyBytes`, and the `308` or `404` of a
[non-canonical path](#non-canonical-paths). It is where CORS belongs, so a
browser lets its script read every response, a refusal included, and where the
headers every response carries are set. Only the probes bypass it.

```go
api, err := rest.New(ctx, svc, rest.Config{
  EdgeMiddleware: func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
      rw.Header().Set("X-Content-Type-Options", "nosniff")

      next.ServeHTTP(rw, req)
    })
  },
  Middleware: authenticate,
})
```

### Success responses

`ResponseSuccess[Metadata, Data]` is a generic type for `2xx` responses. The JSON
body follows the GraphQL-spec envelope shape `{"data": {...}, "extensions": {...}}`
(no `status` field — the HTTP status code carries that). Typed metadata is
folded under top-level `extensions.metadata` for symmetry with the error
envelope. The status defaults to `200`.

```go
type Pagination struct {
  Page  int `json:"page"`
  Total int `json:"total"`
}

type User struct {
  ID   string `json:"id"`
  Name string `json:"name"`
}

api.GET("/users/:id", func(rw http.ResponseWriter, req *http.Request) {
  rest.NewResponseSuccess[Pagination, User](req).
    SetMetadata(Pagination{Page: 1, Total: 42}).
    SetData(User{ID: "usr_123", Name: "Alice"}).
    Write(rw)
})
```

Response JSON:
```json
{
  "data": {
    "id": "usr_123",
    "name": "Alice"
  },
  "extensions": {
    "metadata": {
      "page": 1,
      "total": 42
    }
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
{"data":null}
```

The `data` field is always present on 2xx responses — `null` when no payload
is set, an object/array otherwise — so consumers can rely on its presence.

Responses are marshaled with `encoding/json/v2`, whose `omitempty` drops a
member whose encoding is empty JSON: `null`, `""`, `[]` or `{}`. A member whose
explicit `null` must reach the client, such as an optional-and-nullable wrapper,
is tagged `omitzero` instead, which leaves out only the zero value (a nil
wrapper) and writes a `null` as `null`.

A payload that cannot be marshaled (a `NaN`, invalid UTF-8 in a string, a value
whose own `MarshalJSON` fails) is answered as a `500` with the error envelope
instead, `INTERNAL_ERROR`, and the marshal error is recorded on the request's
span.

### Panics

A panic in a handler, in a route's middlewares or in `Middleware` is answered
with a `500` error envelope, `INTERNAL_ERROR`, rather than a dropped connection.
The answer is written just inside `EdgeMiddleware`, so it carries the headers
`EdgeMiddleware` set before handing the request on (a request id, CORS), and
none of the ones set further in for the answer that never completed. The panic
and its stack are logged against the request's context and recorded on its span.

A panic once the response has started (a flushed stream, a partly written body,
a hijacked connection) cannot be answered any more: it is logged and recorded the
same way, and the response is aborted with `http.ErrAbortHandler`. A handler that
panics with `http.ErrAbortHandler` itself aborts as it always did.

### Testing

`Handler` returns the whole handler the server serves — probes, routes, OpenAPI
validation, `Middleware` and telemetry — so a test can drive the API with
`net/http/httptest`, without a listener:

```go
rw := httptest.NewRecorder()
api.Handler().ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/hello", nil))
```

## Error responses

REST error responses follow the [GraphQL spec error envelope](https://spec.graphql.org/draft/#sec-Errors):

```json
{
  "errors": [
    {"message": "…", "path": [...], "extensions": {"code": "…"}}
  ],
  "extensions": {"metadata": {...}}
}
```

`ResponseError[Metadata]` is the typed builder, for `4xx` and `5xx` statuses — a
`3xx` is not an error: answer it with `http.Redirect`. The status defaults to
`500`. `SetStatus` seeds a single fallback entry built from the localized message
(selected via the `lang` cookie or the `Accept-Language` header) and the
canonical machine code from `errorstack.HTTPStatusToCode`. A status outside the
ones `rest.AddOrEditLanguage` lists gets net/http's English text under
`INTERNAL_ERROR`.

```go
api.GET("/users/:id", func(rw http.ResponseWriter, req *http.Request) {
  rest.NewResponseError[rest.NoMetadata](req).
    SetStatus(http.StatusNotFound).
    Write(rw)
})
```

Response JSON:
```json
{
  "errors": [
    {
      "message": "Resource does not exist",
      "extensions": {"code": "NOT_FOUND"}
    }
  ]
}
```

`SetValidations` replaces `errors[]` with one entry per validation. Each entry
is forced to `extensions.code = "VALIDATION_FAILED"`:

```go
rest.NewResponseError[rest.NoMetadata](req).
  SetStatus(http.StatusBadRequest).
  SetValidations(
    errorstack.Entry{
      Message: "Must be a valid email address",
      Path:    []any{"request", "body", "email"},
    },
    errorstack.Entry{
      Message: "Must be set",
      Path:    []any{"request", "body", "name"},
    },
  ).
  Write(rw)
```

Response JSON:
```json
{
  "errors": [
    {
      "message": "Must be a valid email address",
      "path": ["request", "body", "email"],
      "extensions": {"code": "VALIDATION_FAILED"}
    },
    {
      "message": "Must be set",
      "path": ["request", "body", "name"],
      "extensions": {"code": "VALIDATION_FAILED"}
    }
  ]
}
```

`SetEntries` replaces `errors[]` with entries that keep their own message and
code; an entry without a code gets the status' own. Call `SetStatus` first:

```go
rest.NewResponseError[rest.NoMetadata](req).
  SetStatus(http.StatusConflict).
  SetEntries(errorstack.Entry{
    Message: "Email address is already registered",
    Path:    []any{"request", "body", "email"},
  }).
  Write(rw)
```

Response JSON:
```json
{
  "errors": [
    {
      "message": "Email address is already registered",
      "path": ["request", "body", "email"],
      "extensions": {"code": "CONFLICT"}
    }
  ]
}
```

`SetMetadata` folds typed metadata under top-level `extensions.metadata`.

## OpenAPI validation

```go
api, err := rest.New(ctx, svc, rest.Config{
  Address: ":8080",
  OpenAPI: rest.ConfigOpenAPI{
    Enabled:      true,
    Descriptions: []string{"./descriptions/openapi.yaml"},
  },
})
```

`New` loads the descriptions — `ctx` bounds the loading of one served over the
network — merges them, and refuses one that is not a valid OpenAPI description.

A description may `$ref` helix's own documents (the envelopes, response
components and headers under `documents/openapi`) by their raw URL at a tag, a
branch or a commit:

```yaml
$ref: https://raw.githubusercontent.com/mountayaapp/helix.go/refs/tags/<tag>/documents/openapi/components/responses/500.yaml
```

Those are never fetched: they are read from the copy embedded in the module the
server is built with (the `documents` package), whatever tag the reference
names. A server therefore starts without network access, and validates its
responses against the envelope its own version of helix writes. A reference to a
document that version does not ship is refused at load. Any other remote
reference is fetched over the network.
The first is the base: it carries the document's info, servers and security, and
the others contribute their paths and components. Two descriptions may each
declare a different method on the same path; two declaring the same operation, or
the same component name, is refused.

- **Order**: once a route has matched, its response validation wraps everything
  it runs, then its `rest.WithPreValidation` middlewares run, then its request is
  validated, then its handler runs.
- **Requests** that do not match the description are refused with `400`, one
  `VALIDATION_FAILED` entry per non-conforming value, and never reach their
  handler. Each entry's `path` locates the value and its `message` says what is
  wrong with it: in an OpenAPI 3.1 description, validated as JSON Schema 2020-12,
  that is the validator's own message for the value (`minLength: got 2, want 3`),
  never its report's header or the schema's URL. A `oneOf` or an `anyOf` that
  matched no branch is one entry at its value (`'oneOf' failed, none matched`)
  rather than every branch's failures, a problem reported twice at one path is
  one entry, the names a message lists are sorted, and the entries come in the
  order of their paths, so a request is answered the same way every time. A
  body over `MaxRequestBodyBytes` is refused with `413`. Security schemes are
  not checked: authentication is the business of `Middleware` or of a route's
  `rest.WithPreValidation` middlewares. A `HEAD` no description declares is
  validated as the `GET` it mirrors.
- **A refusal is bounded**, whatever the request holds, so its size is set by
  these limits and never by the client, and the limits sit above what an ordinary
  refusal holds, so one reads exactly as it always did. A name or a value the
  request supplied (a member, a part, a query parameter, a value that does not
  parse, the string a `pattern` refuses, a `Content-Type`, a part's own media
  type) is quoted whole up to 4 KiB: a browser's multipart `Content-Type` is 68 to
  90 bytes and RFC 2046 allows 100, and a URL is kept under 2 KiB, a description
  commonly bounding one at 2,048 bytes. A message is at most 8 KiB, a path at
  most 32 segments, the first ones, and one message lists at most 16 names. The
  entries together weigh at most 64 KiB, weighed as the envelope writes them: the
  first ones are listed, in order, and one more entry, with no path, says how many
  are not (`465 more problems are not listed`), so a client mapping entries to
  fields finds every listed entry where it always was and that one under no field.
  A first entry heavier than that on its own is still listed, cut until it fits:
  its path to its first segments, its message to its beginning. What is cut shows
  it: a quoted value or a message cut on a character ends with `…`, a path cut
  ends with a `…` segment, and a list of names ends with `and 3 more`. Every string is valid UTF-8: bytes that are not are written as
  U+FFFD, so a name spelled in other bytes is refused with `400` like any other.
  A body that cannot be read or decoded (JSON that does not parse, a malformed
  multipart part, an escape that does not decode) is one entry at
  `["request", "body"]` saying `Cannot be decoded as its content type`: the
  parser's own error, which quotes the bytes it stopped on, is recorded on the
  `REST: OpenAPI / Request Validation` span and not sent.
- **Path parameters** are validated percent-decoded, as OpenAPI defines a
  simple-style path parameter: `/files/a%20b` holds the value `a b`, which a
  `pattern` admitting a space admits, and `a%2Fb` holds `a/b`, which one refusing
  `/` refuses. A segment that does not decode, which only a template with a
  literal after a parameter in the same segment can produce, is refused with one
  entry at `["request", "params", name]`, and the rest of the request is not
  validated.
- **Undeclared query parameters**, with `RefuseUndeclaredQuery` set, are refused
  in the same `400`, one entry at `["request", "query", name]` each, ahead of the
  other entries but after a path parameter that does not decode. An operation declares a query parameter through its own
  parameters, its path item's, or the name of an `apiKey` security scheme carried
  `in: query` that one of its security requirements names. A route admits a
  parameter its description has no place for, one a `rest.WithPreValidation`
  middleware reads, with `rest.WithUndeclaredQuery(names...)`:

  ```go
  api.GET("/files/:id", download, rest.WithPreValidation(authenticate), rest.WithUndeclaredQuery("token"))
  ```
- **Responses** that do not match are still sent to the client, and the failure
  is recorded as an error on the `REST: OpenAPI / Response Validation` span. A
  response a `rest.WithPreValidation` middleware writes is validated like the
  handler's; a `400` from the request validation itself is not. A response to
  `HEAD` is not validated. A body is kept for validation only when the description declares a schema for its
  status and media type, and only up to 1 MiB; a larger one, a streamed one
  (`text/event-stream`, or flushed before its body is complete), and a hijacked
  connection are not validated. A response that declared its `Content-Length` and
  wrote all of it is complete: flushing it, to send it before work the handler
  does on its way out, leaves it validated.
- **Empty schemas** (`{}`) admit `null` in an OpenAPI 3.1 description, as JSON
  Schema 2020-12 defines them: an envelope declaring `data: {}`, narrowed to
  `type: 'null'` by an operation with no payload, validates `{"data":null}`.
  The validator reads a schema using no 2020-12 keyword with OpenAPI 3.0's rules,
  which refuse that `null`, so every empty schema a JSON body reaches is spelled
  as the list of every JSON type, which means the same and is read the same way
  by both. A parameter's or a header's schema is left as written, and so is
  every schema of an OpenAPI 3.0 description, where `null` needs `nullable`.
- **Routes no description declares** are served unvalidated. Their span carries
  `rest.openapi.validated=false`, which is how to find them; so does building the
  router with `rest.NewOpenAPIRouter` in a test and asking it.
- **Servers**: the host of every server URL is ignored, so a description naming
  its production host still validates a server running anywhere else. Its path is
  kept: with a server `https://api.example.com/v1`, the path `/things` describes
  requests to `/v1/things`. A service behind a proxy that strips that prefix
  should list a server for the path it actually sees, such as `/`.

The probes are never validated.

## Request body limit

Every request body is bounded by `MaxRequestBodyBytes` (10 MiB by default). A
request declaring a larger `Content-Length` is answered `413` before anything
reads it. A body sent without one is cut off at the limit: reading past it fails
with an `*http.MaxBytesError`, which the OpenAPI validation answers with `413`,
and which a handler reading the body itself should answer the same way:

```go
body, err := io.ReadAll(req.Body)
if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
  rest.NewResponseError[rest.NoMetadata](req).
    SetStatus(http.StatusRequestEntityTooLarge).
    Write(rw)
  return
}
```

## Cross-site request forgery

An API that authenticates browsers with cookies accepts forged form posts unless
it checks where they came from. Set `CrossOriginProtection` to a
`*http.CrossOriginProtection`, configured with its trusted origins and bypass
patterns as usual: a non-safe cross-origin browser request is answered `403` with
the error envelope, before `Middleware` runs. An API authenticating with a header
a browser does not send on its own, such as `Authorization`, does not need it.

```go
api, err := rest.New(ctx, svc, rest.Config{
  CrossOriginProtection: http.NewCrossOriginProtection(),
})
```

## Trust boundary

By default the server continues the trace its caller sent, and reads the
caller's baggage — which is what makes a request crossing several helix services
one trace. A server reachable from the internet should not trust what its caller
sends: set `PublicEndpoint: true`, and every request starts a trace of its own,
linked to the caller's so the two remain navigable, and inbound baggage is
ignored, so that nothing a client puts in its headers is propagated onward as
though a trusted upstream service had set it.

## Telemetry

### Spans

Every request opens one server span, around the whole handler chain — before
`EdgeMiddleware` and `Middleware`, so a response either writes itself (a `401`, a
`429`, a CORS preflight) is traced like any other.

- `REST: <METHOD> <route>` — the request, once the router matched a route:
  `REST: GET /users/:id`, never the concrete path.
- `REST: <METHOD>` — a request the router never matched: a `404`, the answer to a
  non-canonical path, or a response `EdgeMiddleware` or `Middleware` wrote
  itself.
- `REST: OpenAPI / Request Validation` (internal) — the validation of a request
  a description declares.
- `REST: OpenAPI / Response Validation` (internal) — the validation of its
  response.

A `5xx` response, or a panicking handler, sets the span's status to error, and a
panic is recorded on the span as an exception. A `4xx` leaves it unset: it is the
client's error.

### Attributes

The server span carries:

- `rest.name` — the server's `Name`.
- `rest.method` — the request method; one outside the HTTP specification is
  recorded as `_OTHER`.
- `rest.path` — the concrete request path, or the route template on a route
  registered with `rest.WithRedactedPath()`.
- `rest.route` — the matched route template, when there is one.
- `rest.status_code` — the response's status code.
- `rest.request.size` — the request body size, when the request declares it.
- `rest.response.size` — the response body size.
- `rest.openapi.validated` — whether a description declares the request's
  operation, when OpenAPI is enabled.
- `rest.route.param.<name>` — the value of each path parameter, only with
  `RecordRouteParams`, and never on a route registered with
  `rest.WithRedactedPath()`.
- The attributes of the `event.Event` in the request's context, when there is
  one.

The OpenAPI validation spans carry `rest.name`.

Example:
```
rest.name: "rest"
rest.method: "GET"
rest.path: "/users/usr_123"
rest.route: "/users/:id"
rest.status_code: 200
rest.request.size: 0
rest.response.size: 48
```

### Metrics

- `rest.request.duration` (`s`) — the duration of each request, by `rest.name`,
  `rest.method`, `rest.status_code` and `rest.route`.
- `rest.request.size` (`By`) — the size of request bodies, by the same.
- `rest.response.size` (`By`) — the size of response bodies, by the same.
- `rest.operation.duration` (`s`) — the duration of each OpenAPI validation, by
  `rest.name`, `rest.operation` (`OpenAPI / Request Validation`,
  `OpenAPI / Response Validation`) and `rest.outcome` (`ok`, `error`).

## Health probes

The `rest` integration exposes two health probe endpoints following Kubernetes
conventions. Both bypass `EdgeMiddleware`, `Middleware`, `CrossOriginProtection`
and the OpenAPI validation, so they are never blocked by authentication or other
service-level middleware.

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

Checks the health of every server and dependency attached to the `Service`.
Concurrent probes share one check. When all of them are healthy, the response
is:

```json
{"data":null}
```

When at least one is not, the response is a generic `503`:

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

The probe is answered to whoever asks, and the reason a dependency is unhealthy
— a driver error — routinely names hosts, users and databases. Which integration
failed, and why, is written to the log and recorded on the probe's span instead.

Pass a custom `Readiness` function in the config to override this behavior.
