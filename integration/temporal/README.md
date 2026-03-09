# helix.go - Temporal integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/temporal)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/temporal)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/temporal)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The Temporal integration provides an opinionated, type-safe way to interact with
[Temporal](https://temporal.io/) for durable, fault-tolerant workflow executions.

The integration has two constructors depending on the service's role:

- `temporal.Connect(svc, cfg)` creates a **client-only** connection and
  automatically registers it as a **dependency** via `service.Attach()`. Use this
  for services that need to start or schedule workflows without processing them.

- `temporal.New(svc, cfg)` creates a **worker** along with a client, and
  automatically registers the worker as a **server** via `service.Serve()`. Use
  this for worker services that process workflows and activities.

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/temporal
```

## Configuration

### ConfigClient

Used by both `temporal.Connect()` and embedded in `ConfigWorker`.

- `Address` (`string`) — Temporal server address. Default: `"127.0.0.1:7233"`.
- `Namespace` (`string`) — Temporal namespace. Default: `"default"`.
- `DataConverter` (`converter.DataConverter`) — Custom serialization/deserialization
  for workflow arguments.
- `TLS` (`integration.ConfigTLS`) — TLS settings.

### ConfigWorker

Used by `temporal.New()`.

- `Client` (`ConfigClient`) — Embedded client configuration.
- `TaskQueue` (`string`) — Task queue name for the worker. **Required**.
- `WorkerActivitiesPerSecond` (`float64`) — Max activities per second per worker.
  Default: `100000`.
- `TaskQueueActivitiesPerSecond` (`float64`) — Max activities per second for the
  entire task queue (server-managed). Default: `100000`.
- `EnableSessionWorker` (`bool`) — Enable Temporal session worker. Default: `false`.

## Usage

### Defining workflows and activities

```go
import (
  "time"

  "github.com/mountayaapp/helix.go/integration/temporal"
  "go.temporal.io/sdk/workflow"
)

type OrderInput struct {
  UserID  string
  ItemIDs []string
}

type OrderResult struct {
  OrderID string
  Total   float64
}

type ChargeInput struct {
  UserID string
  Amount float64
}

type ChargeResult struct {
  TransactionID string
}

var ProcessOrder = temporal.NewWorkflow[OrderInput, OrderResult]("process-order")

var ChargeUser = temporal.NewActivity[ChargeInput, ChargeResult](
  "charge-user",
  workflow.ActivityOptions{
    StartToCloseTimeout: 30 * time.Second,
  },
)
```

Use `temporal.NoInput` and `temporal.NoResult` when a workflow or activity takes
no input or produces no result.

### Client mode (dependency)

#### Execute workflows

```go
import (
  "context"

  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/temporal"

  "go.temporal.io/sdk/client"
)

svc, err := service.New()
if err != nil {
  panic(err)
}

c, err := temporal.Connect(svc, temporal.ConfigClient{
  Address:   "localhost:7233",
  Namespace: "default",
})
if err != nil {
  panic(err)
}

run, err := ProcessOrder.Execute(ctx, c, client.StartWorkflowOptions{
  TaskQueue: "orders",
}, OrderInput{
  UserID:  "usr_123",
  ItemIDs: []string{"item_1", "item_2"},
})
if err != nil {
  // ...
}

result, err := ProcessOrder.GetResult(ctx, run)
if err != nil {
  // ...
}
```

#### Schedule workflows

```go
opts := temporal.ScheduleOptions{
  TaskQueue: "orders",
  CronExpressions: []string{
    "0 1 * * *",
  },
}

err = ProcessOrder.CreateSchedule(ctx, c, opts)
if err != nil {
  // ...
}
```

### Worker mode (server)

```go
import (
  "context"

  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/temporal"

  "go.temporal.io/sdk/workflow"
)

svc, err := service.New()
if err != nil {
  panic(err)
}

client, worker, err := temporal.New(svc, temporal.ConfigWorker{
  Client: temporal.ConfigClient{
    Address:   "localhost:7233",
    Namespace: "default",
  },
  TaskQueue: "orders",
})
if err != nil {
  panic(err)
}

ProcessOrder.Register(worker, func(ctx workflow.Context, input OrderInput) (OrderResult, error) {
  var chargeResult ChargeResult
  err := ChargeUser.Execute(ctx, ChargeInput{
    UserID: input.UserID,
    Amount: 99.99,
  }).GetResult(ctx, &chargeResult)
  if err != nil {
    return OrderResult{}, err
  }

  return OrderResult{
    OrderID: "ord_789",
    Total:   99.99,
  }, nil
})

ChargeUser.Register(worker, func(ctx context.Context, input ChargeInput) (ChargeResult, error) {
  return ChargeResult{
    TransactionID: "txn_456",
  }, nil
})

_ = client
```

### Repeatable activities

For activities that run multiple times with different configurations, use
`NewRepeatableActivity` with an extra `Config` type parameter:

```go
type DownloadInput struct {
  BatchID string
}

type DownloadConfig struct {
  URL      string
  MaxWidth int
}

type DownloadResult struct {
  FilePath string
}

var DownloadImage = temporal.NewRepeatableActivity[
  DownloadInput, DownloadConfig, DownloadResult,
]("download-image", workflow.ActivityOptions{
  StartToCloseTimeout: 2 * time.Minute,
})

// In a workflow, execute once per image:
for _, img := range images {
  var result DownloadResult
  err := DownloadImage.Execute(ctx, DownloadInput{BatchID: "batch_1"}, DownloadConfig{
    URL:      img.URL,
    MaxWidth: 1024,
  }).GetResult(ctx, &result)
  if err != nil {
    // ...
  }
}
```

### Event propagation

Events propagated via `event.ContextWithEvent` are automatically carried across
workflow and activity boundaries through distributed tracing context.

```go
import (
  "github.com/mountayaapp/helix.go/event"
  "github.com/mountayaapp/helix.go/integration/temporal"
)

// In a REST handler — attach an event before executing a workflow.
ctx = event.ContextWithEvent(ctx, event.Event{UserID: "usr_123"})
run, err := ProcessOrder.Execute(ctx, client, opts, input)

// In a workflow — retrieve the event.
e, ok := temporal.EventFromWorkflow(ctx)

// In an activity — retrieve the event.
e, ok := temporal.EventFromActivity(ctx)
```

## Trace attributes

The `temporal` integration sets the following trace attributes:
- `temporal.server.address`
- `temporal.namespace`

When applicable, these attributes can be set as well:
- `temporal.worker.taskqueue`
- `temporal.workflow.id`
- `temporal.workflow.run_id`
- `temporal.workflow.namespace`
- `temporal.workflow.type`
- `temporal.workflow.attempt`
- `temporal.activity.id`
- `temporal.activity.type`
- `temporal.activity.attempt`
- `temporal.update.id`

Example:
```
temporal.server.address: "temporal.mydomain.tld"
temporal.namespace: "default"
temporal.worker.taskqueue: "orders"
temporal.workflow.namespace: "default"
temporal.workflow.type: "process-order"
temporal.workflow.attempt: 1
```
