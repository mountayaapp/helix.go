# AGENTS.md

Instructions for any coding agent working in this repository. `README.md` says what helix.go
is for; this file says how to change it.

helix.go is an open-source Go library: a service runtime and a set of infrastructure
integrations, each in its own module. Other projects consume it as an upstream dependency.

## Commands

```bash
mise install    # Go, jq, golangci-lint and govulncheck, at the versions mise.toml pins
make init       # go mod tidy in every module
make lint       # the linters, across every module
make test       # the tests, across every module (needs Docker)
make vulncheck  # govulncheck, across every module
mise run ci     # the gate: lint, test and vulncheck, exactly as CI runs them
bash ./scripts/release.sh --dry-run v0.26.0   # maintainers: preview a release; nothing is tagged, committed or pushed
make release VERSION=v0.26.0   # maintainers: tidy check, gate, tag the root, tidy and tag the integrations against it, restore the local replaces
bash ./scripts/release.sh --resume v0.26.0    # maintainers: continue a release that stopped after publishing the root's tag
```

`make lint`, `make test` and `make vulncheck` each run `scripts/modules.sh` with the check's
name, over the root module and every integration `ecosystem.json` lists.

Every tool these commands run is pinned in `mise.toml` (https://mise.jdx.dev), and CI installs
the same versions from it: change a version there and nowhere else. In a shell without mise
activated, run a command as `mise exec -- make lint`.

Integration modules are standalone: each has its own `go.mod` with a local `replace` back to
the root. Run module commands with `GOWORK=off`, so `go.sum` stays complete for the standalone
builds CI performs.

## Rules

- **Nothing in helix.go knows who uses it.** Documentation, examples and test fixtures stay
  neutral: no consumer's domain, names or data. A change made for one consumer is described by
  what it does for any consumer.
- **Read the style skills before writing Go.** `google-go-style` holds the Google Go Style Guide
  and the deliberate house deviations (package docs in `overview.go`, three import groups,
  bimodal error casing, `Connect` for dialing constructors, `NewContext`/`FromContext`,
  `errorstack` over `fmt.Errorf`, white-box tests with no `t.Parallel()`). `effective-go` holds
  the core language idiom. Both are in `.agents/skills/`.
- **Doc comments are godoc.** helix.go is read on pkg.go.dev, so every exported identifier has
  a `//` comment that starts with its name.
- **A public API change is a release.** Exported names, signatures and behaviour are a contract
  with every consumer; change them deliberately and say so in the release notes.
- **Git is read-only for an agent.** `git diff`, `log`, `show`, `status` and `blame` only. A
  person commits, tags and releases.
