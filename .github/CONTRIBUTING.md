# Contributing to `helix.go`

We are incredibly thankful for the contributions we can receive from the community.

## Development setup

The repository is a Go workspace of several modules: the root module plus one per
integration under `integration/`. All of them are kept on the same `go` directive
and released in lockstep.

Requirements:

- **Go 1.27 or later.** `GOTOOLCHAIN=auto` (the default) downloads it for you
  from the `go` directive in `go.mod`.
- `jq`, used by the scripts to read `ecosystem.json`.

```sh
make init   # regenerate go.work + tidy every module
make lint   # golangci-lint across all modules
make test   # go test across all modules
```

`go.work` is gitignored and does not exist on a fresh clone; `make init` creates
it. Note that `scripts/mod-tidy.sh` deliberately tidies with `GOWORK=off`: in
workspace mode the resolved hashes land in `go.work.sum` instead of each module's
own `go.sum`, which breaks the standalone per-module builds that CI performs.
Use `GOWORK=off` for any manual `go get` or `go mod tidy` for the same reason.

### golangci-lint must be built with Go 1.27

golangci-lint refuses to run when the Go version it was built with is older than
the module's `go` directive:

```
can't load config: the Go language version (go1.26) used to build golangci-lint
is lower than the targeted Go version (1.27.0)
```

A binary from a package manager is often built against an older Go. Install it
from source instead, which compiles it with your active toolchain:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

CI installs it the same way.

The same applies to `gofmt`: the package integrating Temporal uses Go 1.27
generic methods, and a `gofmt` from an older toolchain fails to parse them with
`method must have no type parameters`. Use the one from your active toolchain:

```sh
$(go env GOROOT)/bin/gofmt -l .
```

## Contribution Guidelines

Since we want to involve our community as much as possible, we try to keep
contribution guidelines as light but rock-solid as possible.

- Your pull request targets the `main` branch and is up-to-date with this branch.
- Commit messages and pull requests must respect the
  [Conventional Commits Specification](https://www.conventionalcommits.org/).
  The types and scopes to follow can be found on the
  [`semantic.yml` file](https://github.com/mountayaapp/helix.go/blob/main/.github/semantic.yml).
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
