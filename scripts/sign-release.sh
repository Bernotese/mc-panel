#!/usr/bin/env bash
# sign-release.sh TAG [KEY] - signs and publishes the draft release TAG (SEC-07): the owner runs
# it on their own machine (make sign-release TAG=vX.Y.Z), with the release key that never
# touches GitHub (default ~/.ssh/mc-panel-release, an ed25519 key with a passphrase or on a
# hardware key). Signing is the human approval of the release:
#
#   1. TAG is a draft created by the release workflow, its commit is on origin/main, and
#      scripts/install/release-signers at TAG holds no TEST-ONLY key;
#   2. the assets are downloaded: exactly the two tarballs, install.sh and checksums.txt, every
#      one listed once in checksums.txt and matching it, install.sh built for this version;
#   3. after a yes, checksums.txt is signed (ssh-keygen -Y sign, namespace mc-panel-release),
#      the signature is verified against release-signers at TAG (what that install.sh trusts),
#      uploaded as checksums.txt.sig and the release is published.
#
# Needs gh (logged in), git, ssh-keygen and shasum or sha256sum. Bash 3.2 compatible (macOS).
set -euo pipefail

repo=Bernotese/mc-panel
ns=mc-panel-release
principal=mc-panel-release

die() {
  echo "sign-release: $*" >&2
  exit 1
}

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: scripts/sign-release.sh vX.Y.Z [KEY]" >&2
  exit 2
fi
tag="$1"
key="${2:-$HOME/.ssh/mc-panel-release}"
case "$tag" in
v[0-9]*.[0-9]*.[0-9]*) ;;
*) die "TAG must look like vX.Y.Z, not '$tag'" ;;
esac
case "$tag" in
*[!0-9A-Za-z.+-]*) die "TAG '$tag' has unexpected characters" ;;
esac
v="${tag#v}"
[ -f "$key" ] || die "no signing key at $key (create it: docs/dev/README.md, Release signing)"
for c in gh git ssh-keygen; do
  command -v "$c" >/dev/null 2>&1 || die "$c is missing"
done
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$@"; }
else
  sha256() { shasum -a 256 "$@"; }
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/mc-panel-sign.XXXXXX")"
trap 'rm -rf "$work"' EXIT

echo "==> $tag: release state"
draft="$(gh release view "$tag" -R "$repo" --json isDraft --jq .isDraft)" || die "no release $tag (has the release workflow finished?)"
[ "$draft" = true ] || die "$tag is already published; a published release cannot be signed afterwards"
prerelease="$(gh release view "$tag" -R "$repo" --json isPrerelease --jq .isPrerelease)"
author="$(gh release view "$tag" -R "$repo" --json author --jq .author.login)"
case "$author" in
*github-actions*) ;;
*) die "$tag was created by '$author', not by the release workflow (github-actions)" ;;
esac

git fetch --quiet origin main "refs/tags/$tag:refs/tags/$tag" || die "cannot fetch origin/main and $tag"
commit="$(git rev-parse --verify --quiet "$tag^{commit}")" || die "tag $tag not found"
git merge-base --is-ancestor "$commit" origin/main || die "$tag ($commit) is not on origin/main"

git show "$commit:scripts/install/release-signers" >"$work/release-signers" 2>/dev/null ||
  die "scripts/install/release-signers is missing at $tag"
if grep -q TEST-ONLY "$work/release-signers"; then
  die "release-signers at $tag still holds the TEST-ONLY key; that install.sh trusts no real key"
fi

echo "==> $tag: assets"
mkdir "$work/assets"
gh release download "$tag" -R "$repo" -D "$work/assets" || die "download of the assets failed"
expected="checksums.txt
install.sh
mc-panel_${v}_linux_amd64.tar.gz
mc-panel_${v}_linux_arm64.tar.gz"
have="$(cd "$work/assets" && find . -mindepth 1 -maxdepth 1 | sed "s:^\./::" | LC_ALL=C sort)"
if [ "$have" != "$expected" ]; then
  printf 'expected assets:\n%s\nfound:\n%s\n' "$expected" "$have" >&2
  die "the assets of $tag are not what the release workflow makes (an old checksums.txt.sig? delete it in the draft first)"
fi
listed="$(awk '{ n = $2; sub(/^\*/, "", n); print n }' "$work/assets/checksums.txt" | LC_ALL=C sort)"
want_listed="$(printf '%s\n' "$expected" | grep -vx checksums.txt)"
[ "$listed" = "$want_listed" ] || die "checksums.txt lists: $(echo "$listed" | tr '\n' ' ')"
(cd "$work/assets" && sha256 -c checksums.txt) || die "an asset does not match checksums.txt"
grep -qx "MCPANEL_VERSION=$v" "$work/assets/install.sh" || die "install.sh is not built for $v"

echo
echo "Release:  $tag ($commit$([ "$prerelease" = true ] && echo ', pre-release'))"
echo "Key:      $(ssh-keygen -l -f "$key" 2>/dev/null || echo "$key")"
echo "checksums.txt:"
sed 's/^/  /' "$work/assets/checksums.txt"
printf 'Sign and publish %s? [y/N] ' "$tag"
read -r ans
case "$ans" in
y | Y | yes) ;;
*) die "not signed; the draft stays unpublished" ;;
esac

echo "==> $tag: sign"
ssh-keygen -Y sign -f "$key" -n "$ns" "$work/assets/checksums.txt" || die "signing failed"
ssh-keygen -Y verify -f "$work/release-signers" -I "$principal" -n "$ns" \
  -s "$work/assets/checksums.txt.sig" <"$work/assets/checksums.txt" ||
  die "the signature does not verify against release-signers at $tag: is $key a key listed there?"

echo "==> $tag: upload and publish"
gh release upload "$tag" -R "$repo" "$work/assets/checksums.txt.sig" || die "upload of checksums.txt.sig failed"
if [ "$prerelease" = true ]; then
  gh release edit "$tag" -R "$repo" --draft=false
else
  gh release edit "$tag" -R "$repo" --draft=false --latest
fi
[ "$(gh release view "$tag" -R "$repo" --json isDraft --jq .isDraft)" = false ] || die "$tag is still a draft"
echo "published $tag with checksums.txt.sig"
