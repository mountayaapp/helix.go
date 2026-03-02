# helix.go - PostgreSQL integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/postgres)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/postgres)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/postgres)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The PostgreSQL integration provides an opinionated way to interact with PostgreSQL
as an OLTP database. It is a **dependency** integration registered via
`service.Attach()`.

The integration works with all PostgreSQL-compatible databases, such as:
- [Citus](https://www.citusdata.com/)
- [Timescale](https://www.timescale.com/)
- [CockroachDB](https://www.cockroachlabs.com/)
- [YugabyteDB](https://www.yugabyte.com/)
- [Neon](https://neon.tech/)
- [Google AlloyDB](https://cloud.google.com/alloydb)
- [Amazon Aurora](https://aws.amazon.com/rds/aurora/)

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

## Usage

Install the Go module with:
```sh
$ go get github.com/mountayaapp/helix.go/integration/postgres
```

Simple example on how to import, configure, and use the integration:

```go
import (
  "context"

  "github.com/mountayaapp/helix.go/integration/postgres"
)

cfg := postgres.Config{
  Address:  "127.0.0.1:5432",
  Database: "my_db",
  User:     "username",
  Password: "password",
}

db, err := postgres.Connect(cfg)
if err != nil {
  return err
}

ctx := context.Background()
rows, err := db.Query(ctx, "QUERY", args...)
if err != nil {
  // ...
}

defer rows.Close()
for rows.Next() {
  // ...
}
```
