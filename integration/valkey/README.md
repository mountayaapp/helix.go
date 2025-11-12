# helix.go - Valkey integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/valkey)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/valkey)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/valkey)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The Valkey integration provides an opinionated way to interact with Valkey as
key/value database.

## Trace attributes

The `valkey` integration sets the following trace attributes:
- `span.kind`

When applicable, these attributes can be set as well:
- `valkey.key`

Example:
```
valkey.key: "helloworld"
span.kind: "client"
```

## Usage

Install the Go module with:
```sh
$ go get github.com/mountayaapp/helix.go/integration/valkey
```

Simple example on how to import, configure, and use the integration:

```go
import (
  "context"

  "github.com/mountayaapp/helix.go/integration/valkey"
)

cfg := valkey.Config{
  Address: "127.0.0.1:6379",
}

store, err := valkey.Connect(cfg)
if err != nil {
  return err
}

ctx := context.Background()
value, err := store.Get(ctx, "my_key", &valkey.OptionsGet{})
if err != nil {
  // ...
}

var anything types.AnyType
err = json.Unmarshal(value, &anything)
if err != nil {
  // ...
}
```
