#!/usr/bin/env bash

set -euo pipefail

# Releases every module at one version. A Go consumer needs nothing but the tags:
# the module proxy serves each module straight from its tag. A release is
# therefore a commit that points every integration at the new root, one tag per
# module, and a second commit that restores the local replaces a clone develops
# against.
#
# --dry-run runs every check a release runs, the gate included, and refuses
# exactly what a release refuses. Each command that would change the repository,
# origin or the module proxy is printed instead of run.

usage() {
  echo "Usage: make release VERSION=<version>, or bash ./scripts/release.sh [--dry-run] <version>" 1>&2
  echo "Example: make release VERSION=v0.23.0" 1>&2
  exit 1
}

# refuse <message>: stops the release. Every refusal happens before anything has
# changed, so there is nothing to undo.
refuse() {
  echo "$1" 1>&2
  exit 1
}

dry_run=false
version=""
for arg in "$@"; do
  case "$arg" in
    --dry-run) dry_run=true ;;
    -*) usage ;;
    *)
      [[ -z "$version" ]] || usage
      version="$arg"
      ;;
  esac
done

[[ -n "$version" ]] || usage

# A malformed version is only discovered when a consumer fails to resolve it, by
# which point it is tagged and public.
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  refuse "Version \"$version\" is not of the form vX.Y.Z, such as v0.23.0"
fi

# The tools come from mise.toml. They are checked before anything is committed,
# tagged or pushed, so a missing one cannot stop a release halfway.
command -v mise >/dev/null 2>&1 || refuse "mise not found: install it (https://mise.jdx.dev), since the release runs the gate as \`mise run ci\`"
for tool in go jq; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool not found: run \`mise install\` (https://mise.jdx.dev), then make release from a shell with mise activated or as \`mise exec -- make release VERSION=<version>\`" 1>&2; exit 1; }
done

cd "$(dirname "$0")/.."

# Every command that changes the repository, origin or the module proxy goes
# through run, so that --dry-run prints exactly what a release would do. An
# argument holding a space is printed quoted, as it would be typed.
run() {
  if [[ "$dry_run" == true ]]; then
    local line="+" arg
    for arg in "$@"; do
      if [[ "$arg" == *" "* ]]; then
        line+=" \"$arg\""
      else
        line+=" $arg"
      fi
    done
    echo "$line"
  else
    "$@"
  fi
}

if [[ "$dry_run" == true ]]; then
  echo "==> Releasing $version (dry run: every check runs, and each command that would change something is printed instead)"
else
  echo "==> Releasing $version"
fi

integrations=$( jq -r '.integrations[] | .id' ./ecosystem.json )

# Each integration is tagged under its own module path, and the root last.
tags=()
for mod in $integrations; do
  tags+=("integration/$mod/$version")
done
tags+=("$version")

# Phase 0: Check the repository and run the gate.
#
# The release commits with `git commit -am` and pushes main, so anything already
# changed in the tree would ship inside the release commit, and an untracked file
# is one the gate runs against but the tag does not hold. The release therefore
# starts from a clean main that is exactly origin's main. Nothing is fetched: the
# checks compare against origin as it is now and say what to run when it differs.
echo "==> Checking the repository..."

status=$(git status --porcelain --untracked-files=normal)
if [[ -n "$status" ]]; then
  count=$(printf '%s\n' "$status" | wc -l | tr -d ' ')
  {
    echo "The working tree is not clean: a release commits only the release, so commit or remove every change first, untracked files included ($count, see \`git status\`):"
    printf '%s\n' "$status" | sed -n '1,20s/^/    /p'
    if (( count > 20 )); then
      echo "    ... and $((count - 20)) more"
    fi
  } 1>&2
  exit 1
fi

branch=$(git symbolic-ref --quiet --short HEAD || true)
if [[ -z "$branch" ]]; then
  refuse "HEAD is detached: a release is cut from main, so run \`git switch main\` first"
fi
if [[ "$branch" != "main" ]]; then
  refuse "On branch $branch: a release is cut from main, so run \`git switch main\` first"
fi

for tag in "${tags[@]}"; do
  if git rev-parse --quiet --verify "refs/tags/$tag" >/dev/null; then
    refuse "Tag $tag already exists in this clone: pick a version that was never released. If a run that stopped early left it unpushed, delete this version's local tags with \`git tag -d\` first."
  fi
done

# One ls-remote answers both remaining questions: where origin's main is, and
# whether origin already holds any of the tags.
refs=("refs/heads/main")
for tag in "${tags[@]}"; do
  refs+=("refs/tags/$tag")
done
remote=$(git ls-remote origin "${refs[@]}") || refuse "Could not read origin with \`git ls-remote origin\`: check the remote and your access to it, then release again"

existing=$(printf '%s\n' "$remote" | awk '$2 ~ /^refs\/tags\// && $2 !~ /\^\{\}$/ { sub(/^refs\/tags\//, "", $2); print $2 }')
if [[ -n "$existing" ]]; then
  refuse "origin already has ${existing//$'\n'/ }: $version is released, so pick the next version"
fi

head=$(git rev-parse HEAD)
remote_main=$(printf '%s\n' "$remote" | awk '$2 == "refs/heads/main" { print $1 }')
if [[ -z "$remote_main" ]]; then
  refuse "origin has no main branch: push main first, then release again"
fi
if [[ "$head" != "$remote_main" ]]; then
  refuse "HEAD (${head:0:12}) is not origin's main (${remote_main:0:12}), and the release has to land on exactly what origin holds. Nothing was fetched: push your commits to main, or pull origin's, then release again."
fi

# The gate is the one CI runs, mise.toml's `ci` task: lint, test and govulncheck
# across every module, and the tests need Docker running. A pushed tag is public
# at once and the module proxy never forgets it, so the gate passes before the
# release changes anything rather than after.
echo "==> Running the gate (mise run ci)..."
mise run ci || refuse "The gate failed: nothing was changed, committed, tagged or pushed. Fix what it reports, then release again."

# Phase 1: Prepare modules.
#
# Tidying is delegated to mod-tidy.sh rather than repeated here. Every `go mod
# tidy` has to run with GOWORK=off: in workspace mode the resolved hashes land in
# go.work.sum, which is gitignored, so the module's own go.sum would be published
# incomplete and standalone builds - what a consumer and CI both do, since go.work
# is absent on a fresh clone - fail on a missing go.sum entry. mod-tidy.sh also
# runs `go work sync` before tidying, which this path used to skip.
echo "==> Tidying modules..."
run bash ./scripts/mod-tidy.sh

