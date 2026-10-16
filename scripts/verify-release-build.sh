#!/usr/bin/env bash
# verify-release-build.sh [--assets DIR] [--node-version X.Y.Z] TAG - rebuilds the release TAG
# from its tagged commit and compares it with the tarballs of the draft (SEC-07): the owner
# runs it on their own machine (make verify-release-build TAG=vX.Y.Z), and sign-release.sh
# runs it before asking to sign. The release build is reproducible: panel, agent and files of
# a clean checkout, built with goreleaser's flags, the Go toolchain of go.mod and the Node
# version of .node-version, are byte-identical to the release's.
#
#   1. a clean clone of the tagged commit (on origin/main) in a temp dir;
#   2. the web build there: npm ci --ignore-scripts and make web-build with exactly the Node of
#      .node-version (fnm exec when fnm is there, else node from PATH, which must match);
#   3. panel, agent and files for linux/amd64 and linux/arm64 with CGO_ENABLED=0, -trimpath,
#      -ldflags "-s -w -X main.version=<version>" and GOTOOLCHAIN=go<go.mod version>;
#   4. each draft tarball (--assets DIR: already downloaded and checked by sign-release.sh;
#      otherwise downloaded here by release ID, uploader github-actions[bot]) is extracted into
#      a temp dir: only regular files and directories, exactly panel, agent, files and the
#      files of deploy/; the binaries must be byte-identical to the rebuild and deploy/ to the
#      tagged commit's.
#
# Everything (clone, Go module and build caches, the Go toolchain, the npm cache, node_modules)
# lives in one temp dir that is removed at the end; nothing is installed. --node-version is for
# tags before .node-version existed. Needs git, go (1.21 or later, it fetches the toolchain of
# go.mod), node (or fnm), gh and tar. Bash 3.2 compatible (macOS).
# VERIFY_RELEASE_PREBUILT=DIR (tests only) takes the rebuilt binaries from DIR/<arch>/.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=release-lib.sh
. "$here/release-lib.sh"

assets=""
node_override=""
while [ "$#" -gt 1 ]; do
  case "$1" in
  --assets)
    assets="$2"
    shift 2
    ;;
  --node-version)
    node_override="$2"
    shift 2
    ;;
  *) break ;;
  esac
done
if [ "$#" -ne 1 ]; then
  echo "usage: scripts/verify-release-build.sh [--assets DIR] [--node-version X.Y.Z] vX.Y.Z" >&2
  exit 2
fi
tag="$1"
rl_check_tag "$tag"
v="$rl_version"
for c in git go tar; do
  command -v "$c" >/dev/null 2>&1 || rl_die "$c is missing"
done

tmp="$(mktemp -d "${TMPDIR:-/tmp}/mc-panel-verify.XXXXXX")"
cleanup() {
  chmod -R u+w "$tmp" 2>/dev/null || true # the Go module cache is read-only
  rm -rf "$tmp"
}
trap cleanup EXIT

echo "==> $tag: rebuild from the tagged commit"
commit="$(rl_tag_commit "$tag")" || exit 1
src="$tmp/src"
git clone --quiet --shared --no-checkout "$(git rev-parse --show-toplevel)" "$src"
git -C "$src" -c advice.detachedHead=false checkout --quiet "$commit"

# The flags must be goreleaser's of this commit; a change there needs a change here.
for want in '-trimpath' '-s -w -X main.version={{ .Version }}' 'CGO_ENABLED=0'; do
  grep -qF -- "$want" "$src/.goreleaser.yaml" ||
    rl_die ".goreleaser.yaml at $tag no longer has '$want'; update verify-release-build.sh to its build flags"
done

node_version="$node_override"
if [ -z "$node_version" ]; then
  [ -f "$src/.node-version" ] || rl_die "$tag has no .node-version; pass --node-version X.Y.Z (the Node of its release run)"
  node_version="$(tr -d ' \nv' <"$src/.node-version")"
fi
case "$node_version" in
[0-9]*.[0-9]*.[0-9]*) ;;
*) rl_die "unexpected Node version '$node_version'" ;;
esac
# run_node CMD...: CMD with exactly Node $node_version.
if command -v fnm >/dev/null 2>&1; then
  run_node() { fnm exec --using="$node_version" "$@"; }
  have="$(run_node node --version 2>/dev/null)" ||
    rl_die "fnm has no Node $node_version; get it with: fnm install $node_version"
else
  run_node() { "$@"; }
  have="$(node --version 2>/dev/null)" || have="none"
fi
[ "$have" = "v$node_version" ] ||
  rl_die "the release was built with Node $node_version, but this machine runs $have; get it with: fnm install $node_version (https://github.com/Schniz/fnm), then run this again"

