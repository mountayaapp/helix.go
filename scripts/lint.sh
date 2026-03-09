#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CONFIG="$ROOT_DIR/.golangci.yml"

failed=()

# Lint root module.
echo "Linting ./"
if ! (cd "$ROOT_DIR" && golangci-lint run --config "$CONFIG" ./...); then
  failed+=(".")
fi

# Lint each integration module.
integrations=$(jq -r '.integrations[] | .id' "$ROOT_DIR/ecosystem.json")
for mod in $integrations; do
  echo "Linting ./integration/$mod"
  if ! (cd "$ROOT_DIR/integration/$mod" && golangci-lint run --config "$CONFIG" ./...); then
    failed+=("integration/$mod")
  fi
done

if [ ${#failed[@]} -gt 0 ]; then
  echo ""
  echo "FAIL: lint failed in ${failed[*]}"
  exit 1
fi
