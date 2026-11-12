# helix.go - ClickHouse integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/clickhouse)
[![Latest version](https://img.shields.io/github/v/tag/mountayaapp/helix.go?filter=v*&sort=semver&label=version)](https://github.com/mountayaapp/helix.go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The ClickHouse integration provides an opinionated way to interact with ClickHouse
as an OLAP database, supporting statements, analytical reads, batch writes and
per-row async inserts, all traced and measured. It is a **dependency** integration:
`clickhouse.Connect(ctx, svc, cfg)` pings the server, then registers the
connection with the Service through `service.Attach()`. The Service owns its
lifecycle from there, and closes it when it stops.

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/clickhouse
```

## Configuration

- `Name` (`string`) — Identifies this connection in health output, error context
  and telemetry (`clickhouse.name`). Two connections of the same kind are told apart
  by it. Default: `"clickhouse"`.
- `Address` (`string`) — ClickHouse server address, native protocol. Default:
  `"127.0.0.1:9000"`.
- `Database` (`string`) — Database name. Default: `"default"`.
- `User` (`string`) — Username. Default: `"default"`.
- `Password` (`string`) — Password. No default: an empty password is sent as
  such, which is what the stock `default` user of a fresh server expects.
- `TLS` (`integration.ConfigTLS`) — TLS settings. The server's certificate is
  verified against the host of `Address` unless `TLS.ServerName` says otherwise.
- `DialTimeout` (`time.Duration`) — How long opening a connection, and waiting
  for one to come free in the pool, may take. It alone bounds a dial and its
  handshake, whatever the call's `ctx` says (see [Timeouts](#timeouts)).
  Default: `30s`.
- `ReadTimeout` (`time.Duration`) — How long a statement whose `ctx` has no
  deadline may wait on the server's answer, from the first read to the last. A
  context deadline on a call takes precedence. Default: `5m`.
- `MaxOpenConns` (`int`) — Connections the pool holds open at once, idle and
  acquired together. Default: `MaxIdleConns + 5`.
- `MaxIdleConns` (`int`) — Connections the pool keeps open while unused.
  Default: `5`.
- `ConnMaxLifetime` (`time.Duration`) — How long a connection is reused before
  it is replaced. Default: `1h`.
- `Compression` (`string`) — Wire compression: `"none"`, `"lz4"`, `"lz4hc"` or
  `"zstd"`. Default: `"none"`.
- `Settings` (`map[string]any`) — ClickHouse settings applied to every statement,
  such as `{"max_execution_time": 60}`.
- `Optional` (`bool`) — Marks the database as one the Service can run without,
  for a while: `Connect` does not fail when the server cannot be reached, and the
  connection's health does not gate the Service's readiness (see
  [Optional connections](#optional-connections)). Default: `false`.

### Timeouts

The `ctx` of a call bounds the call once it holds a connection: a statement, a
batch's begin and its send end with `ctx`. Dialing a connection, and its
handshake, are not bounded by `ctx`: the driver runs them under `DialTimeout`
alone, so a call to a server that accepts connections and never answers, or that
cannot be reached at all, waits out `DialTimeout` whatever its `ctx` says. That
includes `Connect`'s ping. Set `DialTimeout` to what a caller can afford to wait
on a hung server, for instance below what a shutdown leaves to close.

Every pool and timeout field left at its zero value takes the driver's default,
listed above. A negative one, or an unknown compression, is a validation error.

## Usage

### Connecting

`Connect` validates the `Config`, then pings the server before returning, so an
unreachable server or a rejected login fails at startup rather than on the first
statement. `ctx` bounds the ping once a connection is established; the dial and
handshake are bounded by `DialTimeout` ([Timeouts](#timeouts)). A `Config` that is
not valid fails with `VALIDATION_FAILED` before anything is dialed; a server that
cannot be reached fails with `SERVICE_UNAVAILABLE`, the driver's own message in
the entry's `reason` extension, unless the connection is `Optional`
([Optional connections](#optional-connections)). A `Connect` that fails returns a
nil `ClickHouse`.

```go
import (
  "context"
  "os"
  "os/signal"
  "syscall"

  "github.com/mountayaapp/helix.go/integration"
  "github.com/mountayaapp/helix.go/integration/clickhouse"
  "github.com/mountayaapp/helix.go/service"
)

func run() error {
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

  svc, err := service.New(ctx, service.Config{})
  if err != nil {
    return err
  }

  db, err := clickhouse.Connect(ctx, svc, clickhouse.Config{
    Name:     "analytics",
    Address:  "endpoint.clickhouse.cloud:9440",
    Database: "analytics",
    User:     "default",
    Password: os.Getenv("CLICKHOUSE_PASSWORD"),
    TLS: integration.ConfigTLS{
      Enabled: true,
    },
  })
  if err != nil {
    return err
  }

  // Use db from your servers or workers. Its lifecycle belongs to svc.
  _ = db

  return svc.Run(ctx)
}
```

### Statements and reads

Pass query arguments as variadic parameters, so the driver binds them rather
than interpolating them into the SQL.

`Exec` runs a statement that returns no rows: DDL, `ALTER`, `INSERT ... SELECT`.

```go
err := db.Exec(ctx, `
  CREATE TABLE IF NOT EXISTS page_views (
    date  Date,
    path  String,
    count UInt64
  ) ENGINE = SummingMergeTree ORDER BY (date, path)`)
```

`Select` scans a whole result set into a pointer to a slice of structs whose
exported fields carry `ch` struct tags matching the selected columns. It is meant
for analytical reads such as aggregations over large datasets.

```go
type PageViewStat struct {
  Date  time.Time `ch:"date"`
  Path  string    `ch:"path"`
  Views uint64    `ch:"views"`
}

var stats []PageViewStat
err := db.Select(ctx, &stats,
  `SELECT date, path, sum(count) AS views
   FROM page_views
   WHERE date >= ? AND date <= ?
   GROUP BY date, path
   ORDER BY date`,
  startAt, endAt,
)
```

`Query` returns the rows to iterate over, and `QueryRow` a single row whose error
is deferred to `Scan`, which returns `sql.ErrNoRows` when there is no row. Rows
hold a connection until they are closed or `Next` reports `false`, and a row until
it is scanned, so always close or scan them: that is also when their span ends.

```go
rows, err := db.Query(ctx, "SELECT path, count FROM page_views WHERE date = ?", day)
if err != nil {
  return err
}
defer rows.Close()

for rows.Next() {
  var path string
  var count uint64
  if err := rows.Scan(&path, &count); err != nil {
    return err
  }
}
if err := rows.Err(); err != nil {
  return err
}

var total uint64
err = db.QueryRow(ctx, "SELECT sum(count) FROM page_views WHERE date = ?", day).Scan(&total)
```

The driver's per-statement options, set on `ctx` through `Context` from
`github.com/ClickHouse/clickhouse-go/v2` — settings, query ID, parameters — apply
as usual. Two of them the integration sets itself, and yields to yours: a span set
with the driver's `WithSpan` is shipped to the server instead of the operation's,
and a `WithProfileEvents` callback is kept, in which case the span carries no
`clickhouse.selected.*` or `clickhouse.inserted.*` attributes.

### Batch inserts

A batch appends rows client-side and sends them in a single `INSERT`. Every batch
must end with `Send` or `Abort`: until then it holds a connection and an open span.
`Abort` does nothing on a batch already finished, so defer it right after
`NewBatchInsert` to cover every early return.

```go
type Event struct {
  Date   time.Time `ch:"date"`
  Name   string    `ch:"name"`
  UserID string    `ch:"user_id"`
}

batch, err := db.NewBatchInsert(ctx, "events")
if err != nil {
  return err
}
defer batch.Abort(ctx)

for _, event := range events {
  if err := batch.AppendStruct(ctx, &event); err != nil {
    return err
  }
}

if err := batch.Send(ctx); err != nil {
  return err
}
```

A batch is single-use. Once `Send` has run, whether it succeeded or failed, or
`Abort` has, `AppendStruct` and `Send` return an error wrapping the driver's
`ErrBatchAlreadySent` (`github.com/ClickHouse/clickhouse-go/v2`), recognisable
with `errors.Is`: a row is never dropped without the caller being told. After a failed `Send`, start a new batch.

A batch does not depend on the context it was begun with: it can be begun in one
request and sent after that request ended. The `ctx` given to each call bounds
that call: `NewBatchInsert`'s bounds the begin, and `Send`'s the send, once a
connection is held ([Timeouts](#timeouts)). A batch is not safe for concurrent
use.

With no option, a batch inserts every column of the table, in the table's order,
so each row must carry them all. `WithColumns` names the columns it inserts
instead, and `WithColumnsOf` names those a struct's `ch` tags name, in their
order. The statement becomes `INSERT INTO events (date, name, user_id)`:
`AppendStruct` fills the named columns from the fields whose tags name them,
ignores any other field, and every column of the table left unnamed takes its
`DEFAULT`. A writer that names its columns keeps working while the table gains a
column with a `DEFAULT` under it.

```go
batch, err := db.NewBatchInsert(ctx, "events", clickhouse.WithColumnsOf(Event{}))
```

### Async inserts

For high-frequency, low-latency per-request writes (e.g. request counters), use
`AsyncInsertStruct`. It relies on ClickHouse's native `async_insert` mechanism:
the server buffers incoming rows and flushes them in the background, so the
call returns as soon as the server acknowledges receipt without waiting for
the flush. For bulk loads, prefer `NewBatchInsert`.

Every exported field must carry a `ch` struct tag; use `ch:"-"` to opt out.
Embedded structs are rejected: unlike the batch API, `AsyncInsertStruct` does not
recurse into child fields.

```go
type PageView struct {
  Date  time.Time `ch:"date"`
  Path  string    `ch:"path"`
  Count uint64    `ch:"count"`
}

err := db.AsyncInsertStruct(ctx, "page_views", PageView{
  Date:  time.Now().UTC(),
  Path:  "/pricing",
  Count: 1,
})
```

Server-side buffering is governed by `async_insert_max_data_size`,
`async_insert_busy_timeout_ms`, and `async_insert_max_query_number`.

### Table and column names

`NewBatchInsert` and `AsyncInsertStruct` build their `INSERT` from the table name
they are given, which is routinely derived from input. It must be a name made of
letters, digits and underscores, optionally qualified by its database
(`analytics.events`); anything else is refused with `VALIDATION_FAILED` before
anything is sent. Accepted names are backtick-quoted.

Column names taken from `ch` struct tags are backtick-quoted with backticks and
backslashes escaped, so a tag can only ever name a column. A tag that is empty,
not valid UTF-8 or holds a control character is refused.

## Health

The Service checks this dependency by pinging the server. A server integration's
readiness probe (`GET /ready`) reports a failure as a generic `503` that names
nothing internal; the driver's reason goes to the logs and the span only.

Every connection also pings its server in the background, every 15 seconds, each
ping bounded by 5 seconds once a connection is established, and stopped by
`Close`. Every ping, `Connect`'s, the background one's and each health check's,
sets the `clickhouse.reachable` gauge, so it follows the server whether or not
anything probes the Service.

### Optional connections

A service whose work does not all need the database, and that handles a failed
statement itself (keeping what it could not write, answering a read with its own
error), sets `Optional`. Then:

- `Connect` attaches the connection even when the server cannot be reached, and
  logs a warning. The pool dials the server again on the next statement; until it
  answers, statements fail as they do against any server that is down.
- What refuses the connection still fails `Connect`, as it does without the
  option: a server that answers with an exception (a rejected login, a missing
  database) and a TLS handshake that fails (a certificate that is not trusted or
  names another host, a server that does not speak TLS, or a TLS alert from
  either end, such as a server that requires a client certificate the
  configuration does not present). That is a
  configuration to fix, not an outage to wait out. A server that cannot be
  reached or does not answer is waited out, and so is a name that does not
  resolve, since its DNS record can come back.
- The health check answers at once, healthy, without pinging the server: a ping
  to a server that hangs would hold a readiness probe for as long as a dial may
  last. A database outage therefore leaves every replica ready rather than taking
  the service out of its load balancer. Watch `clickhouse.reachable`, which the
  background ping keeps current, instead.

```go
db, err := clickhouse.Connect(ctx, svc, clickhouse.Config{
  Name:     "analytics",
  Address:  "endpoint.clickhouse.cloud:9440",
  Optional: true,
})
```

## Telemetry

### Spans

| Span | Kind | Attributes |
|---|---|---|
| `ClickHouse: Exec` | client | `clickhouse.query` |
| `ClickHouse: Select` | client | `clickhouse.query`, `clickhouse.selected.*` |
| `ClickHouse: Query` | client | `clickhouse.query`, `clickhouse.selected.*` |
| `ClickHouse: QueryRow` | client | `clickhouse.query`, `clickhouse.selected.*` |
| `ClickHouse: Batch` | internal | `clickhouse.table` |
| `ClickHouse: Batch / Begin` | client | `clickhouse.table` |
| `ClickHouse: Batch / Send` | client | `clickhouse.table`, `clickhouse.selected.*`, `clickhouse.inserted.*` |
| `ClickHouse: Batch / Abort` | client | `clickhouse.table` |
| `ClickHouse: Async Insert` | client | `clickhouse.table` |

`ClickHouse: Batch` stands for a whole batch: it is opened by `NewBatchInsert`,
ended by `Send` or `Abort`, and parents the batch's other spans whatever context
each call is given. An `AppendStruct` failure is recorded on it. `Query` ends when
its rows are closed or exhausted, and `QueryRow` when its row is scanned.

A failure is recorded on its span with the error the driver returned. The driver
writes the values bound to a statement into its text before sending it, and a
server's exception repeats the statement it was running, so that error can hold
every value of the query: register a function with `trace.SetErrorSanitizer` to
decide what of an error's text a span records.

Each statement also ships its span's identifiers to the server, which records them
in `system.opentelemetry_span_log`, so a slow query can be followed from the trace
into ClickHouse's own view of it.

### Trace attributes

Every span carries:
- `clickhouse.name` — the `Config.Name` of the connection.
- `clickhouse.database`

When applicable, these attributes are set as well:
- `clickhouse.query` — the statement's text, for the statements the caller wrote.
  Its arguments are never recorded.
- `clickhouse.table`
- `clickhouse.selected.rows`, `clickhouse.selected.bytes`,
  `clickhouse.inserted.rows`, `clickhouse.inserted.bytes` — the server's own
  counters for the statement, summed as it reports them.

Example:
```
clickhouse.name: "analytics"
clickhouse.database: "analytics"
clickhouse.query: "SELECT path, count FROM page_views WHERE date = ?"
clickhouse.selected.rows: 1024
```

### Metrics

- `clickhouse.operation.duration` (histogram, `s`) — duration of every operation,
  by `clickhouse.name`, `clickhouse.operation` (the span name after
  `ClickHouse: `) and `clickhouse.outcome` (`ok` or `error`). The `Batch` span is
  not measured: its duration includes the caller's own time appending.
- `clickhouse.pool.connections` (gauge, `{connection}`) — connections held by the
  pool, by `clickhouse.name` and `clickhouse.pool.state`: `idle`, or `acquired` by
  a statement, including one still being dialed.
- `clickhouse.pool.connections.max` (gauge, `{connection}`) — the most connections
  the pool may hold open, by `clickhouse.name`. `acquired` against it is the
  headroom left before a statement waits for a connection.
- `clickhouse.reachable` (gauge, `1`) — whether the server answered the last ping,
  at `Connect`, in the background every 15 seconds, or at a health check, by
  `clickhouse.name`: `1` when it did, `0` when it did not. It is how an optional
  connection's outage shows.
