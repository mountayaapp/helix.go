#!/usr/bin/env bash

set -euo pipefail

# Releases every module at one version. A Go consumer needs nothing but the tags:
# the module proxy serves each module straight from its tag. The root goes first
# and alone: its tag is put on the commit the gate passed on, and the release
# waits until the module proxy serves it. Only then can each integration require
# the root at that tag and be tidied against it, so that the go.sum its own tag
# holds is complete and the integration builds on its own. A release is
# therefore the root's tag, a commit that points every integration at it with
# one tag per integration, and a second commit that restores the local replaces
# a clone develops against.
#
# --dry-run runs every check a release runs, the gate and the tidy included, and
# refuses exactly what a release refuses. Each command that would change the
# repository, origin or the module proxy is printed instead of run.
#
# --resume continues a release that stopped once the root's tag was public and
# before any integration was tagged, such as one whose wait for the module proxy
# ran out. It runs the same checks, the gate included, but requires the root's
# tag at HEAD, here and on origin, where a release refuses it, and goes on from
# the wait for the module proxy.

usage() {
  echo "Usage: make release VERSION=<version>, or bash ./scripts/release.sh [--dry-run] [--resume] <version>" 1>&2
  echo "Example: make release VERSION=v0.23.0" 1>&2
  echo "--resume continues a release that stopped after it published the root's tag" 1>&2
  exit 1
}

# refuse <message>: stops the release. Every refusal up to the root's tag happens
# before anything has changed, so there is nothing to undo. One after it says
# what is already public and how to go on.
refuse() {
  echo "$1" 1>&2
  exit 1
}

# refuse_changes <reason> <status>: refuses with a `git status --porcelain`
# listing, its first twenty paths and how many more there are.
refuse_changes() {
  local count
  count=$(printf '%s\n' "$2" | wc -l | tr -d ' ')
  {
    echo "$1 ($count, see \`git status\`):"
    printf '%s\n' "$2" | sed -n '1,20s/^/    /p'
    if (( count > 20 )); then
      echo "    ... and $((count - 20)) more"
    fi
  } 1>&2
  exit 1
}

dry_run=false
resume=false
version=""
for arg in "$@"; do
  case "$arg" in
    --dry-run) dry_run=true ;;
    --resume) resume=true ;;
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

# wait_for_proxy <module> <version>: asks the module proxy for one version until
# it serves it, for up to ten minutes, and fails if it never does. The caller
# decides whether that stops the release.
wait_for_proxy() {
  local module="$1"
  local version="$2"
  local max_attempts=40
  local attempt=0

  # A dry run never asks: the tag does not exist yet, and asking now is exactly
  # what caches the 404 this wait is here to prevent.
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

  return 1
}

heading="==> Releasing $version"
if [[ "$resume" == true ]]; then
  heading="==> Resuming the release of $version from its root's tag"
fi
if [[ "$dry_run" == true ]]; then
  echo "$heading (dry run: every check runs, and each command that would change something is printed instead)"
else
  echo "$heading"
fi

root_module="github.com/mountaya/helix.go"
integrations=$( jq -r '.integrations[] | .id' ./ecosystem.json )

# The root is tagged alone and first. Each integration is tagged after it, under
# its own module path.
integration_tags=()
for mod in $integrations; do
  integration_tags+=("integration/$mod/$version")
done

# What is left to do by hand when the post-release commit fails. Every tag is
# public by then, so the release itself is done.
post_release_by_hand="Restore each integration's replace with \`go mod edit -replace $root_module=../../ integration/<id>/go.mod\`, run \`make init\`, then commit what changed as \"version(scripts): Apply post-release of $version\" and push main."

# Phase 0: Check the repository, tidy, and run the gate.
#
# The release commits with `git commit -am` and pushes main, so anything already
# changed in the tree would ship inside the release commit, and an untracked file
# is one the gate runs against but the tag does not hold. The release therefore
# starts from a clean main that is exactly origin's main. Nothing is fetched: the
# checks compare against origin as it is now and say what to run when it differs.
echo "==> Checking the repository..."

status=$(git status --porcelain --untracked-files=normal)
if [[ -n "$status" ]]; then
  refuse_changes "The working tree is not clean: a release commits only the release, so commit or remove every change first, untracked files included" "$status"
fi

