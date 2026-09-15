#!/usr/bin/env bash

set -euo pipefail

export GOPRIVATE="github.com/mountayaapp/*"

# go.work is gitignored, so it does not exist on a fresh clone. Create it before
# `go work use`, which is fatal without it.
[ -f go.work ] || go work init

go work use -r ./

rm -rf go.sum go.work.sum

# Align the workspace build list before tidying. `go work sync` writes resolved
# versions back into each member's go.mod, so it has to run first: doing it last
# can bump a requirement after go.sum was written, leaving the module unbuildable
# on its own.
go work sync

# Every `go mod tidy` below runs with GOWORK=off on purpose. In workspace mode the
# resolved hashes land in go.work.sum, which is gitignored, so the module's own
# go.sum comes out incomplete and standalone builds (what CI does, since go.work
# is absent on a fresh clone) fail on a missing go.sum entry.
GOWORK=off go mod tidy

integrations=$( cat ./ecosystem.json | jq -r '.integrations[] | .id' | cat )
for mod in $integrations; do
  cd ./integration/$mod

  rm -rf go.sum
  GOWORK=off go mod tidy

  cd ../../
done
