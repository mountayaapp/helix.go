# helix.go - PostgreSQL integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/postgres)
[![Latest version](https://img.shields.io/github/v/tag/mountayaapp/helix.go?filter=v*&sort=semver&label=version)](https://github.com/mountayaapp/helix.go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The PostgreSQL integration provides an opinionated way to interact with PostgreSQL
as an OLTP database, on top of a [pgx](https://github.com/jackc/pgx) connection
pool. It is a **dependency** integration: calling `postgres.Connect(ctx, svc, cfg)`
builds the pool, checks that the database can be reached, and registers it with
the Service through `service.Attach()`. The Service owns its lifecycle from then
on; the `postgres.PostgreSQL` you get back can query, not close the pool.

The integration works with all PostgreSQL-compatible databases, such as:
- [Citus](https://www.citusdata.com/)
- [Timescale](https://www.timescale.com/)
- [CockroachDB](https://www.cockroachlabs.com/)
- [YugabyteDB](https://www.yugabyte.com/)
- [Neon](https://neon.tech/)
- [Google AlloyDB](https://cloud.google.com/alloydb)
- [Amazon Aurora](https://aws.amazon.com/rds/aurora/)

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/postgres
```

## Configuration

- `Name` (`string`) — Identifies the connection in health output, error context and
  telemetry (`postgres.name`). Two connections attached to the same Service — a
  primary and a replica — must have different names. Default: `"postgres"`.
- `Address` (`string`) — Server address, as `"host:port"`. Default:
  `"127.0.0.1:5432"`.
- `Database` (`string`) — Database name. **Required**.
- `User` (`string`) — Username. **Required**.
- `Password` (`string`) — Password. **Required**.
- `ApplicationName` (`string`) — Reported to the server as `application_name`, as
  shown by `pg_stat_activity` and the server's logs. Default: the server's.
- `TimeZone` (`string`) — IANA time zone of every connection, such as `"UTC"`.
  The server takes it as the session's `TimeZone`, which decides what a
  `timestamptz` is cast to a date or a `timestamp` in. The driver scans every
  `timestamptz`, alone or in an array, a range or a multirange, into a `time.Time`
  in that location: the instant is unchanged, only its zone. A name other than
  `"UTC"` needs the time zone database on the host, or `time/tzdata` imported.
  Default: the server's `TimeZone`, and scans in the process's local zone.
- `MaxConns` (`int32`) — Maximum number of connections in the pool. Default:
  `max(4, runtime.NumCPU())`.
- `MinConns` (`int32`) — Connections the pool keeps open even when idle. Must not
  be greater than `MaxConns` when `MaxConns` is set. Default: `0`.
- `MaxConnLifetime` (`time.Duration`) — How long a connection may live before it
  is replaced. Default: `time.Hour`.
- `MaxConnIdleTime` (`time.Duration`) — How long a connection may sit idle before
  it is closed. Default: `30 * time.Minute`.
- `HealthCheckPeriod` (`time.Duration`) — How often idle connections are checked
  against the two limits above and `MinConns`. Default: `time.Minute`.
- `ConnectTimeout` (`time.Duration`) — Bounds each attempt to open a connection,
  TLS handshake and authentication included, on top of the context's deadline.
  Default: `2 * time.Minute` for a pooled connection, the context alone for a
  `Listener`'s.
- `TLS` (`integration.ConfigTLS`) — TLS settings. See [TLS](#tls).
- `OnNotification` (`func(*pgconn.Notification)`) — **Deprecated**: use
  [`Listen`](#listen--notify). Called when a pooled connection happens to read a
  notification, which is only while it runs a statement.
- `OnWrite` (`func(context.Context)`) — Called each time the database has durably
  kept something written through the pool, with the statement's context. See
  [Knowing what was written](#knowing-what-was-written). Default: none.

A pool or timeout field left at zero keeps the driver's default; a negative one is
a validation error. In JSON, the durations are integer nanoseconds.

### TLS

With `TLS.Enabled`, every connection uses TLS or fails. There is no fallback to
plaintext, whatever `PGSSLMODE` says in the environment, and the server's
certificate is verified:
- against `TLS.RootCAPEMs` when set, and the system's roots otherwise;
- for the server name `TLS.ServerName` when set, and otherwise the host of
  `Address`, which is also sent as SNI.

`TLS.InsecureSkipVerify` keeps the encryption and drops the verification. A
client certificate is presented when `TLS.CertPEM` and `TLS.KeyPEM` are set.

With TLS disabled, connections are plaintext. The driver's own default — try TLS
without verifying the certificate, then fall back to plaintext — is never used: a
connection is either verified TLS or knowingly plaintext. A server that only
accepts TLS (`hostssl` in `pg_hba.conf`) therefore requires `TLS.Enabled`.

## Usage

### Connecting

```go
import (
  "context"
  "os"
  "os/signal"
  "syscall"

  "github.com/mountayaapp/helix.go/integration/postgres"
  "github.com/mountayaapp/helix.go/service"
)

func run() error {
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

  svc, err := service.New(ctx, service.Config{})
  if err != nil {
    return err
  }

  db, err := postgres.Connect(ctx, svc, postgres.Config{
    Name:     "primary",
    Address:  "127.0.0.1:5432",
    Database: "my_db",
    User:     "username",
    Password: "password",
  })
  if err != nil {
    return err
  }

  // Hand db to the servers that use it. Its lifecycle belongs to svc.
  _ = db

  // Run blocks until ctx is cancelled, then closes the pool along with the rest
  // of the Service.
  return svc.Run(ctx)
}
```

`Connect` opens one connection and pings the database before returning. It
returns:
- a `VALIDATION_FAILED` error, before anything is opened or dialed, when the
  `Config` is not valid — TLS settings included;
- a `SERVICE_UNAVAILABLE` error, `"Failed to connect to PostgreSQL"`, when the
  database cannot be reached or refuses the credentials, or the TLS handshake
  fails. The driver's message is in the entry's `reason` extension;
- the Service's error when it refuses the connection, such as for a `Name` already
  attached.

On every error, whatever `Connect` opened is closed again.

### Querying

Every method takes a connection from the pool for the duration of the work, and is
safe for concurrent use. Errors are pgx's own, unwrapped: `errors.As(err,
&pgErr)` with a `*pgconn.PgError` reaches the server's error code.

```go
// Query multiple rows. The rows hold a connection until closed.
rows, err := db.Query(ctx, "SELECT id, name FROM users WHERE active = $1", true)
if err != nil {
  // ...
}
defer rows.Close()

for rows.Next() {
  var id string
  var name string
  if err := rows.Scan(&id, &name); err != nil {
    // ...
  }
}
if err := rows.Err(); err != nil {
  // ...
}

// Query a single row. Scan returns pgx.ErrNoRows when there is none.
var name string
err = db.QueryRow(ctx, "SELECT name FROM users WHERE id = $1", "usr_123").Scan(&name)
if err != nil {
  // ...
}

// Execute a statement (INSERT, UPDATE, DELETE).
tag, err := db.Exec(ctx, "UPDATE users SET active = $1 WHERE id = $2", false, "usr_123")
if err != nil {
  // ...
}
```

### Batches

`SendBatch` sends every queued statement in one round trip. The results are read
in queue order, and must be closed:

```go
batch := &pgx.Batch{}
batch.Queue("INSERT INTO events (id) VALUES ($1)", "evt_1")
batch.Queue("INSERT INTO events (id) VALUES ($1)", "evt_2")

if err := db.SendBatch(ctx, batch).Close(); err != nil {
  // ...
}
```

### Bulk loading

`CopyFrom` loads rows with the `COPY` protocol, all or nothing:

```go
copied, err := db.CopyFrom(ctx,
  pgx.Identifier{"events"},
  []string{"id", "name"},
  pgx.CopyFromRows([][]any{{"evt_1", "created"}, {"evt_2", "deleted"}}),
)
```

### Transactions

```go
import (
  "github.com/jackc/pgx/v5"
)

tx, err := db.BeginTx(ctx, pgx.TxOptions{})
if err != nil {
  // ...
}

// Safe to defer — Rollback is a no-op if Commit was already called.
defer tx.Rollback(ctx)

_, err = tx.Exec(ctx, "UPDATE accounts SET balance = balance - $1 WHERE id = $2", 100, "acc_1")
if err != nil {
  // ...
}

_, err = tx.Exec(ctx, "UPDATE accounts SET balance = balance + $1 WHERE id = $2", 100, "acc_2")
if err != nil {
  // ...
}

err = tx.Commit(ctx)
if err != nil {
  // ...
}
```

A transaction holds its connection until `Commit` or `Rollback`. Unlike
`database/sql`, `ctx` only affects the `BEGIN`: cancelling it does not roll the
transaction back. A `Tx` also has `Prepare`, `Query`, `QueryRow`, `SendBatch` and
`CopyFrom`, which run inside the transaction.

Nested transactions are supported via savepoints:

```go
subtx, err := tx.Begin(ctx)
```

### Knowing what was written

`OnWrite` tells a caller that the database has durably kept something, which is
what a request needs to know before it can tell a client that retrying it is
safe. It is called with the context of the statement that got the write kept, so
a value the request put in its context is visible to it:

```go
type writtenKey struct{}

db, err := postgres.Connect(ctx, svc, postgres.Config{
  // ...
  OnWrite: func(ctx context.Context) {
    if written, ok := ctx.Value(writtenKey{}).(*atomic.Bool); ok {
      written.Store(true)
    }
  },
})
```

It is called:
- once for every transaction that commits, a read-only one included;
- once for a statement, a batch or a copy run outside of a transaction that
  inserted, updated, deleted, merged or copied at least one row.

It is never called for a statement that failed, a rollback, a read, a write
inside a transaction that has not committed yet, or the savepoint a nested
transaction's `Commit` releases. A `COMMIT` the server turns into a rollback,
because a statement of the transaction failed, is not reported either.

Outside of a transaction, the statement's command tag is what tells a write from
a read. A write the tag does not report goes unseen there: one made by a function
a `SELECT` calls, one under a data-modifying `WITH` whose top-level statement is a
`SELECT`, or one made by a statement other than the last of a multi-statement
string. Run such writes in a transaction: its `COMMIT` is always seen. A `COMMIT`
whose answer never arrived, on a connection lost at that moment, is not reported
although the server may have applied it.

`OnWrite` runs synchronously, on the goroutine that ran the statement, before the
call that finished it returns: `Exec`, `Commit`, the `Close` of the Rows of a
`Query` or of the BatchResults of a `SendBatch`, the `Scan` of the Row of a
`QueryRow`. Keep it quick, and do not let it panic.

### LISTEN / NOTIFY

`Listen` opens a connection of its own, outside of the pool, and listens on one or
more channels with it. Notifications are read as they arrive:

```go
listener, err := db.Listen(ctx, "events")
if err != nil {
  return err
}
defer listener.Close(ctx)

for {
  notif, err := listener.WaitForNotification(ctx)
  if err != nil {
    return err
  }

  // notif.Channel, notif.Payload
}
```

- A channel name is matched exactly, case included: it is the name
  `pg_notify(channel, payload)` addresses. An unquoted `NOTIFY Events` folds the
  name to `events`.
- When `ctx` ends, `WaitForNotification` returns its error and the Listener stays
  usable. When the Listener is closed — including by a `Close` from another
  goroutine, which interrupts the wait — it returns `postgres.ErrClosed`. Any
  other error means the connection failed: close the Listener and listen again.
- The server does not queue notifications for a channel nobody listens on: those
  sent while no Listener is connected are lost.
- `Close` closes the Listener's connection and nothing else. Stopping the Service
  closes every Listener still open; `Listen` then returns `postgres.ErrClosed`.

The Listener's connection uses the same address, credentials and TLS settings as
the pool, but does not count towards `MaxConns` or the pool's metrics.

## Health

`CheckHealth` takes a connection from the pool and pings the database. When it
fails, the Service's readiness probe answers a generic `503` with no detail; the
failure, with the driver's message in its `reason`, goes to the log and the span.
The ping is not traced, and neither is the one `Connect` makes: a readiness probe
polled every few seconds would otherwise fill the traces with its own spans.

## Shutdown

When the Service stops, every open Listener is closed, then the pool. Closing the
pool waits for each connection still in use — rows not closed, a transaction not
committed or rolled back — to be released. That wait is bounded by the Service's
shutdown budget: past it, the dependency reports an error and the pool finishes
closing in the background, as connections are released.

## Telemetry

### Traces

Every span is a `client` span. The facade's methods are named after themselves:

- `PostgreSQL: Exec`
- `PostgreSQL: Query` — ends when the rows are closed
- `PostgreSQL: QueryRow` — ends when the row is scanned
- `PostgreSQL: Batch` — ends when the batch results are closed
- `PostgreSQL: Copy From`
- `PostgreSQL: Listen`
- `PostgreSQL: Transaction / Begin` — for a transaction and a nested one alike
- `PostgreSQL: Transaction / Commit`
- `PostgreSQL: Transaction / Rollback`
- `PostgreSQL: Transaction / Exec`
- `PostgreSQL: Transaction / Query`
- `PostgreSQL: Transaction / QueryRow`
- `PostgreSQL: Transaction / Prepare`
- `PostgreSQL: Transaction / Batch`
- `PostgreSQL: Transaction / Copy From`

And the work pgx does underneath them:

- `PostgreSQL: Pool / Acquire` — taking a connection from the pool, including the
  wait when none is free
- `PostgreSQL: Connect` — opening a connection, which the pool does lazily
- `PostgreSQL: Prepare` — pgx preparing a statement for its cache before a
  parameterised query or a copy

Every span carries:
- `postgres.name`
- `postgres.database`

When applicable, these are carried as well:
- `postgres.query` — the statement as written, placeholders included. The
  arguments bound to it are never recorded. Its length is not bounded by the
  integration: set `OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT` to cap it.
- `postgres.rows` — rows the statement returned or affected, as the server's
  command tag reports them, on a statement that succeeded and on a copy
- `postgres.statement` — the prepared statement's name
- `postgres.already_prepared` — whether a prepare found the statement already on
  the connection
- `postgres.batch.size` — statements in a batch
- `postgres.table` and `postgres.columns` — the target of a copy

A failure is recorded on the span as an error. In a batch, each statement that
failed is recorded on the batch's span. The error is the driver's, and
PostgreSQL's message can quote the input it refused (`invalid input syntax for
type uuid: "…"`): register a function with `trace.SetErrorSanitizer` to decide
what of an error's text a span records.

Example:
```
postgres.name: "primary"
postgres.database: "my_db"
postgres.query: "SELECT id, username FROM users WHERE id = $1"
postgres.rows: 1
```

Waiting for a notification is not traced: a wait can last for hours.

### Metrics

- `postgres.operation.duration` (histogram, seconds) — duration of every span
  above, with the attributes `postgres.name`, `postgres.operation` (the span's
  name without the `PostgreSQL: ` prefix, such as `Exec` or `Pool / Acquire`) and
  `postgres.outcome` (`ok` or `error`).
- `postgres.pool.connections` (gauge, `{connection}`) — connections held by the
  pool, with the attributes `postgres.name` and `postgres.pool.state`: `idle`,
  `acquired` or `constructing`.
- `postgres.pool.connections.max` (gauge, `{connection}`) — the pool's maximum
  size, with the attribute `postgres.name`.
- `postgres.pool.acquire.waits` (counter, `{acquire}`) — acquires that found no
  free connection and had to wait for one, with the attribute `postgres.name`.
- `postgres.pool.acquire.wait.duration` (counter, seconds) — total time those
  acquires waited, with the attribute `postgres.name`.

The pool's instruments are observed on collection, so an idle service still
reports the shape of its pool. A Listener's connection is not counted in them.

## Security

- With `TLS.Enabled`, no connection is ever plaintext, and the certificate is
  verified for the host dialed unless `TLS.InsecureSkipVerify` is set. See
  [TLS](#tls).
- Credentials and the database name are percent-encoded into the connection URI,
  so no value can add a connection parameter of its own.
- A channel name given to `Listen` is quoted as an identifier: it cannot carry SQL.
- `postgres.query` records the SQL text, never the arguments. Pass values as
  arguments (`$1`) rather than in the SQL text to keep them out of traces.
- Errors returned by the facade are the driver's own, and routinely name the host,
  the user or the database. Do not send them to a client as they are.