go_version="$(awk '$1 == "go" { print $2; exit }' "$src/go.mod")"
[ -n "$go_version" ] || rl_die "no go line in go.mod at $tag"
# Caches, the Go toolchain and npm's cache in the temp dir: nothing outside it changes.
# Modules and the toolchain come through the Go proxy and are checked against the checksum DB.
export GOPATH="$tmp/gopath" GOMODCACHE="$tmp/gopath/pkg/mod" GOCACHE="$tmp/gocache" GOFLAGS=-modcacherw \
  GOTOOLCHAIN="go$go_version" GOENV=off GOWORK=off GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org \
  GONOSUMDB='' GONOPROXY='' GOPRIVATE='' GOINSECURE='' \
  npm_config_cache="$tmp/npm-cache" npm_config_update_notifier=false
unset GOAMD64 GOARM64 GOOS GOARCH GOEXPERIMENT CGO_ENABLED

out="$tmp/out"
if [ -n "${VERIFY_RELEASE_PREBUILT:-}" ]; then
  out="$VERIFY_RELEASE_PREBUILT"
else
  echo "==> web build (Node $node_version)"
  (cd "$src/web" && run_node npm ci --ignore-scripts --no-audit --no-fund >/dev/null) || rl_die "npm ci failed"
  (cd "$src" && run_node make web-build >/dev/null) || rl_die "the web build failed"
  for arch in amd64 arm64; do
    echo "==> go build linux/$arch (go$go_version)"
    for b in panel agent files; do
      if ! (cd "$src" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
        -ldflags "-s -w -X main.version=$v" -o "$out/$arch/$b" "./cmd/$b") >"$tmp/build.log" 2>&1; then
        cat "$tmp/build.log" >&2
        rl_die "go build of $b ($arch) failed"
      fi
    done
  done
fi

if [ -z "$assets" ]; then
  command -v gh >/dev/null 2>&1 || rl_die "gh is missing"
  rl_release_id "$tag"
  assets="$tmp/assets"
  mkdir "$assets"
  rl_download_assets "$assets" "mc-panel_${v}_linux_amd64.tar.gz" "mc-panel_${v}_linux_arm64.tar.gz"
fi

mkdir "$tmp/tree"
git -C "$src" archive "$commit" deploy | tar -x -C "$tmp/tree" # $tmp/tree/deploy/...
(cd "$tmp/tree" && find deploy -type f | LC_ALL=C sort) >"$tmp/deploy.list"
fail=0
for arch in amd64 arm64; do
  tarball="$assets/mc-panel_${v}_linux_${arch}.tar.gz"
  [ -f "$tarball" ] || rl_die "missing $tarball"
  echo "==> compare mc-panel_${v}_linux_${arch}.tar.gz"
  # names: relative, no "..", before anything is extracted
  if tar -tzf "$tarball" | grep -qE '(^/|(^|/)\.\.(/|$))'; then
    rl_die "$tarball has absolute or .. paths"
  fi
  x="$tmp/x-$arch"
  mkdir "$x"
  tar -xzf "$tarball" -C "$x" || rl_die "cannot extract $tarball"
  if [ -n "$(find "$x" ! -type f ! -type d)" ]; then
    rl_die "$tarball holds entries that are neither files nor directories: $(find "$x" ! -type f ! -type d | sed "s|^$x/||" | tr '\n' ' ')"
  fi
  (cd "$x" && find . -type f | sed 's|^\./||' | LC_ALL=C sort) >"$tmp/got.list"
  { printf '%s\n' agent files panel; cat "$tmp/deploy.list"; } | LC_ALL=C sort >"$tmp/want.list"
  if ! cmp -s "$tmp/got.list" "$tmp/want.list"; then
    echo "  entries differ (< tarball, > expected):" >&2
    diff "$tmp/got.list" "$tmp/want.list" | grep '^[<>]' | sed 's/^/    /' >&2 || true
    fail=1
    continue
  fi
  for b in panel agent files; do
    if cmp -s "$x/$b" "$out/$arch/$b"; then
      echo "  $b: identical ($(rl_sha256 "$x/$b"))"
    else
      echo "  $b: DIFFERS from the rebuild (release $(rl_sha256 "$x/$b"), rebuild $(rl_sha256 "$out/$arch/$b"))" >&2
      fail=1
    fi
  done
  while read -r f; do
    if ! cmp -s "$x/$f" "$tmp/tree/$f"; then
      echo "  $f: DIFFERS from $tag" >&2
      fail=1
    fi
  done <"$tmp/deploy.list"
done
[ "$fail" = 0 ] || rl_die "the release tarballs of $tag are not what its commit builds; do not sign"
echo "OK: the tarballs of $tag are exactly what $commit builds"
