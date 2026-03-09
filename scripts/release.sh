#!/usr/bin/env bash

set -euo pipefail

if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  echo "Environment variable GITHUB_TOKEN must be set and not be empty" 1>&2
  exit 1
fi

if [[ -z "${1:-}" ]]; then
  echo "Usage: ./scripts/release.sh <version>" 1>&2
  echo "Example: ./scripts/release.sh v0.23.0" 1>&2
  exit 1
fi

export GORELEASER_CURRENT_TAG="$1"

echo "==> Releasing $GORELEASER_CURRENT_TAG"

# Phase 1: Prepare modules.
echo "==> Tidying modules..."
go work use -r ./
go mod tidy

integrations=$( jq -r '.integrations[] | .id' ./ecosystem.json )
for mod in $integrations; do
  echo "  -> Preparing integration/$mod"
  cd ./integration/$mod

  go mod tidy

  ./scripts/mod-require.sh "$GORELEASER_CURRENT_TAG"
  ./scripts/mod-dropreplace.sh

  cd ../../
done

# Phase 2: Commit, tag, and push.
echo "==> Committing release..."
git commit -am "version: Release $GORELEASER_CURRENT_TAG"
git push origin main

echo "==> Creating tags..."
for mod in $integrations; do
  git tag "integration/$mod/$GORELEASER_CURRENT_TAG"
done

git tag "$GORELEASER_CURRENT_TAG"
git push --tags

# Phase 3: Wait for the Go module proxy to index all modules.
# This prevents cached 404s when cross-integration dependencies
# (e.g. graphql -> valkey) are resolved by the proxy.
echo "==> Waiting for Go module proxy to index modules..."

wait_for_proxy() {
  local module="$1"
  local version="$2"
  local max_attempts=40
  local attempt=0

  while (( attempt < max_attempts )); do
    if GOPROXY=https://proxy.golang.org go list -m "${module}@${version}" > /dev/null 2>&1; then
      echo "  -> ${module}@${version} available"
      return 0
    fi

    attempt=$((attempt + 1))
    echo "  .. ${module}@${version} not yet available (attempt ${attempt}/${max_attempts})"
    sleep 15
  done

  echo "ERROR: ${module}@${version} not available on proxy after ${max_attempts} attempts" 1>&2
  return 1
}

wait_for_proxy "github.com/mountayaapp/helix.go" "$GORELEASER_CURRENT_TAG"

for mod in $integrations; do
  wait_for_proxy "github.com/mountayaapp/helix.go/integration/$mod" "$GORELEASER_CURRENT_TAG"
done

# Phase 4: Create GitHub release.
echo "==> Running goreleaser..."
goreleaser release --clean

# Phase 5: Restore local replace directives for development.
echo "==> Restoring local replace directives..."
for mod in $integrations; do
  cd ./integration/$mod

  ./scripts/mod-replace.sh

  cd ../../
done

git commit -am "version(scripts): Apply post-release of $GORELEASER_CURRENT_TAG"
git push origin main

echo "==> Release $GORELEASER_CURRENT_TAG complete!"
