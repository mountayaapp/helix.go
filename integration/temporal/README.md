# helix.go - Temporal integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/temporal)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/temporal)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/temporal)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The Temporal integration provides an opinionated way to interact with Temporal
for durable, fault-tolerant executions.

The integration has two constructors depending on the service's role:

- `temporal.Connect()` creates a **client-only** connection registered as a
  **dependency** via `service.Attach()`. Use this for services that need to start
  or schedule workflows without processing them.

- `temporal.New()` creates a **worker** along with a client, registered as a
  **server** via `service.Serve()`. Use this for worker services that process
  workflows and activities.

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
temporal.worker.taskqueue: "demo"
temporal.workflow.namespace: "default"
temporal.workflow.type: "hello_world"
temporal.workflow.attempt: 1
```

## Usage

Install the Go module with:
```sh
$ go get github.com/mountayaapp/helix.go/integration/temporal
```

Define type-safe workflows and activities:

```go
import (
  "github.com/mountayaapp/helix.go/integration/temporal"
)

var MyWorkflow = temporal.NewWorkflow[
  WorkflowInput,
  WorkflowResult,
]("workflow-name")

var MyActivity = temporal.NewActivity[
  ActivityInput,
  ActivityResult,
]("activity-name")
```

### Client mode (dependency)

Use `temporal.Connect()` with `ConfigClient` when the service only needs to start
or schedule workflows. This registers a client-only connection as a dependency.

#### Execute workflows

```go
import (
  "github.com/mountayaapp/helix.go/integration/temporal"
)

cfg := temporal.ConfigClient{
  Address:   "localhost:7233",
  Namespace: "default",
}

client, err := temporal.Connect(cfg)
if err != nil {
  // ...
}

run, err := MyWorkflow.Execute(ctx, client, opts, input)
if err != nil {
  // ...
}
```

#### Schedule workflows

```go
import (
  "github.com/mountayaapp/helix.go/integration/temporal"
)

cfg := temporal.ConfigClient{
  Address:   "localhost:7233",
  Namespace: "default",
}

client, err := temporal.Connect(cfg)
if err != nil {
  // ...
}

opts := temporal.ScheduleOptions{
  TaskQueue: "task-queue",
  CronExpressions: []string{
    "0 1 * * *",
  },
}

err = MyWorkflow.CreateSchedule(ctx, client, opts)
if err != nil {
  // ...
}
```

### Worker mode (server)

Use `temporal.New()` with `ConfigWorker` when the service processes workflows
and activities. This registers a worker as a server and also returns a client.

#### Register workflows and activities

```go
import (
  "github.com/mountayaapp/helix.go/integration/temporal"
)

cfg := temporal.ConfigWorker{
  Client: temporal.ConfigClient{
    Address:   "localhost:7233",
    Namespace: "default",
  },
  TaskQueue: "demo",
}

client, worker, err := temporal.New(cfg)
if err != nil {
  // ...
}

MyWorkflow.Register(worker, myWorkflowImpl)
MyActivity.Register(worker, myActivityImpl)
```

The `client` returned by `temporal.New()` can be used to start or schedule
additional workflows from within the same worker service.

#### Execute activities

Execute type-safe activities from a workflow:

```go
err := MyActivity.Execute(ctx, input).GetResult(ctx, &result)
if err != nil {
  // ...
}
```
