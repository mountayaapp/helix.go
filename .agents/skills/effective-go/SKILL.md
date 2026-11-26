---
name: effective-go
description: >-
  Core Go language idiom from Effective Go (go.dev/doc/effective_go).
  Use when writing or reviewing Go code: formatting, naming, control flow,
  functions and multiple returns, defer, allocation (new vs make), embedding,
  concurrency, and the errors/panic/recover contract.
metadata:
  author: mountaya
  version: "1.1"
  source: https://go.dev/doc/effective_go
---

# Effective Go

Idiomatic Go as defined by the canonical guide. Read alongside
[google-go-style](../google-go-style/SKILL.md), which is more prescriptive and
covers naming and API decisions in more depth.

> **Scope caveat.** Effective Go was written for Go's 2009 release and is not
> actively updated. It does **not** cover modules, generics, error wrapping
> (`%w`), `log/slog`, or anything after Go 1. For those, defer to the style
> guide, the package docs, and the release notes.

## Formatting

Never argue about formatting — run `gofmt` (and `goimports`). Tabs for indent,
no line-length limit, no manual alignment. If a construct looks bad after
`gofmt`, the construct is usually wrong, not the formatter.

The same goes for idioms a newer Go superseded: since Go 1.26, `go fix`
rewrites code to the current ones (`errors.AsType`, `new(expr)`, `for range n`,
`maps.Copy`, …). Run it after raising a module's `go` line, and review what it
changed.

## Commentary

Doc comments precede the declaration with no blank line and begin with the name
being declared, so `go doc` output reads as a sentence:

```go
// Compile parses a regular expression and returns, if successful, a Regexp
// that can be used to match against text.
func Compile(str string) (*Regexp, error) {
```

Package comments go on one file only (`package foo` preceded by the comment).
Start them with `Package foo ...`.

## Names

- **Package names**: short, lowercase, single word. The name is part of every
  reference, so `bufio.Reader` not `bufio.BufReader`, `ring.New` not
  `ring.NewRing`. Avoid stutter.
- **Getters**: no `Get` prefix. Field `owner` → getter `Owner()`, setter
  `SetOwner()`.
- **Interfaces**: one-method interfaces take the method name plus `-er`:
  `Reader`, `Writer`, `Formatter`. Don't take those names in vain — if your
  method does not match `io.Writer`'s signature and meaning, don't call it
  `Write`.
- **MixedCaps**, never underscores. Exported `MaxLength`, unexported
  `maxLength` — never `MAX_LENGTH`.

## Semicolons and braces

The lexer inserts semicolons, which is why the opening brace **must** be on the
same line:

```go
if i < f() {   // correct
    g()
}
```

Putting `{` on its own line silently changes the meaning of the program.

## Control structures

`if`, `for` and `switch` take no parentheses. `for` covers all loop forms —
there is no `while`.

Prefer accepting an initializer and omitting the `else` when the `if` body ends
in `break`, `continue`, `goto` or `return`:

```go
f, err := os.Open(name)
if err != nil {
    return err
}
// f is in scope and valid from here on — no else, no extra nesting.
```

Use `range` for slices, maps, strings and channels, and since Go 1.22–1.23 for
integers (`for i := range n`) and iterator functions (`iter.Seq`). Range over a
string decodes UTF-8 runes; range over a slice with only the index is
`for i := range v`.

Go's `switch` has no fallthrough by default; cases may be comma-separated lists
and may be expressionless (a cleaner `if/else` chain). Type switches declare the
variable per-case:

```go
switch v := value.(type) {
case string:
    return v
case int:
    return strconv.Itoa(v)
}
```

## Functions

Multiple return values replace in-band error sentinels. Named result parameters
are documentation and can be set by `defer`; keep them short and don't abuse
naked returns in long functions.

`defer` runs LIFO when the enclosing **function** returns (not the block), and
its arguments are evaluated at `defer` time. Use it to pair acquisition with
release so the release can't be forgotten on an early return:

```go
f, err := os.Open(name)
if err != nil {
    return err
}
defer f.Close()
```

## Data: allocation

- `new(T)` — zeroes storage, returns `*T`. Since Go 1.26 it also takes a value,
  `new(expr)`, and returns a pointer to a copy of it: `new(true)`,
  `new(30 * time.Second)`. A pointer helper such as `func BoolPtr(v bool) *bool`
  is no longer needed.
- `make(T, ...)` — slices, maps and channels **only**; returns an initialized
  `T` (not a pointer).

Design so the zero value is useful (`bytes.Buffer`, `sync.Mutex`). Prefer
composite literals over field-by-field assignment.

Append and copy are the slice workhorses; a slice carries pointer, length and
capacity, so passing one by value shares the backing array but not the header.

## Embedding

Embed to borrow behaviour, not to fake inheritance. An embedded type's methods
are promoted to the outer type; the outer type may override by declaring the
same method. Interfaces can embed interfaces (`ReadWriter` = `Reader` +
`Writer`).

## Concurrency

> Do not communicate by sharing memory; instead, share memory by communicating.

Goroutines are cheap; a channel gives both communication and synchronization.
Unbuffered channels synchronize the exchange; buffered channels can be used as
semaphores to bound parallelism. `select` multiplexes. Use `sync.Mutex` when a
mutex genuinely is the right tool — the proverb is a default, not a ban.

Beware capturing the loop variable in a closure. (Since Go 1.22 each iteration
has a fresh variable, so this is no longer a bug — but be explicit when the
goroutine's input matters.)

To start goroutines and wait for all of them, `wg.Go(f)` (Go 1.25) does in one
call what `wg.Add(1)`, `go` and `defer wg.Done()` did in three.

## Errors, panic, recover

Errors are values; return them, don't panic. Error strings should be lowercase
and unpunctuated because they get wrapped into larger messages.

Provide structured error types when callers need to discriminate, and let them
test with `errors.Is` / `errors.AsType` (post-Effective Go, but the correct modern
form). `errors.AsType[*T](err)` (Go 1.26) returns the match, where `errors.As`
needs a variable declared to fill:

```go
if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
    return pathErr.Path
}
```

Wrap with `%w` to preserve the chain.

`panic` is for genuinely unrecoverable situations — chiefly impossible states
during initialization. A library should not let a panic escape its API boundary:
recover at the package edge and convert to an error.

## Checklist

- [ ] `gofmt`/`goimports` clean
- [ ] `go fix` clean: no idiom a newer Go replaced
- [ ] Exported symbols have doc comments starting with the symbol name
- [ ] No stutter (`pkg.PkgThing`), no `Get` prefixes
- [ ] Errors returned, not panicked; lowercase unpunctuated strings; wrapped with `%w`
- [ ] Zero values useful; `make` only for slice/map/chan
- [ ] Early return instead of `else` after a terminating branch
- [ ] `defer` for cleanup that must survive early returns
