#!/usr/bin/env bash
# sign-release.sh [--skip-build-check] TAG [KEY] - signs and publishes the draft release TAG
# (SEC-07): the owner runs it on their own machine from a checkout of the repo
# (make sign-release TAG=vX.Y.Z), with the release key that never touches GitHub (default
# ~/.ssh/mc-panel-release, an ed25519 key with a passphrase or on a hardware key). Signing is
# the human approval of the release, so nothing on GitHub is taken on trust:
#
#   1. exactly one release has the tag TAG; it is a draft whose author is exactly
#      github-actions[bot] (the release workflow), and its ID is used from then on;
#   2. the tagged commit is on origin/main, and scripts/install/release-signers at that commit
#      holds no TEST-ONLY key;
#   3. the assets are exactly the two tarballs, install.sh and checksums.txt, each uploaded by
#      github-actions[bot]; every one is listed once in checksums.txt and matches it (and the
#      digest GitHub reports);
#   4. install.sh is rebuilt here from the tagged commit (assemble.sh, deterministic) and must
#      be byte-identical to the draft's;
#   5. the tarballs must contain exactly what the tagged commit builds (verify-release-build.sh:
#      the build is reproducible); --skip-build-check skips this with a loud warning, for when
#      the toolchain is not available;
#   6. after a yes, checksums.txt is signed (ssh-keygen -Y sign, namespace mc-panel-release; the
#      key's passphrase is asked for) and the signature verified against release-signers at
#      the tag (what that install.sh trusts);
#   7. checksums.txt.sig is uploaded; the assets of the release ID are listed again (names,
#      uploaders, digests against the local files), and only then the release is published.
#
# Needs gh (logged in), git, ssh-keygen and shasum or sha256sum, plus what
# verify-release-build.sh needs. Bash 3.2 compatible (macOS). SIGN_RELEASE_VERIFY (tests only)
# replaces verify-release-build.sh.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=release-lib.sh
. "$here/release-lib.sh"
ns=mc-panel-release
principal=mc-panel-release
verify="${SIGN_RELEASE_VERIFY:-$here/verify-release-build.sh}"

skip_build=0
if [ "${1:-}" = --skip-build-check ]; then
  skip_build=1
  shift
fi
if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: scripts/sign-release.sh [--skip-build-check] vX.Y.Z [KEY]" >&2
  exit 2
fi
tag="$1"
key="${2:-$HOME/.ssh/mc-panel-release}"
rl_check_tag "$tag"
v="$rl_version"
[ -f "$key" ] || rl_die "no signing key at $key (create it: docs/dev/README.md, Release signing)"
for c in gh git ssh-keygen; do
  command -v "$c" >/dev/null 2>&1 || rl_die "$c is missing"
done

work="$(mktemp -d "${TMPDIR:-/tmp}/mc-panel-sign.XXXXXX")"
trap 'rm -rf "$work"' EXIT

expected="checksums.txt
install.sh
mc-panel_${v}_linux_amd64.tar.gz
mc-panel_${v}_linux_arm64.tar.gz"

echo "==> $tag: release"
rl_release_id "$tag"
[ "$rl_draft" = true ] || rl_die "$tag is already published; a published release cannot be signed afterwards"
commit="$(rl_tag_commit "$tag")" || exit 1
git show "$commit:scripts/install/release-signers" >"$work/release-signers" 2>/dev/null ||
  rl_die "scripts/install/release-signers is missing at $tag"
if grep -q TEST-ONLY "$work/release-signers"; then
  rl_die "release-signers at $tag still holds a TEST-ONLY key; that install.sh trusts no real key"
fi

echo "==> $tag: assets of release $rl_rid"
rl_list_assets >"$work/assets.txt" || rl_die "cannot list the assets of release $rl_rid"
[ "$(awk '{ print $2 }' "$work/assets.txt")" = "$expected" ] ||
  rl_die "the assets of $tag are not what the release workflow makes: $(awk '{ print $2 }' "$work/assets.txt" | tr '\n' ' ')(a stray checksums.txt.sig? delete it in the draft first)"
mkdir "$work/assets"
# shellcheck disable=SC2086 # the four names
rl_download_assets "$work/assets" $expected

listed="$(awk '{ n = $2; sub(/^\*/, "", n); print n }' "$work/assets/checksums.txt" | LC_ALL=C sort)"
[ "$listed" = "$(printf '%s\n' "$expected" | grep -vx checksums.txt)" ] ||
  rl_die "checksums.txt lists: $(echo "$listed" | tr '\n' ' ')"