branch=$(git symbolic-ref --quiet --short HEAD || true)
if [[ -z "$branch" ]]; then
  refuse "HEAD is detached: a release is cut from main, so run \`git switch main\` first"
fi
if [[ "$branch" != "main" ]]; then
  refuse "On branch $branch: a release is cut from main, so run \`git switch main\` first"
fi

# One ls-remote answers both remaining questions: where origin's main is, and
# which of the tags origin already holds. Origin is read before this clone's tags:
# a root's tag that origin holds alone is a release that stopped after publishing
# it, and only origin can tell that apart from a tag a run left unpushed.
refs=("refs/heads/main" "refs/tags/$version")
for tag in "${integration_tags[@]}"; do
  refs+=("refs/tags/$tag")
done
remote=$(git ls-remote origin "${refs[@]}") || refuse "Could not read origin with \`git ls-remote origin\`: check the remote and your access to it, then release again"

existing=$(printf '%s\n' "$remote" | awk '$2 ~ /^refs\/tags\// && $2 !~ /\^\{\}$/ { sub(/^refs\/tags\//, "", $2); print $2 }')
local_root=""

if [[ "$resume" != true ]]; then
  if [[ "$existing" == "$version" ]]; then
    refuse "origin already has the root's tag $version and none of its integrations' tags: a release stopped after publishing the root. Continue it with \`bash ./scripts/release.sh --resume $version\`."
  fi
  if [[ -n "$existing" ]]; then
    refuse "origin already has ${existing//$'\n'/ }: $version is released, so pick the next version"
  fi

  for tag in "$version" "${integration_tags[@]}"; do
    if git rev-parse --quiet --verify "refs/tags/$tag" >/dev/null; then
      refuse "Tag $tag already exists in this clone: pick a version that was never released. If a run that stopped early left it unpushed, delete this version's local tags with \`git tag -d\` first."
    fi
  done
else
  # A resumed release tags nothing but the integrations, so none of their tags
  # may exist yet, and the root's has to, here and on origin alike.
  released=$(printf '%s\n' "$existing" | grep -vxF -- "$version" || true)
  if [[ -n "$released" ]]; then
    refuse "origin already has ${released//$'\n'/ }: --resume continues only a release that tagged nothing but the root, and $version's integrations are already tagged"
  fi

  for tag in "${integration_tags[@]}"; do
    if git rev-parse --quiet --verify "refs/tags/$tag" >/dev/null; then
      refuse "Tag $tag already exists in this clone: --resume continues only a release that tagged nothing but the root. If a run that stopped early left it unpushed, delete this version's integration tags with \`git tag -d\` first."
    fi
  done

  local_root=$(git rev-parse --quiet --verify "refs/tags/$version^{commit}" || true)
  if [[ -z "$local_root" ]]; then
    refuse "This clone has no tag $version, and --resume continues from it. If origin has it, fetch it with \`git fetch origin tag $version\` and resume again; otherwise release without --resume."
  fi

  # A tag created by hand may be annotated, and the commit it names is then on
  # the peeled line.
  remote_root=$(printf '%s\n' "$remote" | awk -v ref="refs/tags/$version" '$2 == ref "^{}" { peeled = $1 } $2 == ref { direct = $1 } END { print (peeled != "" ? peeled : direct) }')
  if [[ -z "$remote_root" ]]; then
    refuse "origin has no tag $version: --resume continues only a release whose root's tag is public. Delete the local one with \`git tag -d $version\`, then release without --resume."
  fi
  if [[ "$remote_root" != "$local_root" ]]; then
    refuse "Tag $version is ${local_root:0:12} in this clone but ${remote_root:0:12} on origin, and the integrations are released against the one origin publishes. Replace the local one with \`git tag -d $version && git fetch origin tag $version\`, then resume again."
  fi
fi

head=$(git rev-parse HEAD)
remote_main=$(printf '%s\n' "$remote" | awk '$2 == "refs/heads/main" { print $1 }')
if [[ -z "$remote_main" ]]; then
  refuse "origin has no main branch: push main first, then release again"
fi
if [[ "$head" != "$remote_main" ]]; then
  refuse "HEAD (${head:0:12}) is not origin's main (${remote_main:0:12}), and the release has to land on exactly what origin holds. Nothing was fetched: push your commits to main, or pull origin's, then release again."
