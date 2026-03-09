# helix.go - PostgreSQL integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/postgres)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/postgres)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/postgres)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The PostgreSQL integration provides an opinionated way to interact with PostgreSQL
as an OLTP database. It is a **dependency** integration — calling
`postgres.Connect(svc, cfg)` automatically registers it via `service.Attach()`.

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

- `Address` (`string`) — PostgreSQL server address. Default: `"127.0.0.1:5432"`.
- `Database` (`string`) — Database name. **Required**.
- `User` (`string`) — Username. **Required**.
- `Password` (`string`) — Password. **Required**.
- `TLS` (`integration.ConfigTLS`) — TLS settings.
- `OnNotification` (`func(*pgconn.Notification)`) — Callback for PostgreSQL
  LISTEN/NOTIFY notifications.

## Usage

### Connecting

```go
import (
  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/postgres"
)

svc, err := service.New()
if err != nil {
  panic(err)
}

db, err := postgres.Connect(svc, postgres.Config{
  Address:  "127.0.0.1:5432",
  Database: "my_db",
  User:     "username",
  Password: "password",
})
if err != nil {
  panic(err)
}
```

### Querying

```go
// Query multiple rows.
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

// Query a single row.
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

Nested transactions are supported via savepoints:

```go
subtx, err := tx.Begin(ctx)
```

## Trace attributes

The `postgres` integration sets the following trace attributes:
- `postgres.database`

When applicable, these attributes can be set as well:
- `postgres.query`
- `postgres.transaction.query`

Example:
```
postgres.database: "my_db"
postgres.query: "SELECT id, username FROM users;"
```
