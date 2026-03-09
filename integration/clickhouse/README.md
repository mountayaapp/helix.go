# helix.go - ClickHouse integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/clickhouse)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/clickhouse)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/clickhouse)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The ClickHouse integration provides an opinionated way to interact with ClickHouse
as an OLAP database, optimized for batch writes. It is a **dependency** integration
— calling `clickhouse.Connect(svc, cfg)` automatically registers it via
`service.Attach()`.

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/clickhouse
```

## Configuration

- `Address` (`string`) — ClickHouse server address. Default: `"127.0.0.1:9000"`.
- `Database` (`string`) — Database name. Default: `"default"`.
- `User` (`string`) — Username. Default: `"default"`.
- `Password` (`string`) — Password. Default: `"default"`.
- `TLS` (`integration.ConfigTLS`) — TLS settings.

## Usage

### Connecting

```go
import (
  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration"
  "github.com/mountayaapp/helix.go/integration/clickhouse"
)

svc, err := service.New()
if err != nil {
  panic(err)
}

db, err := clickhouse.Connect(svc, clickhouse.Config{
  Address:  "endpoint.clickhouse.cloud:9440",
  Database: "analytics",
  User:     "default",
  Password: "secret",
  TLS: integration.ConfigTLS{
    Enabled:            true,
    InsecureSkipVerify: true,
  },
})
if err != nil {
  panic(err)
}
```

### Batch inserts

```go
import (
  "context"
  "time"
)

type Event struct {
  Date   time.Time `ch:"date"`
  Name   string    `ch:"name"`
  UserID string    `ch:"user_id"`
}

ctx := context.Background()

// Create a batch insert for a table.
batch, err := db.NewBatchInsert(ctx, "events")
if err != nil {
  // ...
}

// Append rows to the batch using struct tags.
for i := 0; i < 10_000; i++ {
  err = batch.AppendStruct(ctx, &Event{
    Date:   time.Now().UTC(),
    Name:   "page_view",
    UserID: "usr_123",
  })
  if err != nil {
    // ...
  }
}

// Send the batch to ClickHouse.
err = batch.Send(ctx)
if err != nil {
  // ...
}
```

## Trace attributes

The `clickhouse` integration sets the following trace attributes:
- `clickhouse.database`

Example:
```
clickhouse.database: "analytics"
```
