---
name: google-go-style
description: >-
  Google Go Style Guide, Decisions and Best Practices, plus helix.go's own
  house conventions. Use when writing or reviewing Go in this repo: naming,
  doc comments, error construction, contexts, interfaces, option structs,
  test structure, and which deviations here are deliberate.
metadata:
  author: mountaya
  version: "1.1"
  source: https://google.github.io/styleguide/go/
---

# Google Go Style

Three layers, in increasing specificity:
[Guide](https://google.github.io/styleguide/go/guide) (principles) →
[Decisions](https://google.github.io/styleguide/go/decisions) (rules) →
[Best practices](https://google.github.io/styleguide/go/best-practices)
(patterns). Read with [effective-go](../effective-go/SKILL.md).

## Principles, in priority order

1. **Clarity** — the purpose and rationale are clear to the reader.
2. **Simplicity** — accomplishes its goal the simplest way possible.
3. **Concision** — high signal-to-noise ratio.
4. **Maintainability** — easy to change correctly.
5. **Consistency** — matches the surrounding codebase.

They are ordered: when two conflict, the earlier wins. Clarity beats concision,
so never compress at the cost of comprehension.

**Least mechanism.** Prefer a core language construct (slice, map, channel,
struct, loop) over a stdlib tool, and stdlib over a new dependency. Complexity
is easy to add and hard to remove. A `map[string]bool` is a set; reach for a set
library only when a map genuinely can't express the operations.

## Naming

- **MixedCaps**, never underscores. `MaxLength` / `maxLength`, never
  `MAX_LENGTH`. Exceptions for underscores: generated-code packages, `Test*` /
  `Benchmark*` / `Example*` names in `_test.go`, and low-level syscall interop.
- **Length scales with scope**, inversely with use count. A 1–7 line scope can
  take `c`; a file-scope variable needs words. `r` for `io.Reader`/`*http.Request`,
  `w` for `io.Writer`/`http.ResponseWriter`, `i` for indices are all fine.
- **Receivers**: one or two letters, an abbreviation of the type, and identical
  across *every* method on that type. Never `this` or `self`. Omit the name if
  unused. Avoid names that shadow common identifiers — `err` as a receiver is a
  bad idea for exactly this reason.
- **Packages**: lowercase, no underscores, no camelCase, unbroken multi-word
  (`tabwriter`). Avoid names likely to be shadowed by locals — `usercount` beats
  `count`.
- **No `Get` prefix.** `Counts()` not `GetCounts()`. Use `Fetch`/`Compute` when
  the call is expensive or can fail, to signal that to the reader.
- **Functions returning things get noun names**; functions doing things get verb
  names. Variants that differ only by type append the type: `ParseInt`,
  `ParseInt64`.
- **Initialisms keep their case**: `ID`, `URL`, `HTTP`, `API` — so `userID`,
  `ResourceID`, `ServeHTTP`. Never `Id`, `Url`.

## Doc comments

Every exported top-level name needs one, and so does any unexported declaration
with non-obvious behaviour. Full sentences, beginning with the name of the thing:

```go
// A Request represents a request to run a command.
type Request struct { ... }

// Encode writes the JSON encoding of req to w.
func Encode(w io.Writer, req *Request) { ... }
```

An article ("A", "The") may precede the name. Struct fields can take short
end-of-line phrases that assume the field name is the subject. Document
unexported code the same way, so exporting it later is a rename.

## Errors

**Error strings are lowercase and unpunctuated** — unless they start with an
exported name, proper noun or acronym — because they get wrapped into larger
messages:

```go
err := fmt.Errorf("something bad happened")   // good
err := fmt.Errorf("Something bad happened.")  // bad
```

The *displayed* message is different and typically capitalized:
`log.Errorf("Operation aborted: %v", err)`.

Wrap with `%w` when callers should be able to unwrap; use `errors.Is` /
`errors.AsType` to inspect — `errors.AsType[*T](err)` (Go 1.26) returns the
match, where `errors.As` needs a variable declared to fill. Use `errors.New`
when there is nothing to format — `fmt.Errorf` with no verbs is a smell.

## Contexts

`context.Context` is **always the first parameter**:

```go
func F(ctx context.Context /* other arguments */) {}
```

Exceptions: HTTP handlers (`req.Context()`), streaming RPC methods
(`stream.Context()`), test functions (`t.Context()`), and entrypoints
(`main`/`init` use `context.Background()`). Do not store a `Context` in a struct.

## Interfaces

- Don't create an interface until there's a real need. Resist abstracting for
  its own sake, and don't wrap clients in interfaces purely for testing.
- **Keep them small.** Small interfaces compose; large ones are hard to implement
  and hard to consume.
- **The consumer defines the interface**, containing only the methods it uses.
  The producer exports one only when the interface *is* the product.
- **Accept interfaces, return concrete types.** Returning concrete keeps the
  caller's full range of use available.
- Keep interface types unexported when only used within the package.
- Document them as user manuals: contract, edge cases, expected errors. Depth
  should track cognitive load, not method count.

> **Go 1.27 note.** Interface methods may **not** declare type parameters, and
> interface methods cannot be implemented by generic methods. If a type needs
> generic methods, it must be a concrete type.

## Function arguments

Long or ambiguous parameter lists become an **option struct**, passed last:
fields are self-documenting, defaults can be omitted, and it grows without
breaking call sites. Export it only if the function is exported.

## Tests

- Table-driven where cases are homogeneous; name subtests via `t.Run`.
- Mark helpers with `t.Helper()`; clean up with `t.Cleanup`.
- Failure messages state what was called, what came back, and what was wanted:
  `t.Errorf("Op(%q) failed unexpectedly; err=%v", args, err)`.
- Prefer testing through the real implementation's public API over test doubles.
- Benchmarks loop with `for b.Loop()` (Go 1.24), not `for range b.N`: setup
  before the loop stays out of the timing without `b.ResetTimer`, and the body
  is not optimized away.

---

# helix.go house conventions

These deviate from the guides **on purpose**. Do not "fix" them.

**Package docs live in `overview.go`, not `doc.go`.** Every package has one,
opening with `Package <name> ...`. Several encode architectural invariants — e.g.
`errorstack/overview.go` states that the package must not import any other
package in this ecosystem. Respect that constraint.

**Imports are grouped in three blocks**: stdlib, then
`github.com/mountaya/helix.go/...`, then third-party. This is stricter than
`gofmt`/`goimports` enforces; `make lint` checks it, with golangci-lint's `gci`.

**JSON is `encoding/json/v2`.** Its `omitempty` drops a member that encodes as
empty JSON (`null`, `""`, `[]`, `{}`), so a number or a bool that must not be
written when zero is tagged `omitzero`. It has no default encoding for a
`time.Duration`, and its `format` tag option is not available yet. The v1
`encoding/json` stays only where the bytes must match a v1 reader or writer,
and says why where it is used: the GraphQL handler decodes a body the way
gqlgen's transport will, and the Temporal schedule fingerprint must match the
one earlier releases recorded.

**Error message casing is bimodal and deliberate**, documented at
`errorstack/error.go`:
- *User-facing* envelope messages are Sentence case, no trailing period, present
  tense, actionable — `"Must be set"`, `"Integration is not in a healthy state"`.
- *Internal* span messages are lowercase — `"failed to read blob"`.
- `integration.NormalizeErrorMessage` is the adapter at the seam; it capitalizes
  the first rune of a foreign error.

This contradicts the guide's blanket "lowercase error strings" rule. The guide's
rationale is that error strings get embedded in larger messages; here the
user-facing ones are terminal envelope values that get rendered directly, so
Sentence case is correct for them.

**Primary type named after its package** — `postgres.PostgreSQL`,
`valkey.Valkey`, `clickhouse.ClickHouse`, `bucket.Bucket`, `rest.REST`,
`httpclient.HTTPClient`. This is **not** a deviation. Decisions' *Package vs.
exported symbol name* explicitly contemplates it: "If a package exports only one
type and it is named after the package itself, the canonical name for the
constructor is `New` if one is required." It prescribes the constructor name, not
a rename of the type. The stdlib does the same (`time.Time`, `list.List`,
`template.Template`, `log.Logger`). Mirror it for any new integration.

What the rule *does* prohibit is redundant qualifiers **around** a name:
`widget.NewWidget` (→ `widget.New`), `sqldb.DBConnection` (→ `sqldb.Connection`),
`yamlconfig.ParseYAMLConfig` (→ `yamlconfig.Parse`). Check for that shape, not for
a type that simply shares the package name.

helix uses `Connect` rather than `New` for dependency constructors that dial
(`postgres.Connect`, `valkey.Connect`). That is a considered choice: the guide
endorses a more specific verb when a call does I/O and may block or fail, the same
reasoning behind preferring `Fetch`/`Compute` over `Get`.

**Context accessors use the canonical `NewContext`/`FromContext` pair**, e.g.
`event.NewContext(ctx, e)` and `event.FromContext(ctx)`. Do not copy OpenTelemetry's
`ContextWithSpan`/`SpanFromContext` shape here: it reads fine for OTel because its
package (`trace`) and type (`Span`) differ, but in a package whose primary type
shares its name it produces `event.ContextWithEvent`, which is exactly the
redundancy the guide says to eliminate.

**`errorstack` over `fmt.Errorf` for anything user-facing.** `errorstack.New`,
`NewValidation`, `Wrap`, with `WithCode`/`WithPath`/`WithExtension` options.
`errorstack.Wrap` returns `error` (not `*Error`) on purpose, to avoid the
typed-nil-through-interface trap.

**Tests are white-box** (same package, no `_test` suffix package), colocated,
and use testify `assert`/`require`. No `t.Parallel()` — `service` enforces one
instance per process via `sync.Once`, so parallel tests would race.

**Integration modules are standalone.** Each has its own `go.mod` with a local
`replace` back to the root. Always run module commands with `GOWORK=off` —
workspace mode diverts hashes into the gitignored `go.work.sum` and leaves the
module's own `go.sum` incomplete for the standalone builds CI performs.