fi

# The integrations are released from the commit the root's tag holds, so that
# both carry the code the gate passed on. A main that moved past the tag holds
# code the root's tag does not.
if [[ "$resume" == true && "$local_root" != "$head" ]]; then
  refuse "Tag $version is ${local_root:0:12}, not HEAD (${head:0:12}): main has moved since the root's tag, and the integrations are released only from the commit it holds. $version stays a release of the root alone, so release the next version instead."
fi

# mod-tidy.sh runs before the gate, and the release stops if it changes
# anything. A tidy that changes nothing proves the tree at HEAD is already tidy,
# so the gate then runs on exactly the files the root's tag holds: the root is
# released unchanged from what was gated. An untidy tree is also refused in
# seconds rather than after the gate's minutes. The tidy runs in a dry run as
# well, since it is one of the checks, and what it changes is left in the tree
# to be committed.
#
# Every `go mod tidy` has to run with GOWORK=off: in workspace mode the resolved
# hashes land in go.work.sum, which is gitignored, so the module's own go.sum
# would be published incomplete and standalone builds - what a consumer and CI
# both do, since go.work is absent on a fresh clone - fail on a missing go.sum
# entry. mod-tidy.sh does so, and runs `go work sync` before tidying.
echo "==> Checking that the modules are tidy (scripts/mod-tidy.sh)..."
bash ./scripts/mod-tidy.sh || refuse "scripts/mod-tidy.sh failed: nothing was committed, tagged or pushed. Fix what it reports, then release again."

status=$(git status --porcelain --untracked-files=normal)
if [[ -n "$status" ]]; then
  if [[ "$resume" == true ]]; then
    refuse_changes "The tree is not tidy at the root's tag $version: scripts/mod-tidy.sh changed the files below and left them in the tree. The integrations cannot be released from that commit: commit the tidy as a change of its own, push it to main, and release the next version" "$status"
  fi
  refuse_changes "The tree is not tidy: scripts/mod-tidy.sh changed the files below and left them in the tree. Commit the tidy as a change of its own and push it to main, then release again" "$status"
fi

# The gate is the one CI runs, mise.toml's `ci` task: lint, test and govulncheck
# across every module, and the tests need Docker running. A pushed tag is public
# at once and the module proxy never forgets it, so the gate passes before the
# release tags anything rather than after. A resumed release runs it again: the
# integrations' tags are not public yet, and the gate is what guards each tag.
echo "==> Running the gate (mise run ci)..."
mise run ci || refuse "The gate failed: nothing was changed, committed, tagged or pushed. Fix what it reports, then release again."

# Phase 1: Tag the root alone, and wait until the module proxy serves it.
#
# Each integration is tidied against the root's tag, which `go mod tidy` resolves
# only once the module proxy serves it. No integration requires another, so the
# root is the only module that has to be public first. The wait is fatal: an
# integration cannot be tidied without it.
if [[ "$resume" == true ]]; then
  echo "==> The root's tag $version is already public, so the release goes on from the module proxy"
else
  echo "==> Tagging the root..."
  run git tag "$version"

  # Only the root's tag is pushed. `git push --tags` would publish every other tag
  # in the clone as well, and a published tag cannot be taken back.
  run git push origin "$version" || refuse "Could not push the tag $version: nothing is public yet. Delete the local tag with \`git tag -d $version\`, then release again."
fi

echo "==> Waiting for the module proxy to serve the root..."
wait_for_proxy "$root_module" "$version" || refuse "The module proxy did not serve $root_module@$version within ten minutes. The root's tag $version is public, and no integration is changed or tagged yet. Continue later with \`bash ./scripts/release.sh --resume $version\`, which asks the proxy again before it changes anything."

# Phase 2: Point each integration at the root's tag, and tidy it against it.
#
# The local replace goes and the root is required at its tag. The tidy then adds
# that tag's go.sum entries, which a module replaced by a local directory never
# needs, and the vet builds the integration with the workspace off, exactly as a
# consumer and CI build it from a fresh clone: a go.sum entry missing here fails
# now, before anything is committed.
echo "==> Preparing the integrations against the root's tag..."
if [[ "$dry_run" == true ]]; then
  echo "  (dry run: an integration can be tidied against $version only once the module proxy serves that tag, so the tidy and the vet are printed rather than run)"
