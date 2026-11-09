# helix.go - Valkey integration

[![Go API reference](https://pkg.go.dev/badge/github.com/mountayaapp/helix.go.svg)](https://pkg.go.dev/github.com/mountayaapp/helix.go/integration/valkey)
[![Latest version](https://img.shields.io/github/v/tag/mountayaapp/helix.go?filter=v*&sort=semver&label=version)](https://github.com/mountayaapp/helix.go/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](https://opensource.org/licenses/MIT)

The Valkey integration provides an opinionated way to interact with
[Valkey](https://valkey.io/) (Redis-compatible) as an in-memory key/value store,
pub/sub broker and stream log, on top of
[valkey-go](https://github.com/valkey-io/valkey-go). It is a **dependency**
integration: calling `valkey.Connect(ctx, svc, cfg)` connects to the server and
registers the connection with the Service through `service.Attach()`. The Service
owns its lifecycle from then on; the `valkey.Valkey` you get back can send
commands, not close the connection.

It works the same against a standalone server and a cluster: when the server at
`Address` runs in cluster mode, the client discovers the other nodes from it,
commands spanning several keys are split by hash slot, and scans walk every node.

## Installation

```sh
$ go get github.com/mountayaapp/helix.go/integration/valkey
```

## Configuration

- `Name` (`string`) — Identifies the connection in health output, error context and
  telemetry (`valkey.name`). Two connections attached to the same Service must have
  different names. Default: `"valkey"`.
- `Address` (`string`) — Server address, as `"host:port"`. Default:
  `"127.0.0.1:6379"`.
- `User` (`string`) — Username for authentication.
- `Password` (`string`) — Password for authentication.
- `Database` (`int`) — Numbered database every connection selects. A server in
  cluster mode only accepts `0` before Valkey 9. Default: `0`.
- `ClientName` (`string`) — Name every connection registers with the server, as
  reported by `CLIENT LIST`. Default: none.
- `DialTimeout` (`time.Duration`) — Bounds opening a connection: dialing it, and
  then the handshake on it, are each given this long. Default: the driver's, 5
  seconds.
- `WriteTimeout` (`time.Duration`) — Bounds how long a connection waits for the
  server to answer the commands written to it, beyond which it is considered dead
  and closed. Blocking commands are exempt. Default: the driver's, 10 seconds.
- `RecordKeys` (`bool`) — Records the keys, key patterns, stream and channel names
  a command is given on its span. See [Traces](#traces). Default: `false`.
- `TLS` (`integration.ConfigTLS`) — TLS settings. See [TLS](#tls).

A negative `Database` or timeout is a validation error. In JSON, the durations are
integer nanoseconds; `User`, `Password` and the TLS key material are never
serialized.

### TLS

With `TLS.Enabled`, every connection uses TLS, and the server's certificate is
verified:
- against `TLS.RootCAPEMs` when set, and the system's roots otherwise;
- for the server name `TLS.ServerName` when set, and otherwise the host of the
  address dialed, which is also sent as SNI. In a cluster, that is the address
  each node announces.

`TLS.InsecureSkipVerify` keeps the encryption and drops the verification. A client
certificate is presented when `TLS.CertPEM` and `TLS.KeyPEM` are set.

A TLS configuration that does not convert — a key pair that does not parse, a root
certificate that is not PEM — fails `Connect` before anything is dialed. Nothing is
ever sent in plaintext in its place, credentials included.

## Usage

### Connecting

```go
import (
  "context"
  "os"
  "os/signal"
  "syscall"

  "github.com/mountayaapp/helix.go/integration/valkey"
  "github.com/mountayaapp/helix.go/service"
)

func run() error {
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()

  svc, err := service.New(ctx, service.Config{})
  if err != nil {
    return err
  }

  store, err := valkey.Connect(ctx, svc, valkey.Config{
    Name:    "cache",
    Address: "127.0.0.1:6379",
  })
  if err != nil {
    return err
  }

  // Hand store to the servers that use it. Its lifecycle belongs to svc.
  _ = store

  // Run blocks until ctx is cancelled, then closes the connection along with the
  // rest of the Service.
  return svc.Run(ctx)
}
```

`Connect` connects and authenticates before returning, and returns once `ctx` is
done, whatever the connection's own timeouts. It returns:
- a `VALIDATION_FAILED` error, before anything is dialed, when the `Config` is not
  valid — TLS settings included — or the Service is nil;
- a `SERVICE_UNAVAILABLE` error, `"Failed to connect to Valkey"`, when the server
  cannot be reached, refuses the credentials, the TLS handshake fails or `ctx` is
  done first. The driver's message is in the entry's `reason` extension;
- the Service's error when it refuses the connection, such as for a `Name` already
  attached.

On every error, whatever `Connect` opened is closed again.

Every method is safe for concurrent use. Concurrent commands are pipelined on a
few shared connections.

### Get / Set

```go
// Write a key with a 5-minute TTL.
err = store.Set(ctx, "user:123", []byte(`{"name":"Alice"}`), &valkey.OptionsSet{
  TTL: 5 * time.Minute,
})

// Read a key.
value, err := store.Get(ctx, "user:123", nil)
if valkey.IsNotFound(err) {
  // The key does not exist.
}
```

A missing key returns `valkey.ErrNotFound`; test for it with `valkey.IsNotFound`
or `errors.Is`. It is not recorded as an error on the span unless
`OptionsGet.ErrorRecordOnNotFound` is set. Every other failure is.

The values passed to `Set`, `SetNX`, `CompareAndSet` and `Publish` are copied
before the call returns, so the caller may reuse its buffer at once. The value
`Get` returns is the caller's own.

### TTLs

TTLs are sent in milliseconds (`PX`, `PEXPIRE`), rounded up, so a positive TTL
below a millisecond still sets one.

- `OptionsSet.TTL` of zero or less — or nil options — stores the key without
  expiry. Like any write of a whole value, that also clears a TTL the key had.
- `Expire` with a TTL of zero or less is a `VALIDATION_FAILED` error and leaves the
  key alone. Use `Delete` to delete a key.

```go
// Set a key only if it does not exist yet: a lock, an idempotency marker.
won, err := store.SetNX(ctx, "lock:job_42", []byte("worker_1"), &valkey.OptionsSet{
  TTL: 30 * time.Second,
})

// Refresh a key's TTL — a fixed-window rate limiter, for example.
err = store.Expire(ctx, "ratelimit:usr_123", time.Minute)
```

### Counters

```go
views, err := store.Increment(ctx, "page:views", 1)
stock, err := store.Decrement(ctx, "stock:item_42", 1)
```

Both return the value they left. A missing key counts as `0`.

`IncrementTTL` is the counter of a fixed window, a rate limiter's typically. It
increments like `Increment`, and gives the key a TTL when it has none, sending
`INCRBY` and `PEXPIRE … NX` in one round trip:

```go
count, err := store.IncrementTTL(ctx, "ratelimit:usr_123", 1, time.Minute)
```

- The increment creating the key sets its TTL. Later increments leave it alone, so
  the window does not slide.
- A key without a TTL, whatever left it so, gets one on its next increment: a
  counter is never left in place for good.
- A TTL of zero or less is a `VALIDATION_FAILED` error, and nothing is sent.
- The `NX` option of `PEXPIRE` needs Valkey, or Redis 7.0 or later. An older
  server fails every call.

### Compare and set

`CompareAndSet` and `CompareAndDelete` change a key only while it holds the value
the caller expects, in one atomic step on the server. They are how the holder of
a claim taken with `SetNX` replaces or releases it without touching a claim taken
by someone else since its own expired:

```go
// Take the claim.
won, err := store.SetNX(ctx, "claim:job_42", token, &valkey.OptionsSet{TTL: 30 * time.Second})

// Replace it with the result, only if it is still ours.
replaced, err := store.CompareAndSet(ctx, "claim:job_42", token, result, time.Hour)

// Or release it, only if it is still ours.
released, err := store.CompareAndDelete(ctx, "claim:job_42", token)
```

- Both return `false`, and no error, when the key holds anything else. A missing
  key holds nothing, and matches no expected value, an empty one included.
- The TTL of `CompareAndSet` is applied like `OptionsSet.TTL`: zero or less stores
  the value without expiry, clearing the one the key had.
- Both run as Lua scripts, sent with `EVALSHA`, and with `EVAL` when the server
  does not know them yet. A server with scripting disabled, or an ACL denying
  `EVAL` and `EVALSHA`, refuses them.

### Several keys

```go
entries, err := store.MGet(ctx, []string{"user:123", "user:456", "user:789"})
for _, entry := range entries {
  // entry.Key, entry.Value
}

err = store.Delete(ctx, []string{"user:123", "user:456"})
```

`MGet` returns the keys that exist, in the order they were given; a key holding an
empty value is returned with it. In a cluster, `MGet` sends one `MGET` per hash
slot and `Delete` one `DEL` per key, each batch in a single round trip, so a
failing `Delete` may leave some of the keys deleted.

### Scanning keys

```go
keys, err := store.Scan(ctx, "user:*", &valkey.OptionsScan{
  Limit: 10_000,
})
```

`Scan` returns the keys matching a glob-style pattern — an empty one matches every
key — once each, walking every node of a cluster. `OptionsScan.Count` is how many
keys the server examines per round trip (default `1000`); `OptionsScan.Limit` caps
the result and stops the scan, so a result of exactly `Limit` keys may not be every
match. Without a limit, every match is held in memory at once. Scanning visits the
whole key space whatever the pattern: it is for maintenance and tooling, not for a
request path. To list a group of keys on a request path, index them in a set.

### Sets

```go
added, err := store.SAdd(ctx, "room:42:members", []string{"room:42:user:7"}, &valkey.OptionsSAdd{
  TTL: time.Minute,
})

members, err := store.SMembers(ctx, "room:42:members")
entries, err := store.MGet(ctx, members)

removed, err := store.SRem(ctx, "room:42:members", []string{"room:42:user:7"})
```

`SAdd` returns how many members were new, and `SRem` how many were there; removing
the last member deletes the set. `SMembers` returns the members in no particular
order, and an empty list for a missing set. `OptionsSAdd.TTL`, when positive, sets
the set's time-to-live in the same round trip as the members, on every call: a set
indexing keys that expire on their own lives as long as they are written, and
expires once nobody writes. Members whose keys have expired stay in the set until
removed, so read them back through `MGet`, which leaves a missing key out.

A set that can grow large is read a page at a time with `SScan`, so only a page is
held in memory, whatever the set's size:

```go
var cursor uint64
for {
  page, next, err := store.SScan(ctx, "jobs:pending", cursor, &valkey.OptionsSScan{
    Count: 100,
  })
  if err != nil {
    return err
  }

  for _, member := range page {
    // ...
  }

  cursor = next
  if cursor == 0 {
    break
  }
}
```

`SCard` returns how many members a set holds without reading them, `0` for a
missing set. A scan starts at cursor `0` and is done once the cursor returned is
`0` again; a missing set is done at once. `OptionsSScan.Count` is how many
members the server examines per page (default `100`), a hint: a small set comes
back whole in one page. `OptionsSScan.Match` keeps the members matching a
glob-style pattern, after the page is read, so a page may be empty while the
scan is not done. A member in the set for the whole scan is returned at least
once and may be returned more than once; one added or removed during the scan
may be returned or not. The set is one key, so in a cluster the scan goes to
the node holding it.

### Pub/Sub

```go
// Subscribe blocks until ctx is done: run it in a goroutine.
go func() {
  err := store.Subscribe(ctx, "events", func(ctx context.Context, msg valkey.PubSubMessage) {
    // msg.Channel, msg.Payload. ctx carries the message's span.
  })
  if err != nil {
    // The subscription ended for another reason than ctx.
  }
}()

err = store.Publish(ctx, "events", []byte("hello"))
```

Subscriptions share the client's pipelined connection, and every `Subscribe` of one
channel shares one server subscription: the first opens it and the last to leave
unsubscribes it. The handler is called on `Subscribe`'s goroutine, one message at a
time, and messages are queued for it without waiting, so a slow handler holds up no
other subscription and no command. A subscriber further behind than
`Config.SubscriberBacklog` messages (default `1024`) or
`Config.SubscriberBacklogBytes` bytes (default 32 MiB) is ended with
`ErrSubscriberTooSlow`, and the `ctx` handed to its handler is done from then on.

It returns `nil` once `ctx` is done. A subscription whose connection is lost is not
resumed in silence: what was published in between is gone, so `Subscribe` returns
`ErrSubscriptionInterrupted`, as it does when the server ends the subscription or
the Service closes the connection at shutdown. Messages are only delivered to the
subscribers connected when they are published, so a caller that must not miss one
subscribes again and reads what it missed from a durable source, such as a stream.

### Streams

```go
id, err := store.XAdd(ctx, "orders", 10_000, map[string]string{
  "order_id": "ord_42",
  "status":   "created",
})

entries, err := store.XRange(ctx, "orders", "-", "+", &valkey.OptionsXRange{
  Count: 100,
})
```

`XAdd` appends an entry and returns its id; with a `maxLen` above zero the stream
is capped to about that many entries (`MAXLEN ~`). Fields are sent in sorted
order, so an entry reads back the same way every time, and an empty map is a
`VALIDATION_FAILED` error. `XRange` reads the inclusive range `[start, end]`: `-`
and `+` for the whole stream, `(id` to resume after a known id.
`OptionsXRange.Count` caps the entries returned.

### Other commands

`B` and `Do` send the commands the interface does not cover, with valkey-go's
command builder:

```go
fields, err := store.Do(ctx, store.B().Hgetall().Key("user:123").Build()).AsStrMap()
```

`Do` is traced like every other method, and its nil reply is not recorded as an
error. A command must not be reused after `Do`, and in a cluster, building a
command whose keys fall in different hash slots panics. The connection itself, and
with it closing it, stays with the Service.

### Automatic persisted queries

`QueryCache` is the Valkey-backed automatic-persisted-query (APQ) cache for the
[GraphQL integration](../graphql): it maps the hash a client sends in place of a
query to the query it stands for. Its methods are those of gqlgen's
`graphql.Cache[string]`, so it is passed to the GraphQL integration as it is:

```go
cache := valkey.NewQueryCache(store, valkey.OptionsQueryCache{})
```

- `Prefix` (`string`) — Prepended to every key the cache stores. Default:
  `"apq:"`.
- `TTL` (`time.Duration`) — How long a query stays cached after it was last added.
  Default: `time.Hour`.
- `MaxQueryBytes` (`int`) — Size above which a query is not cached. Any client can
  have a query of its choosing stored, so this bounds what one request can make the
  cache hold. A query left uncached still runs: its client is told the hash is
  unknown and sends the query in full again. Default: `65536` (64 KiB).

A failure to read or write the cache never fails a request: APQ degrades to the
client sending its query in full. The failure is recorded on the span and logged as
a warning.

## Health

`CheckHealth` sends a `PING`. When it fails, the Service's readiness probe answers
a generic `503` with no detail; the failure, with the driver's message in its
`reason`, goes to the log and the span. The ping is not traced: a readiness probe
polled every few seconds would otherwise fill the traces with its own spans.

## Shutdown

When the Service stops, the connection is closed: further commands fail, and
`Close` waits for the commands in flight to finish. That wait is bounded by the
Service's shutdown budget. A running `Subscribe` returns an error.

## Telemetry

### Traces

Every method but `B` opens one span, named after itself:

- `Valkey: Exists`, `Valkey: Get`, `Valkey: Set`, `Valkey: SetNX`,
  `Valkey: Increment`, `Valkey: Decrement`, `Valkey: IncrementTTL`,
  `Valkey: CompareAndSet`, `Valkey: CompareAndDelete`, `Valkey: Expire`,
  `Valkey: Scan`, `Valkey: MGet`, `Valkey: Delete`, `Valkey: SAdd`,
  `Valkey: SRem`, `Valkey: SMembers`, `Valkey: SScan`, `Valkey: SCard`,
  `Valkey: XAdd`, `Valkey: XRange`, `Valkey: Do` — `client` spans.
- `Valkey: Publish` — a `producer` span.
- `Valkey: Subscribe` — a `client` span covering the subscription's setup, from
  sending `SUBSCRIBE` to the server confirming it.
- `Valkey: Receive` — a `consumer` span per message, around the handler, whose
  `ctx` carries it. It is a child of the `ctx` given to `Subscribe`: a message
  carries no trace context of its own.

Every span carries:
- `valkey.name`
- `valkey.address`
- `valkey.database`

`Valkey: Do` also carries `valkey.command`, the command's name, such as `HGETALL`.

With `Config.RecordKeys`, what a command is given is recorded as well:
- `valkey.key` — the key, or the stream of `XAdd` and `XRange`
- `valkey.keys` — the keys of `MGet` and `Delete`
- `valkey.pattern` — the pattern of `Scan`, and the `Match` of `SScan`
- `valkey.channel` — the channel of `Publish`, `Subscribe` and `Receive`

`RecordKeys` is off by default: keys and channels routinely embed session tokens,
user identifiers and email addresses, which do not belong in a tracing backend.
Values and payloads are never recorded.

A failure is recorded on the span as an error. A missing key is not, unless
`OptionsGet.ErrorRecordOnNotFound` says so; neither is a nil reply to `Do`.

Example:
```
valkey.name: "cache"
valkey.address: "127.0.0.1:6379"
valkey.database: 0
valkey.key: "user:123"
```

### Metrics

- `valkey.operation.duration` (histogram, seconds) — duration of every span above
  but `Valkey: Receive`, whose time is the handler's rather than Valkey's, with the
  attributes `valkey.name`, `valkey.operation` (the span's name without the
  `Valkey: ` prefix, such as `Get`) and `valkey.outcome` (`ok` or `error`).

## Security

- With `TLS.Enabled`, no connection is ever plaintext, and the certificate is
  verified for the host dialed unless `TLS.InsecureSkipVerify` is set. A TLS
  configuration that does not convert stops `Connect` before anything is dialed.
  See [TLS](#tls).
- Keys, patterns and channels stay off spans unless `RecordKeys` is set; values and
  payloads are never recorded.
- `QueryCache` bounds the size of what a client can have it store with
  `MaxQueryBytes`.
- Errors returned by the methods are the driver's own, and may name the server or
  the user. Do not send them to a client as they are.
