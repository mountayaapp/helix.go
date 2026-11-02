#!/usr/bin/env bash

set -euo pipefail

# Runs one check across every Go module: the root module, then each integration
# ecosystem.json lists. `make lint`, `make test` and `make vulncheck` call it with
# the check's name, and each check collects the modules it fails in rather than
# stopping at the first, so one run reports all of them.

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

mode="${1:-}"

case "$mode" in
  lint)
    tools=(golangci-lint jq)
    check=(golangci-lint run --config "$ROOT_DIR/.golangci.yml" ./...)
    heading="Linting"
    summary="lint failed"
    ;;
  test)
    tools=(go jq)
    # -race because the defects this suite is most likely to meet are lifecycle
    # ones: a server field written by Start and read by Stop, a span ended from two
    # goroutines. None of them fails deterministically without it.
    check=(go test -race -v ./...)
    heading=""
    summary="tests failed"
    ;;
  vulncheck)
    tools=(govulncheck jq)
    # govulncheck reports only vulnerabilities in code the modules actually call,
    # so a finding here is reachable rather than merely present in the dependency
    # graph.
    check=(govulncheck ./...)
    heading="Checking"
    summary="vulnerabilities found"
    ;;
  *)
    echo "Usage: bash ./scripts/modules.sh <lint|test|vulncheck>" 1>&2
    exit 1
    ;;
esac

# The tools come from mise.toml, at the versions CI runs.
for tool in "${tools[@]}"; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool not found: run \`mise install\` (https://mise.jdx.dev), then make $mode from a shell with mise activated or as \`mise exec -- make $mode\`" 1>&2; exit 1; }
done

# Every module is checked standalone, with the workspace off, because that is what
# CI and every consumer do: `go.work` is absent on a fresh clone, so a module whose
# own go.sum is incomplete passes in workspace mode and fails there. Checking in
# workspace mode hides exactly the breakage the release path already learned to
# avoid.
export GOWORK=off

modules=(".")
integrations=$(jq -r '.integrations[] | .id' "$ROOT_DIR/ecosystem.json")
for mod in $integrations; do
  modules+=("integration/$mod")
done

# The tests run in every module at once and are collected once all of them have
# finished, which keeps the suite's wall time to that of its slowest module. The
# linters and govulncheck run one module at a time, so each module's findings
# print under its own heading.
parallel=false
if [[ "$mode" == "test" ]]; then
  parallel=true
  go clean -testcache
fi

failed=()
pids=()

for dir in "${modules[@]}"; do
  if [[ "$parallel" == true ]]; then
    (cd "$ROOT_DIR/$dir" && "${check[@]}") &
    pids+=($!)
    continue
  fi

  # The root module prints as "./", every other one by its path.
  if [[ "$dir" == "." ]]; then
    echo "$heading ./"
  else
    echo "$heading ./$dir"
  fi
  if ! (cd "$ROOT_DIR/$dir" && "${check[@]}"); then
    failed+=("$dir")
  fi
done

# pids holds one entry per module, in the order of modules, when the tests ran in
# the background. The guard keeps the empty array out of bash 3.2, which treats
# expanding one as an unbound variable under `set -u`.
if [[ "$parallel" == true ]]; then
  for i in "${!pids[@]}"; do
    if ! wait "${pids[$i]}"; then
      failed+=("${modules[$i]}")
    fi
  done
fi

if [ ${#failed[@]} -gt 0 ]; then
  echo ""
  echo "FAIL: $summary in ${failed[*]}"
  exit 1
fi