fi

prepare_failed="The root's tag $version is public, and no integration is committed or tagged. Put the integrations back with \`git restore integration\`, then continue with \`bash ./scripts/release.sh --resume $version\` once the cause is gone. A cause that needs a commit ships under the next version instead."
for mod in $integrations; do
  echo "  -> Preparing integration/$mod"
  run go mod edit \
    -require "$root_module@$version" \
    -dropreplace "$root_module" \
    "./integration/$mod/go.mod" || refuse "Editing integration/$mod/go.mod failed (see above). $prepare_failed"
  run env GOWORK=off go -C "./integration/$mod" mod tidy || refuse "Tidying integration/$mod against $version failed (see above). $prepare_failed"
  run env GOWORK=off go -C "./integration/$mod" vet ./... || refuse "Vetting integration/$mod on its own failed (see above). $prepare_failed"
done

# Phase 3: Commit, tag and push the integrations.
echo "==> Committing the release..."
run git commit -am "version: Release $version" || refuse "Could not commit the release (see above). $prepare_failed"
run git push origin main || refuse "Could not push the release commit (see above): the root's tag $version is public, and the release commit is only in this clone. Drop it with \`git reset --hard $version\`, then continue with \`bash ./scripts/release.sh --resume $version\`."

echo "==> Tagging the integrations..."
for tag in "${integration_tags[@]}"; do
  run git tag "$tag"
done

# Only the tags this release created are pushed, all of them or none. `git push
# --tags` would publish every other tag in the clone as well, and a published tag
# cannot be taken back.
run git push --atomic origin "${integration_tags[@]}" || refuse "Could not push the integrations' tags (see above): the root's tag $version and the release commit are public, and the integrations' tags are only in this clone. Push them with \`git push --atomic origin ${integration_tags[*]}\`. $post_release_by_hand"

# Phase 4: Restore local replace directives for development.
#
# With the replaces back, the root's go.sum entries the release added are ones
# no module needs any more, so the restored tree is tidied again: left in, they
# would make the next release's tidy check refuse a tree nobody touched.
echo "==> Restoring local replace directives..."
for mod in $integrations; do
  run go mod edit -replace "$root_module=../../" "./integration/$mod/go.mod" || refuse "Restoring the replace in integration/$mod/go.mod failed (see above): $version is released and every tag is public. $post_release_by_hand"
done
run bash ./scripts/mod-tidy.sh || refuse "scripts/mod-tidy.sh failed after restoring the replaces (see above): $version is released and every tag is public. $post_release_by_hand"

run git commit -am "version(scripts): Apply post-release of $version" || refuse "Could not commit the post-release (see above): $version is released and every tag is public. $post_release_by_hand"
run git push origin main || refuse "Could not push the post-release commit (see above): $version is released and every tag is public. Push it with \`git push origin main\`."

if [[ "$dry_run" != true ]]; then
  echo "==> Release $version complete!"
fi

# Phase 5: Warm the Go module proxy cache for the integrations (best-effort).
# This prevents cached 404s when a consumer resolves a freshly-tagged module
# before the proxy has seen it. The root was already served in phase 1.
echo "==> Warming Go module proxy cache..."

for mod in $integrations; do
  wait_for_proxy "$root_module/integration/$mod" "$version" || echo "  !! $root_module/integration/$mod@$version not available on proxy after ten minutes (non-fatal)" 1>&2
done

if [[ "$dry_run" == true ]]; then
  echo ""
  echo "==> Dry run of $version complete: every check passed and the tidy changed nothing. Nothing was tagged, edited, committed or pushed, and the module proxy was not asked."
  echo "    The integrations' tidy and vet were printed, not run: they resolve the root's tag $version, which a release publishes before it runs them."
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
      `go get github.com/mountaya/helix.go@<tag>` in a downstream
      project. This is one of pkg.go.dev's three documented paths for
      ensuring a version is indexed and it is lossless.

    * After 30 min, check pkg.go.dev/<module>?tab=versions. If anything
      is still missing, file a pkgsite issue:
      https://github.com/golang/go/issues/new?template=02-pkgsite.md
      Do NOT click the "Request" button or otherwise poke the missing
      URL — the Go team's manual re-index is the only path that clears
      a cached build failure.

EOF
