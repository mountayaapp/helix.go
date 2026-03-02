#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

go clean -testcache

pids=()
dirs=()

# Run root module tests in the background.
(cd "$ROOT_DIR" && go test -test.v ./...) &
pids+=($!)
dirs+=(".")

# Run each integration module tests in the background.
integrations=$(jq -r '.integrations[] | .id' "$ROOT_DIR/ecosystem.json")
for mod in $integrations; do
  (cd "$ROOT_DIR/integration/$mod" && go test -test.v ./...) &
  pids+=($!)
  dirs+=("integration/$mod")
done

# Wait for all test processes and track failures.
failed=()
for i in "${!pids[@]}"; do
  if ! wait "${pids[$i]}"; then
    failed+=("${dirs[$i]}")
  fi
done

if [ ${#failed[@]} -gt 0 ]; then
  echo ""
  echo "FAIL: tests failed in ${failed[*]}"
  exit 1
fi
