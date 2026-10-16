#!/usr/bin/env bash
# sign-release.sh TAG [KEY] - signs and publishes the draft release TAG (SEC-07): the owner runs
# it on their own machine from a checkout of the repo (make sign-release TAG=vX.Y.Z), with the
# release key that never touches GitHub (default ~/.ssh/mc-panel-release, an ed25519 key with
# a passphrase or on a hardware key). Signing is the human approval of the release, so nothing
# on GitHub is taken on trust:
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
#   5. after a yes, checksums.txt is signed (ssh-keygen -Y sign, namespace mc-panel-release) and
#      the signature verified against release-signers at the tag (what that install.sh trusts);
#   6. checksums.txt.sig is uploaded; the assets of the release ID are listed again (names,
#      uploaders, digests against the local files), and only then the release is published.
#
# The tarballs are checked against checksums.txt only; docs/dev/README.md ("Release signing")
# describes the residual risk. Needs gh (logged in), git, ssh-keygen and shasum or sha256sum.
# Bash 3.2 compatible (macOS). SIGN_RELEASE_REPO overrides the repository (tests).
set -euo pipefail

repo="${SIGN_RELEASE_REPO:-Bernotese/mc-panel}"
ns=mc-panel-release
principal=mc-panel-release
bot='github-actions[bot]'

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
  sha256() { sha256sum "$1" | awk '{ print $1 }'; }
else
  sha256() { shasum -a 256 "$1" | awk '{ print $1 }'; }
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/mc-panel-sign.XXXXXX")"
trap 'rm -rf "$work"' EXIT

expected="checksums.txt
install.sh
mc-panel_${v}_linux_amd64.tar.gz
mc-panel_${v}_linux_arm64.tar.gz"

# list_assets: "id name uploader digest" per asset of the release, sorted by name.
list_assets() {
  gh api "repos/$repo/releases/$rid/assets?per_page=100" \
    --jq '.[] | "\(.id) \(.name) \(.uploader.login) \(.digest // "-")"' | LC_ALL=C sort -k2,2
}

echo "==> $tag: release"
ids="$(gh api --paginate "repos/$repo/releases?per_page=100" --jq ".[] | select(.tag_name == \"$tag\") | .id")" ||
  die "cannot list the releases of $repo"
n="$(printf '%s\n' "$ids" | grep -c '^[0-9][0-9]*$' || true)"
[ "$n" = 1 ] || die "expected exactly one release with tag $tag, found $n (delete stray drafts first)"
rid="$ids"
info="$(gh api "repos/$repo/releases/$rid" --jq '"\(.draft) \(.prerelease) \(.author.login) \(.tag_name)"')"
# shellcheck disable=SC2086 # split into draft, prerelease, author and tag on purpose
set -- $info
[ "$4" = "$tag" ] || die "release $rid has tag $4, not $tag"
[ "$1" = true ] || die "$tag is already published; a published release cannot be signed afterwards"
prerelease="$2"
[ "$3" = "$bot" ] || die "$tag was created by '$3', not by the release workflow ($bot)"

git fetch --quiet origin main "refs/tags/$tag:refs/tags/$tag" || die "cannot fetch origin/main and $tag"
commit="$(git rev-parse --verify --quiet "$tag^{commit}")" || die "tag $tag not found"
git merge-base --is-ancestor "$commit" origin/main || die "$tag ($commit) is not on origin/main"
git show "$commit:scripts/install/release-signers" >"$work/release-signers" 2>/dev/null ||
  die "scripts/install/release-signers is missing at $tag"
if grep -q TEST-ONLY "$work/release-signers"; then
  die "release-signers at $tag still holds a TEST-ONLY key; that install.sh trusts no real key"
fi

echo "==> $tag: assets of release $rid"
list_assets >"$work/assets.txt" || die "cannot list the assets of release $rid"
[ "$(awk '{ print $2 }' "$work/assets.txt")" = "$expected" ] ||
  die "the assets of $tag are not what the release workflow makes: $(awk '{ print $2 }' "$work/assets.txt" | tr '\n' ' ')(a stray checksums.txt.sig? delete it in the draft first)"
mkdir "$work/assets"
while read -r id name uploader digest; do
  [ "$uploader" = "$bot" ] || die "$name was uploaded by '$uploader', not by the release workflow ($bot)"
  gh api -H 'Accept: application/octet-stream' "repos/$repo/releases/assets/$id" >"$work/assets/$name" ||
    die "download of $name failed"
  if [ "$digest" != - ] && [ "$digest" != "sha256:$(sha256 "$work/assets/$name")" ]; then
    die "$name does not match the digest GitHub reports ($digest)"
  fi
done <"$work/assets.txt"

listed="$(awk '{ n = $2; sub(/^\*/, "", n); print n }' "$work/assets/checksums.txt" | LC_ALL=C sort)"
[ "$listed" = "$(printf '%s\n' "$expected" | grep -vx checksums.txt)" ] ||
  die "checksums.txt lists: $(echo "$listed" | tr '\n' ' ')"
while read -r sum name; do
  name="${name#\*}"
  [ "$(sha256 "$work/assets/$name")" = "$sum" ] || die "$name does not match checksums.txt"
done <"$work/assets/checksums.txt"

echo "==> $tag: rebuild install.sh from $commit"
mkdir "$work/src"
git archive "$commit" | tar -x -C "$work/src" || die "cannot extract $commit"
bash "$work/src/scripts/install/assemble.sh" "$v" "$work/install.sh" >/dev/null || die "assemble.sh failed"
cmp -s "$work/install.sh" "$work/assets/install.sh" ||
  die "install.sh of the draft differs from install.sh assembled from $commit; do not sign"

echo
echo "Release:  $tag (id $rid, $commit$([ "$prerelease" = true ] && echo ', pre-release'))"
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

echo "==> $tag: upload, check again, publish"
gh release upload "$tag" -R "$repo" "$work/assets/checksums.txt.sig" || die "upload of checksums.txt.sig failed"
me="$(gh api user --jq .login)" || die "cannot read the gh user"
list_assets >"$work/assets2.txt" || die "cannot list the assets of release $rid"
[ "$(awk '{ print $2 }' "$work/assets2.txt")" = "$(printf '%s\nchecksums.txt.sig\n' "$expected" | LC_ALL=C sort)" ] ||
  die "after the upload release $rid holds: $(awk '{ print $2 }' "$work/assets2.txt" | tr '\n' ' '); not published"
while read -r id name uploader digest; do
  want="$bot"
  [ "$name" != checksums.txt.sig ] || want="$me"
  [ "$uploader" = "$want" ] || die "$name of release $rid is now from '$uploader', not '$want'; not published"
  if [ "$digest" = - ]; then
    gh api -H 'Accept: application/octet-stream' "repos/$repo/releases/assets/$id" >"$work/recheck" ||
      die "download of $name failed; not published"
    digest="sha256:$(sha256 "$work/recheck")"
  fi
  [ "$digest" = "sha256:$(sha256 "$work/assets/$name")" ] ||
    die "$name of release $rid changed since it was checked ($digest); not published"
done <"$work/assets2.txt"
latest=true
[ "$prerelease" != true ] || latest=false
gh api -X PATCH "repos/$repo/releases/$rid" -F draft=false -f make_latest="$latest" >/dev/null ||
  die "publishing release $rid failed"
[ "$(gh api "repos/$repo/releases/$rid" --jq .draft)" = false ] || die "$tag is still a draft"
echo "published $tag (release $rid) with checksums.txt.sig"
