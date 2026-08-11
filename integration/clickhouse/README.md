# helix.go - ClickHouse integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/clickhouse)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The ClickHouse integration provides an opinionated way to interact with ClickHouse
as an OLAP database, supporting batch writes, per-row async inserts, and analytical
reads. It is a **dependency** integration — calling `clickhouse.Connect(svc, cfg)`
automatically registers it via `service.Attach()`.

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

### Async inserts

For high-frequency, low-latency per-request writes (e.g. request counters), use
`AsyncInsertStruct`. It relies on ClickHouse's native `async_insert` mechanism:
the server buffers incoming rows and flushes them in the background, so the
call returns as soon as the server acknowledges receipt without waiting for
the flush. For bulk loads, prefer `NewBatchInsert`.

Every exported field must carry a `ch` struct tag; use `ch:"-"` to opt out.
Embedded (anonymous) struct fields are rejected — unlike the batch API,
`AsyncInsertStruct` does not recurse into child fields.

```go
type PageView struct {
  Date  time.Time `ch:"date"`
  Path  string    `ch:"path"`
  Count uint64    `ch:"count"`
}

err = db.AsyncInsertStruct(ctx, "page_views", PageView{
  Date:  time.Now().UTC(),
  Path:  "/pricing",
  Count: 1,
})
if err != nil {
  // ...
}
```

Server-side buffering is governed by `async_insert_max_data_size`,
`async_insert_busy_timeout_ms`, and `async_insert_max_query_number`.

### Reading rows

For analytical reads (e.g. aggregations over large datasets), use `Select`. It
scans the result set into a pointer to a slice of structs whose exported fields
carry `ch` struct tags matching the selected columns. Pass query arguments as
variadic parameters so the driver binds them rather than interpolating into the
SQL.

```go
type PageViewStat struct {
  Date  time.Time `ch:"date"`
  Path  string    `ch:"path"`
  Views uint64    `ch:"views"`
}

var stats []PageViewStat
err = db.Select(ctx, &stats,
  `SELECT date, path, sum(count) AS views
   FROM page_views
   WHERE date >= ? AND date <= ?
   GROUP BY date, path
   ORDER BY date`,
  startAt, endAt,
)
if err != nil {
  // ...
}
```

## Trace attributes

The `clickhouse` integration sets the following trace attributes:
- `clickhouse.database`

When applicable, these attributes can be set as well:
- `clickhouse.table`

Example:
```
clickhouse.database: "analytics"
```
