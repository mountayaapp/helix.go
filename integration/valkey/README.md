# helix.go - Valkey integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/valkey)
[![Go Report Card](https://goreportcard.com/badge/github.com/mountayaapp/helix.go/integration/valkey)](https://goreportcard.com/report/github.com/mountayaapp/helix.go/integration/valkey)
[![GitHub Release](https://img.shields.io/github/v/release/mountayaapp/helix.go)](https://github.com/mountayaapp/helix.go/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The Valkey integration provides an opinionated way to interact with
[Valkey](https://valkey.io/) (Redis-compatible) as an in-memory key/value store.
It is a **dependency** integration — calling `valkey.Connect(svc, cfg)`
automatically registers it via `service.Attach()`.

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/valkey
```

## Configuration

- `Address` (`string`) — Valkey server address. Default: `"127.0.0.1:6379"`.
- `User` (`string`) — Username for authentication.
- `Password` (`string`) — Password for authentication.
- `TLS` (`integration.ConfigTLS`) — TLS settings.

## Usage

### Connecting

```go
import (
  "github.com/mountayaapp/helix.go/service"
  "github.com/mountayaapp/helix.go/integration/valkey"
)

svc, err := service.New()
if err != nil {
  panic(err)
}

store, err := valkey.Connect(svc, valkey.Config{
  Address: "127.0.0.1:6379",
})
if err != nil {
  panic(err)
}
```

### Get / Set

```go
ctx := context.Background()

// Write a key with a 5-minute TTL.
err = store.Set(ctx, "user:123", []byte(`{"name":"Alice"}`), &valkey.OptionsSet{
  TTL: 5 * time.Minute,
})
if err != nil {
  // ...
}

// Read a key.
value, err := store.Get(ctx, "user:123", nil)
if err != nil {
  // ...
}

var data map[string]any
err = json.Unmarshal(value, &data)
if err != nil {
  // ...
}
```

### Multi-get

```go
entries, err := store.MGet(ctx, []string{"user:123", "user:456", "user:789"})
if err != nil {
  // ...
}

for _, entry := range entries {
  // entry.Key, entry.Value
  _ = entry
}
```

### Scanning keys

```go
keys, err := store.Scan(ctx, "user:*")
if err != nil {
  // ...
}
```

### Increment / Decrement

```go
err = store.Increment(ctx, "page:views", 1)
err = store.Decrement(ctx, "stock:item_42", 1)
```

## Trace attributes

The `valkey` integration sets the following trace attributes:
- `valkey.key`

Example:
```
valkey.key: "user:123"
```
