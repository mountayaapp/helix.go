#!/usr/bin/env bash

set -euo pipefail

export GOPRIVATE="github.com/mountayaapp/*"

# go.work is gitignored, so it does not exist on a fresh clone. Create it before
# `go work use`, which is fatal without it.
[ -f go.work ] || go work init

go work use -r ./

rm -rf go.sum go.work.sum
go mod tidy

integrations=$( cat ./ecosystem.json | jq -r '.integrations[] | .id' | cat )
for mod in $integrations; do
  cd ./integration/$mod

  rm -rf go.sum
  go mod tidy

  cd ../../
done

go work sync
