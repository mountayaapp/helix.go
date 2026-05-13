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

# Phase 3: Create GitHub release.
echo "==> Running goreleaser..."
goreleaser release --clean

# Phase 4: Restore local replace directives for development.
echo "==> Restoring local replace directives..."
for mod in $integrations; do
  cd ./integration/$mod

  ./scripts/mod-replace.sh

  cd ../../
done

git commit -am "version(scripts): Apply post-release of $GORELEASER_CURRENT_TAG"
git push origin main

echo "==> Release $GORELEASER_CURRENT_TAG complete!"

# Phase 5: Warm the Go module proxy cache (best-effort).
# This prevents cached 404s when cross-integration dependencies
# (e.g. graphql -> valkey) are resolved by the proxy.
echo "==> Warming Go module proxy cache..."

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

  echo "  !! ${module}@${version} not available on proxy after ${max_attempts} attempts (non-fatal)" 1>&2
  return 0
}

wait_for_proxy "github.com/mountayaapp/helix.go" "$GORELEASER_CURRENT_TAG"

for mod in $integrations; do
  wait_for_proxy "github.com/mountayaapp/helix.go/integration/$mod" "$GORELEASER_CURRENT_TAG"
done

cat <<'EOF'

==> Release complete. pkg.go.dev indexing notes:

    * DO NOT visit pkg.go.dev/<module>@<version> until at least 30 min
      have passed. Per index.golang.org's docs, requesting a version
      before it has fully propagated pins a stale mirror cache for up
      to 30 minutes, and pkg.go.dev's worker caches the resulting
      build failure indefinitely.

    * Verify sooner if needed the way a normal consumer would:
      `go get github.com/mountayaapp/helix.go@<tag>` in a downstream
      project. This is one of pkg.go.dev's three documented paths for
      ensuring a version is indexed and it is lossless.

    * After 30 min, check pkg.go.dev/<module>?tab=versions. If anything
      is still missing, file a pkgsite issue:
      https://github.com/golang/go/issues/new?template=02-pkgsite.md
      Do NOT click the "Request" button or otherwise poke the missing
      URL — the Go team's manual re-index is the only path that clears
      a cached build failure.

EOF
