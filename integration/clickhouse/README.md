# helix.go - ClickHouse integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/clickhouse)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/clickhouse)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/clickhouse)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The ClickHouse integration provides an opinionated way to interact with ClickHouse
as OLAP database. As of today, at [Mountaya](https://mountaya.com/), our Go
services only perform batch writes to ClickHouse. Reading is done solely for
analytical purposes by third-party applications.

## Trace attributes

The `clickhouse` integration sets the following trace attributes:
- `clickhouse.database`
- `span.kind`

Example:
```
clickhouse.database: "my_db"
span.kind: "client"
```

## Usage

Install the Go module with:
```sh
$ go get github.com/mountayaapp/helix.go/integration/clickhouse
```

Simple example on how to import, configure, and use the integration:

```go
import (
  "context"
  "time"

  "github.com/mountayaapp/helix.go/integration/clickhouse"
)

type clickhouseEntry struct {
  Date time.Time `ch:"date"`
}

cfg := clickhouse.Config{
  Address:  "endpoint.clickhouse.cloud:9440",
  Database: "default",
  User:     "default",
  Password: "default",
  TLS: integration.ConfigTLS{
    Enabled:            true,
    InsecureSkipVerify: true,
  },
}

db, err := clickhouse.Connect(cfg)
if err != nil {
  return err
}

ctx := context.Background()
batch, err := db.NewBatchInsert(ctx, "tablename")
if err != nil {
  // ...
}

for i := 0; i < 10_000; i++ {
  data := &clickhouseEntry{
    Date: time.Now().UTC(),
  }

  err = batch.AppendStruct(ctx, data)
  if err != nil {
    // ...
  }
}

err = batch.Send(ctx)
if err != nil {
  // ...
}
```