# Each integration module requires the root at the released tag instead of the
# local replace a clone develops against.
for mod in $integrations; do
  echo "  -> Preparing integration/$mod"
  run go mod edit \
    -require "github.com/mountayaapp/helix.go@$version" \
    -dropreplace github.com/mountayaapp/helix.go \
    "./integration/$mod/go.mod"
done

# Phase 2: Commit, tag, and push.
echo "==> Committing release..."
run git commit -am "version: Release $version"
run git push origin main

echo "==> Creating tags..."
for tag in "${tags[@]}"; do
  run git tag "$tag"
done

# Only the tags this release created are pushed. `git push --tags` would publish
# every other tag in the clone as well, and a published tag cannot be taken back.
run git push origin "${tags[@]}"

# Phase 3: Restore local replace directives for development.
echo "==> Restoring local replace directives..."
for mod in $integrations; do
  run go mod edit -replace github.com/mountayaapp/helix.go=../../ "./integration/$mod/go.mod"
done

run git commit -am "version(scripts): Apply post-release of $version"
run git push origin main

if [[ "$dry_run" != true ]]; then
  echo "==> Release $version complete!"
fi

# Phase 4: Warm the Go module proxy cache (best-effort).
# This prevents cached 404s when a consumer resolves a freshly-tagged module
# before the proxy has seen it.
echo "==> Warming Go module proxy cache..."

wait_for_proxy() {
  local module="$1"
  local version="$2"
  local max_attempts=40
  local attempt=0

  # A dry run never asks: the tag does not exist yet, and asking now is exactly
  # what caches the 404 this phase is here to prevent.
  if [[ "$dry_run" == true ]]; then
    echo "+ GOPROXY=https://proxy.golang.org go list -m ${module}@${version}"
    return 0
  fi

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

wait_for_proxy "github.com/mountayaapp/helix.go" "$version"

for mod in $integrations; do
  wait_for_proxy "github.com/mountayaapp/helix.go/integration/$mod" "$version"
done

if [[ "$dry_run" == true ]]; then
  echo ""
  echo "==> Dry run of $version complete: nothing was tidied, edited, committed, tagged or pushed, and the module proxy was not asked."
  exit 0
fi

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
