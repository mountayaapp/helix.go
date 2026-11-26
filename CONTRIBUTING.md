# Contributing to `helix.go`

We are incredibly thankful for the contributions we can receive from the community.
The rules a change follows are in `AGENTS.md`.

## Development setup

The repository is a Go workspace of several modules: the root module plus one per
integration under `integration/`. All of them are kept on the same `go` directive
and released in lockstep.

Every tool the commands below run is pinned in `mise.toml`, and CI installs the
same versions from it. Install [mise](https://mise.jdx.dev), then run
`mise install` in the repository: it installs Go, jq (the scripts read
`ecosystem.json` with it), golangci-lint and govulncheck at the versions CI runs.

golangci-lint is built from source with the pinned Go rather than taken from a
package manager on purpose: it refuses to run when the Go version it was built
with is older than the module's `go` directive, and a packaged binary is often
built with an older Go.

With mise activated in your shell (`eval "$(mise activate zsh)"`, or bash or fish),
the pinned tools are the ones on your `PATH` in the repository. Without it, run a
command through mise, as in `mise exec -- make lint`.

```sh
mise install     # once, and again whenever mise.toml changes
make init        # regenerate go.work + tidy every module
make lint        # golangci-lint across all modules
make test        # go test across all modules (needs Docker)
make vulncheck   # govulncheck across all modules
mise run ci      # all three, in the order CI runs them
```

`make lint`, `make test` and `make vulncheck` are one script, `scripts/modules.sh`,
run with the check's name. It runs the check in the root module and in every
integration `ecosystem.json` lists, and reports every module that failed rather
than stopping at the first. Several integrations test against real servers started
in containers, so `make test` needs Docker running.

CI runs `mise run ci` on every push to `main` and on every pull request, with the
same tool versions and the same mise version that `mise.toml` sets.

`go.work` is gitignored and does not exist on a fresh clone; `make init` creates
it. Note that `scripts/mod-tidy.sh` deliberately tidies with `GOWORK=off`: in
workspace mode the resolved hashes land in `go.work.sum` instead of each module's
own `go.sum`, which breaks the standalone per-module builds that CI performs.
Use `GOWORK=off` for any manual `go get` or `go mod tidy` for the same reason.

The pinned Go matters for `gofmt` too: the package integrating Temporal uses Go
1.27 generic methods, and a `gofmt` from an older toolchain fails to parse them
with `method must have no type parameters`. With mise activated, the `gofmt` on
your `PATH` is the pinned one:

```sh
gofmt -l .
```

## Releasing

Releases are for maintainers. A release is a set of git tags, one per module at
the same version: `vX.Y.Z` for the root and `integration/<id>/vX.Y.Z` for each
integration. Go consumers need nothing else, since the module proxy serves every
module straight from its tag, so the release script creates no GitHub release.

```sh
bash ./scripts/release.sh --dry-run v0.26.0   # every check, and the commands it would run
make release VERSION=v0.26.0                  # the release itself
bash ./scripts/release.sh --resume v0.26.0    # continue a release stopped after the root's tag
```

`scripts/release.sh` refuses to start unless the working tree is clean, untracked
files included, `main` is checked out, `HEAD` is exactly `main` on `origin` (it
fetches nothing, so pull or push first), and none of the version's tags exists
yet. It then runs `scripts/mod-tidy.sh` and stops if that changes anything: the
changes are left in the tree, to be committed as a change of their own before
releasing. Then it runs the gate, `mise run ci`, and stops if that fails. So the
root is released exactly as it was gated.

Only then does it release, the root first, since each integration is tidied
against the root's new tag and no integration requires another:

1. It tags the root at `HEAD`, pushes that tag alone, and waits until the module
   proxy serves it.
2. In each integration, it swaps the local `replace` for a requirement on the
   root's new tag, then runs `go mod tidy` and `go vet ./...` with `GOWORK=off`.
   Each integration's `go.sum` is then complete, and the integration builds on
   its own, as it does for a consumer.
3. It commits that as `version: Release vX.Y.Z`, pushes `main`, tags every
   integration at that commit, and pushes exactly those tags.
4. It restores the local replaces, tidies again, and commits and pushes that as
   the post-release. Last, it asks the module proxy for each integration's new
   version, so that the first consumer to resolve one does not meet a cached 404.

The person running it owns those commits and pushes.

If the module proxy still does not serve the root's tag after ten minutes, the
release stops there: that tag is public, and nothing else has changed. Once the
proxy serves it, `--resume` continues the release. It runs the same checks, the
gate included, requires the root's tag at `HEAD` here and on `origin` and no
integration tag, and goes on from the wait. A release that stops later says what
is already public and what is left to do.

The dry run runs every check above, the tidy and the gate included, and prints
each command that would tag, push, edit, tidy, vet, commit or ask the module
proxy instead of running it. The integrations' tidy and vet are among the
printed ones: they resolve the root's new tag, which does not exist until the
release pushes it.

## Contribution Guidelines

Since we want to involve our community as much as possible, we try to keep
contribution guidelines as light but rock-solid as possible.

- Your pull request targets the `main` branch and is up-to-date with this branch.
- Commit messages and pull requests must respect the
  [Conventional Commits Specification](https://www.conventionalcommits.org/).
  The types and scopes to follow can be found on the
  [`semantic.yml` file](https://github.com/mountaya/helix.go/blob/main/.github/semantic.yml).
- You sign every Git commits using your real name and real email address. If you
  set your `user.name` and `user.email` git configs, you can sign your commit
  automatically using `git commit -s`. Example:
  ```bash
  $ git commit -s -am "feat(bucket): Add support for MinIO"
  ```
- You agree to both the product license and the Developer Certificate of Origin
  as defined in Exhibit A.

## Exhibit A - Developer Certificate of Origin

    By making a contribution to this project, I certify that:

    (a) The contribution was created in whole or in part by me and I have the right
        to submit it under the open source license indicated in the file; or

    (b) The contribution is based upon previous work that, to the best of my knowledge,
        is covered under an appropriate open source license and I have the right under
        that license to submit that work with modifications, whether created in whole
        or in part by me, under the same open source license (unless I am permitted
        to submit under a different license), as indicated in the file; or

    (c) The contribution was provided directly to me by some other person who certified
        (a), (b) or (c) and I have not modified it.

    (d) I understand and agree that this project and the contribution are public and
        that a record of the contribution (including all personal information I submit
        with it, including my sign-off) is maintained indefinitely and may be redistributed
        consistent with this project or the open source license(s) involved.

<!-- ai-agents:begin -->

## Working with an AI agent

Any coding agent is welcome here. `AGENTS.md` is the entry point for all of them: read it, or have your agent read it, before the first change.

| Agent | What it picks up from this repository |
| --- | --- |
| Claude Code | `CLAUDE.md`, which imports `AGENTS.md`; skills under `.claude/skills/` |
| Codex | `AGENTS.md`, `.agents/skills/` |
| Cursor | `AGENTS.md` |
| Any other | `AGENTS.md` and `.agents/` |

Nothing else is needed: no plugin, no MCP server, no account. Personal agent settings go in `.claude/settings.local.json`, which is never committed.

<!-- ai-agents:end -->