while read -r sum name; do
  name="${name#\*}"
  [ "$(rl_sha256 "$work/assets/$name")" = "$sum" ] || rl_die "$name does not match checksums.txt"
done <"$work/assets/checksums.txt"

echo "==> $tag: rebuild install.sh from $commit"
mkdir "$work/src"
git archive "$commit" | tar -x -C "$work/src" || rl_die "cannot extract $commit"
bash "$work/src/scripts/install/assemble.sh" "$v" "$work/install.sh" >/dev/null || rl_die "assemble.sh failed"
cmp -s "$work/install.sh" "$work/assets/install.sh" ||
  rl_die "install.sh of the draft differs from install.sh assembled from $commit; do not sign"

if [ "$skip_build" = 1 ]; then
  {
    echo
    echo "=================================================================================="
    echo "WARNING: --skip-build-check: the tarballs (the binaries that run as root) are NOT"
    echo "compared with a rebuild of $commit. Anyone who controlled the GitHub release"
    echo "before now could have replaced them together with checksums.txt, and you would"
    echo "sign that. Skip only if you cannot build here and trust the release run's log."
    echo "=================================================================================="
  } >&2
else
  "$verify" --assets "$work/assets" "$tag" ||
    rl_die "the build check failed; do not sign (--skip-build-check only if you cannot build here)"
fi

echo
echo "Release:  $tag (id $rl_rid, $commit$([ "$rl_prerelease" = true ] && echo ', pre-release'))"
echo "Key:      $(ssh-keygen -l -f "$key" 2>/dev/null || echo "$key")"
echo "Build:    $([ "$skip_build" = 1 ] && echo 'NOT CHECKED (--skip-build-check)' || echo 'tarballs identical to the rebuild')"
echo "checksums.txt:"
sed 's/^/  /' "$work/assets/checksums.txt"
printf 'Sign and publish %s? [y/N] ' "$tag"
read -r ans
case "$ans" in
y | Y | yes) ;;
*) rl_die "not signed; the draft stays unpublished" ;;
esac

echo "==> $tag: sign"
ssh-keygen -Y sign -f "$key" -n "$ns" "$work/assets/checksums.txt" || rl_die "signing failed"
ssh-keygen -Y verify -f "$work/release-signers" -I "$principal" -n "$ns" \
  -s "$work/assets/checksums.txt.sig" <"$work/assets/checksums.txt" ||
  rl_die "the signature does not verify against release-signers at $tag: is $key a key listed there?"

echo "==> $tag: upload, check again, publish"
gh release upload "$tag" -R "$rl_repo" "$work/assets/checksums.txt.sig" || rl_die "upload of checksums.txt.sig failed"
me="$(gh api user --jq .login)" || rl_die "cannot read the gh user"
rl_list_assets >"$work/assets2.txt" || rl_die "cannot list the assets of release $rl_rid"
[ "$(awk '{ print $2 }' "$work/assets2.txt")" = "$(printf '%s\nchecksums.txt.sig\n' "$expected" | LC_ALL=C sort)" ] ||
  rl_die "after the upload release $rl_rid holds: $(awk '{ print $2 }' "$work/assets2.txt" | tr '\n' ' '); not published"
while read -r id name uploader digest; do
  want="$rl_bot"
  [ "$name" != checksums.txt.sig ] || want="$me"
  [ "$uploader" = "$want" ] || rl_die "$name of release $rl_rid is now from '$uploader', not '$want'; not published"
  if [ "$digest" = - ]; then
    gh api -H 'Accept: application/octet-stream' "repos/$rl_repo/releases/assets/$id" >"$work/recheck" ||
      rl_die "download of $name failed; not published"
    digest="sha256:$(rl_sha256 "$work/recheck")"
  fi
  [ "$digest" = "sha256:$(rl_sha256 "$work/assets/$name")" ] ||
    rl_die "$name of release $rl_rid changed since it was checked ($digest); not published"
done <"$work/assets2.txt"
latest=true
[ "$rl_prerelease" != true ] || latest=false
gh api -X PATCH "repos/$rl_repo/releases/$rl_rid" -F draft=false -f make_latest="$latest" >/dev/null ||
  rl_die "publishing release $rl_rid failed"
[ "$(gh api "repos/$rl_repo/releases/$rl_rid" --jq .draft)" = false ] || rl_die "$tag is still a draft"
echo "published $tag (release $rl_rid) with checksums.txt.sig"
