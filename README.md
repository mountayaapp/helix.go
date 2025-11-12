# helix.go

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go)](https://goreportcard.com/report/github.com/mountayaapp/helix.go)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

helix is a Go library designed to simplify the development of cloud-native,
consistent, reliable, and high-performance microservices. It provides a lightweight
abstraction layer that streamlines complex back-end development, automating
critical tasks such as logging, tracing, observability, error recording, and event
propagation across services and integrations.

This allows developers to focus solely on business logic. This decoupling of
concerns not only ensures that business logic remains separate from infrastructure
code but also improves both developer productivity and the maintainability of
large-scale systems, making the development process more modular and scalable.

At [Mountaya](https://mountaya.com/), we rely entirely on helix for our backend
observability and reliability, showcasing its capability to support real-world,
high-performance systems. Below is an actual screenshot demonstrating its power
in action. The trace holds an `event.Event` object from end-to-end, across all
spans/services/integrations. It begins in our HTTP Internal API (shown in blue)
via the REST router integration and flows through to one of our Temporal workers
(shown in pink). Importantly, all observability logic is abstracted seamlessly by
helix, so developers didn't need to write code outside of the business logic —
simplifying maintenance and reducing complexity in our codebase.

![End-to-end observability with helix](./assets/screenshot.png)

## Features and benefits

- **Automatic distributed tracing and error recording:** All integrations within
  helix automatically implement distributed tracing and error recording, adhering
  to [OpenTelemetry](https://opentelemetry.io/) standards. This ensures that
  developers can take full advantage of the integrations without needing to
  configure or modify application code. Traces and error data are captured
  transparently, giving you real-time visibility into your services' health and
  performance with zero added effort.

- **Consistent event propagation:** At the heart of helix is the `event.Event`
  object, which is passed seamlessly between services via distributed tracing. This
  object provides full end-to-end observability, enabling you to trace events
  across your entire service mesh with minimal setup. Whether an event is handled
  by one service or propagated across multiple, all its context is preserved and
  easily accessible for debugging, analysis, monitoring, and even usage tracking.

- **Consistent error handling:** helix ensures a uniform approach to error handling
  and recording across the core library and all integrations. By using consistent
  schemas and behaviors, error handling remains predictable and easy to manage
  across your entire stack, reducing complexity and increasing system reliability.

- **OpenAPI support:** helix supports the use of [OpenAPI](https://www.openapis.org/)
  specification to validate HTTP requests and responses against an OpenAPI
  description, ensuring that they match expected formats and types.

- **Type-safety everywhere:** No more empty `interface{}`. By leveraging Go's
  strong type system and generics, the library ensures type safety at every layer
  of the application, reducing runtime errors and improving code clarity. This
  means fewer bugs, easier maintenance, and a more robust developer experience.

## Environment variables

helix relies on the following environment variables:

- `ENVIRONMENT` represents the environment the service is currently running in.
  When value is one of `local`, `localhost`, `dev`, `development`, the logger
  handles logs at `debug` level and higher. Otherwise, the logger handles logs at
  `info` level and higher.
- `OTEL_SDK_DISABLED` can be set to `true` to disable sending OpenTelemetry logs
  and traces to the OTLP endpoint. Defaults to `false`.
- `OTEL_EXPORTER_OTLP_ENDPOINT` is the OTLP gRPC endpoint to send logs and traces
  to. Example: `localhost:4317`.

## Quick start example

This example demonstrates the full service lifecycle: initialization of the REST
router integration, and the idiomatic start/stop pattern for long-running services.

```go
import (
  "net/http"

  "github.com/mountayaapp/helix.go/integration/rest"
  "github.com/mountayaapp/helix.go/service"
)

func main() {

  // Configure and create a new REST router.
  cfg := rest.Config{
    Address: ":8080",
  }

  router, err := rest.New(cfg)
  if err != nil {
    return err
  }

  // Register a new route, including some parameters.
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
    
    // Write consistent HTTP responses using the integration's helpers.
    rest.NewResponseSuccess[types.CustomMetadata, rest.NoData](req).
      SetStatus(http.StatusAccepted).
      SetMetadata(metadata).
      Write(rw)
  })

  // Start the service. It handles all integrations start logic (when applicable)
  // and runs until an interrupt signal.
  if err := service.Start(); err != nil {
    panic(err)
  }

  // Stop the service gracefully, handling connection closure across all
  // integrations.
  if err := service.Stop(); err != nil {
    panic(err)
  }
}
```

## Examples

<details>
  <summary>Event propagation</summary>

  The `event.Event` object seamlessly carries context (like `UserId`) across
  service boundaries, automatically tied to the distributed trace.

  ```go
  import (
    "github.com/mountayaapp/helix.go/event"
    "github.com/mountayaapp/helix.go/integration/rest"
    "github.com/mountayaapp/helix.go/integration/temporal"
  )

  router.POST("/anything", func(rw http.ResponseWriter, req *http.Request) {
    var e = event.Event{
      // ...
    }

    // Attach the event to a context.
    ctx := event.ContextWithEvent(req.Context(), e)

    // The event is automatically propagated to the Temporal integration via ctx.
    wr, err := TemporalWorkflow.Execute(ctx, client, opts, payload)
    if err != nil {
      rest.NewResponseError[rest.NoMetadata](req).
        SetStatus(http.StatusServiceUnavailable).
        Write(rw)
      return
    }

    rest.NewResponseSuccess[rest.NoMetadata, rest.NoData](req).
      SetStatus(http.StatusAccepted).
      Write(rw)
  })
  ```
</details>

<details>
  <summary>Logging</summary>

  Logs are automatically enriched with the trace and `event.Event` from the context,
  ensuring immediate correlation between logs and traces across all services.

  ```go
  import (
    "github.com/mountayaapp/helix.go/event"
    "github.com/mountayaapp/helix.go/integration/rest"
    "github.com/mountayaapp/helix.go/telemetry/log"
  )

  router.POST("/anything", func(rw http.ResponseWriter, req *http.Request) {
    var e = event.Event{
      // ...
    }

    // Attach the event to a context.
    ctx := event.ContextWithEvent(req.Context(), e)
    
    // Log message is automatically tied to the current trace and event via ctx.
    log.Debug(ctx, "automatically tied to router's trace with event via ctx")

    rest.NewResponseSuccess[rest.NoMetadata, rest.NoData](req).
      SetStatus(http.StatusAccepted).
      Write(rw)
  })
  ```
</details>

<details>
  <summary>Custom tracing spans</summary>

  In addition to all automatic traces built in integrations, you can easily start
  new spans that are automatically children of the current trace, allowing for
  fine-grained performance analysis of internal logic.

  ```go
  import (
    "github.com/mountayaapp/helix.go/event"
    "github.com/mountayaapp/helix.go/integration/rest"
    "github.com/mountayaapp/helix.go/telemetry/log"
    "github.com/mountayaapp/helix.go/telemetry/trace"
  )

  router.POST("/anything", func(rw http.ResponseWriter, req *http.Request) {
    var e = event.Event{
      // ...
    }

    // Attach the event to a context.
    ctx := event.ContextWithEvent(req.Context(), e)
    
    // Start a new span, which is a child of the current HTTP request trace.
    ctx, span := trace.Start(ctx, trace.SpanKindClient, "span title")
    defer span.End()

    log.Debug(ctx, "log is tied to the router's trace and this custom span via ctx")

    rest.NewResponseSuccess[rest.NoMetadata, rest.NoData](req).
      SetStatus(http.StatusAccepted).
      Write(rw)
  })
  ```
</details>

<details>
  <summary>Error handling</summary>

  Use `errorstack` package to build structured, traceable errors that can
  accumulate validation failures and other context, improving debugging
  consistency.

  ```go
  stack := errorstack.New("Failed to initialize Stripe client", errorstack.WithIntegration("stripe"))
  stack.WithValidations(errorstack.Validation{
    Message: fmt.Sprintf("%s environment variable must be set and not be empty", envvar),
  })

  if stack.HasValidations() {
    return stack
  }
  ```
</details>

## Integrations

Each integration in helix is highly opinionated, designed to enforce modern best
practices for resilience and observability right out of the box. This approach
ensures developers immediately benefit from consistent error handling, structured
logging, and robust distributed tracing across every external dependency.

Supported integrations:

- [REST router](./integration/rest/README.md) for building REST APIs.
- [Temporal](./integration/temporal/README.md) for durable, fault-tolerant
  executions.
- [PostgreSQL](./integration/postgres/README.md) as transactional database.
- [ClickHouse](./integration/clickhouse/README.md) as analytical database.
- [Valkey](./integration/valkey/README.md) as key/value cache database.
- [Bucket](./integration/bucket/README.md) for standardized blob storage.

## Orchestrators and cloud providers

helix automatically detects the orchestrator or cloud provider your service is
running on. When a recognized orchestrator or cloud provider is identified, traces
and logs are automatically enriched with the corresponding attributes and fields.

<details>
  <summary>Kubernetes</summary>

  Additional trace attributes:

  - `kubernetes.namespace`
  - `kubernetes.pod`

  Additional log fields:

  - `kubernetes_namespace`
  - `kubernetes_pod`
</details>

<details>
  <summary>Nomad</summary>

  Additional trace attributes:

  - `nomad.datacenter`
  - `nomad.job`
  - `nomad.namespace`
  - `nomad.region`
  - `nomad.task`

  Additional log fields:

  - `nomad_datacenter`
  - `nomad_job`
  - `nomad_namespace`
  - `nomad_region`
  - `nomad_task`
</details>

<details>
  <summary>Render</summary>

  Additional trace attributes:

  - `render.instance_id`
  - `render.service_id`
  - `render.service_name`
  - `render.service_type`

  Additional log fields:

  - `render_instance_id`
  - `render_service_id`
  - `render_service_name`
  - `render_service_type`
</details>

## License

Repository licensed under the [MIT License](./LICENSE.md).
