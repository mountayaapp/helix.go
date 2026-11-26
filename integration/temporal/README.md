# helix.go - Temporal integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountaya/helix.go.svg)](https://pkg.go.dev/github.com/mountaya/helix.go/integration/temporal)
[![Latest version](https://img.shields.io/github/v/tag/mountaya/helix.go?filter=v*&sort=semver&label=version)](https://github.com/mountaya/helix.go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The Temporal integration provides an opinionated, type-safe way to interact with
[Temporal](https://temporal.io/) for durable, fault-tolerant workflow executions.
Workflows, activities, signals, queries and updates are declared once as typed
handles, and their input and result types are checked at compile time on both the
client and the worker side.

The integration has two constructors depending on the service's role:

- `temporal.Connect(ctx, svc, cfg)` creates a **client-only** connection and
  registers it with the Service as a **dependency**, through `service.Attach()`. Use
  this for services that start or schedule workflows without processing them.

- `temporal.New(ctx, svc, cfg)` creates a **worker** along with the client it polls
  with. The worker is registered as a **server**, through `service.Serve()`, and its
  client as a **dependency**. Use this for worker services that process workflows
  and activities.

Both dial the server and check its health before returning. The Service owns what
they registered from then on: the `*temporal.Client` and `*temporal.Worker` you get
back start workflows and register implementations, and carry no lifecycle methods.

## Installation

```sh
$ go get github.com/mountaya/helix.go/integration/temporal
```

## Configuration

### ConfigClient

Used by `temporal.Connect()`, and embedded in `ConfigWorker`.

- `Name` (`string`) — Identifies the connection in health output, error context and
  telemetry (`temporal.name`). Two connections attached to the same Service must
  have different names. Ignored by `temporal.New()`, where `ConfigWorker.Name`
  names the worker and its client alike. Default: `"temporal"`.
- `Address` (`string`) — Temporal server address: a `host:port` pair, or a gRPC
  target such as `"dns:///temporal.internal:7233"`. Default: `"127.0.0.1:7233"`.
- `Namespace` (`string`) — Temporal namespace. Default: `"default"`.
- `APIKey` (`string`) — Authenticates with an API key, as Temporal Cloud accepts.
  Setting it turns TLS on, with the system's root certificates, even when
  `TLS.Enabled` is false. Never serialized to JSON.
- `DataConverter` (`converter.DataConverter`) — Serialization of workflow and
  activity arguments and results, and of the Event header. Clients and workers of a
  namespace must agree on it. Default: the SDK's default converter.
- `PropagateFullEvent` (`bool`) — Sends the whole `event.Event` in the Event header
  instead of the allow-listed identifiers. See
  [Event propagation](#event-propagation). Default: `false`.
- `TLS` (`integration.ConfigTLS`) — TLS settings. See [TLS](#tls).

### ConfigWorker

Used by `temporal.New()`.

- `Client` (`ConfigClient`) — Configuration of the client the worker polls with.
  Its `Name` is ignored.
- `Name` (`string`) — Identifies the worker in health output, error context and
  telemetry (`temporal.name`). The worker is registered as a server under it, and
  its client as a dependency under it too. Default: `TaskQueue`.
- `TaskQueue` (`string`) — Task queue the worker polls. **Required**.
- `MaxConcurrentActivities` (`int`) — How many activities the worker runs at once.
  An activity task is polled only while a slot is free, so this is what bounds the
  memory, the connections and the upstream calls the activities of one worker hold
  together: size it from what one activity holds, and for how long. Default:
  `1000`.
- `MaxConcurrentLocalActivities` (`int`) — How many local activities the worker
  runs at once, outside the limit above. Default: `1000`.
- `MaxConcurrentWorkflowTasks` (`int`) — How many workflow tasks the worker runs at
  once. `1` is a validation error: the SDK refuses it, since such a worker would
  only ever poll its sticky queue. Default: `1000`.
- `WorkerActivitiesPerSecond` (`float64`) — Max activities per second per worker.
  Default: `100000`.
- `TaskQueueActivitiesPerSecond` (`float64`) — Max activities per second for the
  entire task queue (server-managed). Default: `100000`.
- `EnableSessionWorker` (`bool`) — Enable Temporal session worker. Default: `false`.
- `DisableWorkflowWorker` (`bool`) — Serve the task queue's activities and nothing
  else: no workflow task is polled, on the task queue or a sticky queue. A worker
  polls for workflow tasks whether or not a workflow is registered, so this is what a
  worker meant for activities alone sets. Give a group of activities slots of their
  own beside another worker's by scheduling them on such a worker's task queue (their
  `ActivityOptions.TaskQueue`). Registering a workflow on it panics, and
  `MaxConcurrentWorkflowTasks` does not apply. Default: `false`.
- `StopTimeout` (`time.Duration`) — How long the worker waits, once told to stop,
  for the activities it is running to finish. See [Shutdown](#shutdown). Default:
  `25 * time.Second`.
- `SysInfoProvider` (`worker.SysInfoProvider`) — Reports the host's CPU and memory
  usage in worker heartbeats. Not wired by default, because the gopsutil-based one
  in `go.temporal.io/sdk/contrib/sysinfo` brings gopsutil, cgroups and eBPF packages
  into every binary; pass `sysinfo.SysInfoProvider()` to opt in. Experimental in the
  SDK. Default: `nil`, and heartbeats report zero.

A rate limit, a concurrency limit or `StopTimeout` left at zero keeps the default; a
negative one is a validation error. In JSON, `StopTimeout` is integer nanoseconds.

Validation errors point at the field to fix, following the JSON shape of the
configuration: `config.address` for `Connect`, `config.client.address` and
`config.task_queue` for `New`.

### TLS

With `TLS.Enabled`, the client connects with TLS or not at all: a TLS configuration
that cannot be built — a certificate or key that does not parse, a root CA that is
not PEM — is a `VALIDATION_FAILED` error returned before anything is dialed, never a
fallback to plaintext. The server's certificate is verified:
- against `TLS.RootCAPEMs` when set, and the system's roots otherwise;
- for the server name `TLS.ServerName` when set, and otherwise the host of
  `Address`.

`TLS.InsecureSkipVerify` keeps the encryption and drops the verification. A client
certificate, for mutual TLS, is presented when `TLS.CertPEM` and `TLS.KeyPEM` are
set.

## Usage

### Defining workflows, activities, signals, queries and updates

Handles carry no connection state: declare them as package-level variables, and
share them between the client and the worker side.

```go
import (
  "time"

  "github.com/mountaya/helix.go/integration/temporal"

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

type CancelPayload struct {
  Reason string
}

type StatusResult struct {
  State string
}

var ProcessOrder = temporal.NewWorkflow[OrderInput, OrderResult]("process-order")

var ChargeUser = temporal.NewActivity[ChargeInput, ChargeResult](
  "charge-user",
  workflow.ActivityOptions{
    StartToCloseTimeout: 30 * time.Second,
  },
)

var CancelOrder = temporal.NewSignal[CancelPayload]("cancel-order")

var OrderStatus = temporal.NewQuery[temporal.NoInput, StatusResult]("order-status")

var AddItem = temporal.NewUpdate[string, OrderInput]("add-item")
```

Use `temporal.NoInput` and `temporal.NoResult` when a workflow, activity or query
takes no input or produces no result.

### Client mode (dependency)

```go
import (
  "context"
  "os"
  "os/signal"
  "syscall"

  "github.com/mountaya/helix.go/integration/temporal"
  "github.com/mountaya/helix.go/service"
)

func run() error {
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

  svc, err := service.New(ctx, service.Config{})
  if err != nil {
    return err
  }

  c, err := temporal.Connect(ctx, svc, temporal.ConfigClient{
    Address:   "127.0.0.1:7233",
    Namespace: "default",
  })
  if err != nil {
    return err
  }

  // Hand c to the servers that start workflows. Its lifecycle belongs to svc.
  _ = c

  // Run blocks until ctx is cancelled, then closes the connection along with the
  // rest of the Service.
  return svc.Run(ctx)
}
```

`Connect` and `New` return:
- a `VALIDATION_FAILED` error, before anything is dialed, when the configuration is
  not valid — TLS settings included;
- a `SERVICE_UNAVAILABLE` error, `"Failed to connect to Temporal"`, when the server
  cannot be reached, refuses the credentials, or the TLS handshake fails. The SDK's
  message is in the entry's `reason` extension;
- the Service's error when it refuses the registration, such as for a name already
  taken.

On every error, the client they dialed is closed again — unless the Service already
owns it, in which case the Service closes it when it stops.

#### Executing workflows

```go
import (
  "go.temporal.io/sdk/client"
)

run, err := c.ExecuteWorkflow(ctx, ProcessOrder, client.StartWorkflowOptions{
  ID:        "order-123",
  TaskQueue: "orders",
}, OrderInput{
  UserID:  "usr_123",
  ItemIDs: []string{"item_1", "item_2"},
})
if err != nil {
  // run is nil.
}

result, err := run.Await(ctx)
if err != nil {
  // ...
}
```

`OrderInput` and `OrderResult` are inferred from the `ProcessOrder` handle, so
passing the wrong input type or reading the wrong result type is a build error.

`Await` follows a run that continues as new, is retried or runs on a cron to the
run that ends the chain. `AwaitInto(ctx, &dst)` decodes into a value the caller
supplies, to merge a result into an accumulating one; it returns a validation error
for a nil `dst`. The merge is a property of the encoding: with JSON, a field the
workflow left empty under `omitempty` keeps what `dst` held, while a protobuf result
is decoded into a reset message, and nothing is merged.

`c.WorkflowRun(ctx, ProcessOrder, workflowID, runID)` returns a handle on an
existing run, to await it from elsewhere. An empty `runID` targets the latest run.

To read a result without blocking on a run that may not have closed, use
`WorkflowResultIfClosed`. It reports whether the run had closed rather than parking
a goroutine on it:

```go
result, closed, err := c.WorkflowResultIfClosed(ctx, ProcessOrder, workflowID, runID)
if err != nil {
  // The run closed without a result: failed, cancelled, terminated, timed out,
  // or continued as new (a *workflow.ContinueAsNewError).
}
if !closed {
  // Still running, paused, or unknown — try again later.
}
```

Unlike `Await`, it never follows a run into the next one: a closed run is reported
as it closed.

#### Signals, queries and updates

```go
// Send a signal. An empty run ID targets the latest run.
err := c.SignalWorkflow(ctx, CancelOrder, workflowID, "", CancelPayload{
  Reason: "out of stock",
})

// Signal a workflow, starting it first if it is not running.
run, err := c.SignalWithStartWorkflow(ctx, ProcessOrder, CancelOrder, client.StartWorkflowOptions{
  ID:        "order-123",
  TaskQueue: "orders",
}, OrderInput{UserID: "usr_123"}, CancelPayload{Reason: "out of stock"})

// Query a workflow's current state.
status, err := c.QueryWorkflow(ctx, OrderStatus, workflowID, "", temporal.NoInput{})

// Send an update and wait for its result.
order, err := c.UpdateWorkflow(ctx, AddItem, workflowID, "", "item_3")
```

#### Managing workflows

```go
// Request cancellation: the workflow sees its context cancelled.
err := c.CancelWorkflow(ctx, workflowID, "")

// End it immediately, recording the reason in its history.
err = c.TerminateWorkflow(ctx, workflowID, "", "operator request")

// Read its status and metadata.
desc, err := c.DescribeWorkflow(ctx, workflowID, "")
```

#### Scheduling workflows

```go
err := c.CreateSchedule(ctx, ProcessOrder, temporal.ScheduleOptions{
  TaskQueue: "orders",
  CronExpressions: []string{
    "0 1 * * *",
  },
}, OrderInput{UserID: "usr_system"})
```

`CreateSchedule` is meant to run on every startup of every replica. It creates the
schedule when there is none, leaves an existing one alone when it matches the
request, and updates it otherwise — keeping its state, so a schedule an operator
paused stays paused. Two replicas creating it at the same time is not an error.

The schedule's ID is `ScheduleOptions.ID`, the workflow's name by default.
`OverlapPolicy` defaults to `SCHEDULE_OVERLAP_POLICY_SKIP`. Whether a schedule
matches is decided by a fingerprint of the request — workflow, task queue, cron
expressions, overlap policy and input — recorded in the action's memo under
`helix.schedule.fingerprint`, which therefore also appears on every workflow the
schedule starts. A change made outside the code, in the Temporal UI, is left in
place until the request itself changes, except for the workflow, the task queue
and the overlap policy, which are compared as described.

### Worker mode (server)

```go
import (
  "context"
  "os"
  "os/signal"
  "syscall"

  "github.com/mountaya/helix.go/integration/temporal"
  "github.com/mountaya/helix.go/service"

  "go.temporal.io/sdk/workflow"
)

func run() error {
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

  svc, err := service.New(ctx, service.Config{})
  if err != nil {
    return err
  }

  // A worker service that starts no workflow of its own discards the client.
  _, worker, err := temporal.New(ctx, svc, temporal.ConfigWorker{
    Client: temporal.ConfigClient{
      Address:   "127.0.0.1:7233",
      Namespace: "default",
    },
    TaskQueue: "orders",
  })
  if err != nil {
    return err
  }

  worker.RegisterWorkflow(ProcessOrder, func(ctx workflow.Context, input OrderInput) (OrderResult, error) {
    _, err := ChargeUser.Execute(ctx, ChargeInput{
      UserID: input.UserID,
      Amount: 99.99,
    }).Await(ctx)
    if err != nil {
      return OrderResult{}, err
    }

    return OrderResult{
      OrderID: "ord_789",
      Total:   99.99,
    }, nil
  })

  worker.RegisterActivity(ChargeUser, func(ctx context.Context, input ChargeInput) (ChargeResult, error) {
    return ChargeResult{
      TransactionID: "txn_456",
    }, nil
  })

  // Run starts the worker, and drains it when ctx is cancelled.
  return svc.Run(ctx)
}
```

Register every workflow and activity before the Service starts. The implementation
signature is checked against the handle's declared types at compile time, so a
mismatched `func` will not build.

#### An activity that panics

An activity that panics fails with a fixed message, `activity panicked`, and no
stack trace. Left to the SDK, the failure would carry the value the code panicked
with, printed, and the stack of its goroutine, both stored in the history of the
workflow that scheduled the activity and handed to its code as the error's text;
a panic's value is whatever the failing code held, a query or a key included.

What a workflow and a retry policy act on is unchanged: the failure has the type
the SDK gives a panic, `PanicError`, so the workflow still receives a
`*temporal.PanicError` and a policy listing `PanicError` in
`NonRetryableErrorTypes` still stops on it, and it is retryable, so the activity
is retried as its policy says. The panic and its stack are recorded on the
activity's span, and what the SDK records of a panic is recorded as it always
was: the line `Activity panic.` (`LocalActivity panic.` for a local activity),
with `WorkflowID`, `RunID`, `ActivityType` and `Attempt`, through the Service's
logger, the panic written as an error field that the function registered with
`trace.SetErrorSanitizer` is applied to, and the counters
`temporal_activity_task_error` and `temporal_local_activity_error`, under the
tags the SDK gives them, so an alert on either goes on firing. A panic is one
line, as under a plain worker: the SDK logs `Activity error.` for an activity
that returns an error, which the recovered panic is, and the Service's logger
leaves that line out for the failure the interceptor returned. A worker whose
client logs through another logger than helix's (an SDK worker handed
`WorkerInterceptors()` on a client of its own) writes both, `Activity panic.`
and then `Activity error.` with `activity panicked`, which are one event. A panic
in a workflow's own code is the SDK's to handle, and is not covered.

A test running activities in the SDK's test environment gets the same behaviour
by handing it the worker's interceptors:

```go
env := suite.NewTestActivityEnvironment()
env.SetWorkerOptions(worker.Options{Interceptors: temporal.WorkerInterceptors()})
```

#### Activity options

The `workflow.ActivityOptions` an activity is declared with are its defaults.
Options already on the workflow context take precedence, field by field, which is
how a single call gets a longer timeout or another task queue:

```go
ctx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
  StartToCloseTimeout: time.Hour,
})

result, err := ChargeUser.Execute(ctx, input).Await(ctx)
```

The workflow's own task queue, which the SDK puts on every workflow context, counts
as unset: an activity declared with a `TaskQueue` runs there unless the context
names another one.

#### Repeatable activities

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

// Register it on the worker:
worker.RegisterRepeatableActivity(DownloadImage, func(ctx context.Context, input DownloadInput, config DownloadConfig) (DownloadResult, error) {
  return DownloadResult{FilePath: "/tmp/img.jpg"}, nil
})

// In a workflow, execute once per image:
for _, img := range images {
  result, err := DownloadImage.Execute(ctx, DownloadInput{BatchID: "batch_1"}, DownloadConfig{
    URL:      img.URL,
    MaxWidth: 1024,
  }).Await(ctx)
  if err != nil {
    // ...
  }
}
```

#### Receiving signals, answering queries, handling updates

```go
worker.RegisterWorkflow(ProcessOrder, func(ctx workflow.Context, input OrderInput) (OrderResult, error) {
  state := "pending"

  // Answer queries from the workflow's current state.
  if err := OrderStatus.SetHandler(ctx, func(temporal.NoInput) (StatusResult, error) {
    return StatusResult{State: state}, nil
  }); err != nil {
    return OrderResult{}, err
  }

  // Handle updates, after an optional validator that rejects them without a trace
  // in the workflow's history.
  if err := AddItem.SetHandler(ctx, func(ctx workflow.Context, item string) (OrderInput, error) {
    input.ItemIDs = append(input.ItemIDs, item)
    return input, nil
  }, func(ctx workflow.Context, item string) error {
    if item == "" {
      return errors.New("item must be set")
    }
    return nil
  }); err != nil {
    return OrderResult{}, err
  }

  // Wait for a signal.
  payload, _ := CancelOrder.Receive(ctx)
  state = "cancelled: " + payload.Reason

  return OrderResult{}, nil
})
```

`Receive` blocks until the signal arrives, and does not return when the context is
cancelled. To wait on a signal alongside a timer, cancellation or other signals, use
its `Channel` with a `workflow.Selector`:

```go
var payload CancelPayload

selector := workflow.NewSelector(ctx)
selector.AddReceive(CancelOrder.Channel(ctx), func(ch workflow.ReceiveChannel, _ bool) {
  ch.Receive(ctx, &payload)
})
selector.AddFuture(workflow.NewTimer(ctx, time.Hour), func(workflow.Future) {})
selector.Select(ctx)
```

## Event propagation

An `event.Event` set on the context with `event.NewContext` travels with the work
Temporal carries: from a client to the workflow it starts, signals, queries or
updates, and from a workflow to its activities and child workflows. It travels in a
dedicated `event` header, not as baggage.

```go
import (
  "github.com/mountaya/helix.go/event"
  "github.com/mountaya/helix.go/integration/temporal"
)

// In a REST handler — attach an event before executing a workflow.
ctx = event.NewContext(ctx, event.Event{ID: "evt_123", Name: "order.placed"})
run, err := c.ExecuteWorkflow(ctx, ProcessOrder, opts, input)

// In a workflow — retrieve the event.
e, ok := temporal.EventFromWorkflow(ctx)

// In an activity — retrieve the event. event.FromContext(ctx) finds it too, so the
// integrations an activity calls propagate it further.
e, ok := temporal.EventFromActivity(ctx)
```

What the header holds is decided by the sending client:
- **By default, only the allow-listed identifiers** that `event.Event.Propagated`
  sends as baggage: the event ID and name, the organization and tenant IDs, and the
  application name and version. The user ID, IP address, user agent, device,
  location, meta and subscriptions never leave the process.
- **The whole Event** with `ConfigClient.PropagateFullEvent`. Temporal persists
  headers in workflow history, which the server keeps for the namespace's retention
  period and shows to anyone allowed to read that history. Enable it only when a
  worker needs those fields and the namespace's retention and access rules are
  acceptable for them.

Either way, the header is encoded with the client's `DataConverter`: a payload codec
configured there — encryption included — applies to it as it does to arguments,
and a worker decodes it with its own. A worker re-sends what it received to its
activities and child workflows under its own client's rule.

Only an Event set explicitly with `event.NewContext` is sent. An Event that
`event.FromContext` would rebuild from inbound baggage — whatever the caller of an
HTTP endpoint put in its headers — is not written to workflow history.

`EventFromWorkflow` and `EventFromActivity` report `false` when the work arrived
without an Event, or with a header that could not be decoded; the latter is logged,
through the workflow's logger, which stays quiet while the workflow replays.

The trace itself crosses Temporal through the SDK's `_tracer-data` header, written
with the Service's propagator. It carries the trace context only: baggage is not
propagated through Temporal, so neither is anything else a caller put in its
`baggage` header.

## Health

The client — `Connect`'s, or the one a worker polls with — is a dependency whose
`CheckHealth` calls the server's health check. The worker is a server whose
`CheckHealth` reports whether it is polling: it is unhealthy before it has started,
once it has stopped, and after it stopped on a fatal error. When either fails, the
Service's readiness probe answers a generic `503` with no detail; the failure, with
its `reason`, goes to the log and the span.

## Shutdown

When the Service stops, the worker stops polling and waits up to
`ConfigWorker.StopTimeout` for the activities it is running to finish. Activities
still running after that are cancelled, and Temporal retries them on another worker
according to their retry policy. Workflow tasks are not lost: their state is in
history, and another worker picks them up.

`StopTimeout` is spent inside the Service's `ShutdownTimeout`, which all
integrations share and which keeps part of itself for flushing telemetry — with the
defaults, 25 of its 30 seconds. A longer `StopTimeout` is cut short: the worker is
reported as not drained, and its client is closed under the activities still
running. Raise `ShutdownTimeout` along with it.

The worker's client is closed after every server has drained, with the other
dependencies, so that a REST API still finishing its requests can keep starting
workflows through it.

A Service stopped before the worker started — `Run` with a context already
cancelled, a `SIGTERM` during startup — never starts it.

## Telemetry

Spans, metrics and logs are produced by the Temporal SDK's own instrumentation,
through the Service's tracer, meter and logger, and speak this integration's
vocabulary.

### Traces

Spans on the client side, and on the workflow side when a workflow calls out, are
`client` spans:

- `Temporal: Start Workflow`
- `Temporal: Signal Workflow`
- `Temporal: Signal With Start Workflow`
- `Temporal: Query Workflow`
- `Temporal: Update Workflow`
- `Temporal: Update With Start Workflow`
- `Temporal: Create Schedule`
- `Temporal: Start Activity` — a workflow scheduling an activity, local ones included
- `Temporal: Start Child Workflow`
- `Temporal: Signal External Workflow`
- `Temporal: Signal Child Workflow`
- `Temporal: Start Nexus Operation`

Spans on the worker side are `server` spans:

- `Temporal: Run Workflow`
- `Temporal: Run Activity`
- `Temporal: Handle Signal`
- `Temporal: Handle Query`
- `Temporal: Validate Update`
- `Temporal: Handle Update`
- `Temporal: Run Start Nexus Operation Handler`
- `Temporal: Run Cancel Nexus Operation Handler`

Every span carries:
- `temporal.name`
- `temporal.server.address`
- `temporal.namespace`

When applicable, these are carried as well:
- `temporal.workflow.id`
- `temporal.workflow.run_id`
- `temporal.workflow.type` — on workflow spans, and on `Run Activity` for the
  workflow that scheduled the activity
- `temporal.workflow.attempt` — on `Run Workflow`
- `temporal.activity.id`
- `temporal.activity.type`
- `temporal.activity.attempt` — on `Run Activity`
- `temporal.signal.type`
- `temporal.query.type`
- `temporal.update.id`
- `temporal.update.type`
- `temporal.schedule.id`
- `temporal.nexus.operation`
- `temporal.worker.task_queue` — on `Run Workflow` and `Run Activity`

`Run Workflow` and `Run Activity` are the entry spans of the worker side, and carry
the attributes of the Event the work arrived with (`event.id`, `event.name`, …) —
only what the client's header held. See [Event propagation](#event-propagation).

A failure is recorded on the span, and sets its status to error unless it is an
application error the workflow or activity marked as benign. The SDK records the
error and writes its text as the status's description, and both go through the
function registered with `trace.SetErrorSanitizer`, as on the span of every other
integration: an activity's error is whatever the code under it returned, a
driver's text quoting the values it was handed included.

Example:
```
temporal.name: "orders"
temporal.server.address: "temporal.internal:7233"
temporal.namespace: "default"
temporal.workflow.id: "order-123"
temporal.workflow.run_id: "0194e2a1-…"
temporal.workflow.type: "process-order"
temporal.workflow.attempt: 1
temporal.worker.task_queue: "orders"
event.id: "evt_123"
event.name: "order.placed"
```

A workflow is replayed from its history whenever a worker has to rebuild its state
— after a restart, or when the workflow left the worker's cache. Each replay opens
a `Run Workflow` span of its own, so one workflow execution can show several. Spans
for activities, child workflows and signals are not repeated: the SDK skips them
while replaying.

### Metrics

The SDK's own metrics — request latencies, poller counts, task slots, workflow and
activity outcomes — are reported through the Service's meter, under the SDK's
`temporal_` names, with the attribute `temporal.name`. Among them:

- `temporal_request`, `temporal_request_latency`
- `temporal_long_request`, `temporal_long_request_latency`
- `temporal_worker_start`, `temporal_num_pollers`
- `temporal_worker_task_slots_available`, `temporal_worker_task_slots_used`
- `temporal_workflow_task_execution_latency`, `temporal_workflow_task_replay_latency`
- `temporal_workflow_completed`, `temporal_workflow_endtoend_latency`
- `temporal_activity_execution_latency`, `temporal_activity_succeed_endtoend_latency`
- `temporal_sticky_cache_size`, `temporal_sticky_cache_hit`

See [Temporal's SDK metrics reference](https://docs.temporal.io/references/sdk-metrics)
for the full list.

### Logs

The SDK logs through the Service's logger. A line logged for a workflow or an
activity — including through `workflow.GetLogger` and `activity.GetLogger` — is
correlated with its span, and carries the same `trace_id` and `span_id` as every
other line helix logs. `workflow.GetLogger` stays quiet while the workflow replays.

## Security

- With `TLS.Enabled`, no connection is ever plaintext. A TLS configuration that
  cannot be built fails `Connect` and `New` before anything is dialed. See
  [TLS](#tls).
- `APIKey` turns TLS on: the key is a bearer token sent on every request.
- The Event header holds only the allow-listed identifiers unless
  `PropagateFullEvent` is set, is encoded with the configured `DataConverter`, and is
  persisted in workflow history for the namespace's retention period. See
  [Event propagation](#event-propagation).
- Only an Event set explicitly is written to history: inbound baggage is neither
  turned into one nor carried through Temporal.
- Workflow and run IDs exposed through `WorkflowRun.Metadata()`, in the
  `temporalrest` package, reach REST clients verbatim. Do not build workflow IDs
  from personal data or secrets you would not hand to the caller.
- Errors returned by the client are the SDK's own, and may name the server, the
  namespace or the workflow. Do not send them to a client as they are.
- An activity that panics stores neither the value it panicked with nor a stack in
  workflow history. See [An activity that panics](#an-activity-that-panics). An
  error an activity returns is stored with its text, as the SDK stores it: do not
  put in it what the history should not hold.
