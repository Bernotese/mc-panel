# shellcheck shell=bash
# release-lib.sh - helpers of sign-release.sh and verify-release-build.sh (SEC-07), sourced by
# both: the owner runs them on their own machine against the draft release of a tag. Nothing on
# GitHub is taken on trust: one release per tag, used by its ID; the author and every asset's
# uploader must be exactly the release workflow. Bash 3.2 compatible (macOS).
# SIGN_RELEASE_REPO overrides the repository (tests). The rl_* variables are read by the
# scripts that source this file.
# shellcheck disable=SC2034

rl_repo="${SIGN_RELEASE_REPO:-Bernotese/mc-panel}"
rl_bot='github-actions[bot]'

rl_die() {
  echo "${0##*/}: $*" >&2
  exit 1
}

if command -v sha256sum >/dev/null 2>&1; then
  rl_sha256() { sha256sum "$1" | awk '{ print $1 }'; }
else
  rl_sha256() { shasum -a 256 "$1" | awk '{ print $1 }'; }
fi

# rl_check_tag TAG: TAG looks like vX.Y.Z[-pre]; sets rl_version (without v).
rl_check_tag() {
  case "$1" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) rl_die "TAG must look like vX.Y.Z, not '$1'" ;;
  esac
  case "$1" in
  *[!0-9A-Za-z.+-]*) rl_die "TAG '$1' has unexpected characters" ;;
  esac
  rl_version="${1#v}"
}

# rl_release_id TAG: sets rl_rid to the ID of the only release with tag TAG and rl_draft,
# rl_prerelease to its state; dies unless there is exactly one and its author is the workflow.
rl_release_id() {
  local ids n info
  ids="$(gh api --paginate "repos/$rl_repo/releases?per_page=100" --jq ".[] | select(.tag_name == \"$1\") | .id")" ||
    rl_die "cannot list the releases of $rl_repo"
  n="$(printf '%s\n' "$ids" | grep -c '^[0-9][0-9]*$' || true)"
  [ "$n" = 1 ] || rl_die "expected exactly one release with tag $1, found $n (delete stray drafts first)"
  rl_rid="$ids"
  info="$(gh api "repos/$rl_repo/releases/$rl_rid" --jq '"\(.draft) \(.prerelease) \(.author.login) \(.tag_name)"')" ||
    rl_die "cannot read release $rl_rid"
  # shellcheck disable=SC2086 # split into draft, prerelease, author and tag on purpose
  set -- $info "$1"
  [ "$4" = "$5" ] || rl_die "release $rl_rid has tag $4, not $5"
  [ "$3" = "$rl_bot" ] || rl_die "$5 was created by '$3', not by the release workflow ($rl_bot)"
  rl_draft="$1"
  rl_prerelease="$2"
}

# rl_list_assets: "id name uploader digest" per asset of release rl_rid, sorted by name.
rl_list_assets() {
  gh api "repos/$rl_repo/releases/$rl_rid/assets?per_page=100" \
    --jq '.[] | "\(.id) \(.name) \(.uploader.login) \(.digest // "-")"' | LC_ALL=C sort -k2,2
}

# rl_download_assets DIR NAME...: downloads the assets NAME of release rl_rid into DIR; each
# must exist, be uploaded by the release workflow and match the digest GitHub reports.
rl_download_assets() {
  local dir="$1" list id name uploader digest want
  shift
  list="$(rl_list_assets)" || rl_die "cannot list the assets of release $rl_rid"
  for want in "$@"; do
    printf '%s\n' "$list" | awk -v n="$want" '$2 == n { f = 1 } END { exit !f }' ||
      rl_die "release $rl_rid has no asset $want"
  done
  while read -r id name uploader digest; do
    case " $* " in
    *" $name "*) ;;
    *) continue ;;
    esac
    [ "$uploader" = "$rl_bot" ] || rl_die "$name was uploaded by '$uploader', not by the release workflow ($rl_bot)"
    gh api -H 'Accept: application/octet-stream' "repos/$rl_repo/releases/assets/$id" >"$dir/$name" ||
      rl_die "download of $name failed"
    if [ "$digest" != - ] && [ "$digest" != "sha256:$(rl_sha256 "$dir/$name")" ]; then
      rl_die "$name does not match the digest GitHub reports ($digest)"
    fi
  done <<EOF
$list
EOF
}

# rl_tag_commit TAG: fetches origin/main and TAG, prints the tagged commit; dies unless it is
# on origin/main.
rl_tag_commit() {
  local c
  git fetch --quiet origin main "refs/tags/$1:refs/tags/$1" || rl_die "cannot fetch origin/main and $1"
  c="$(git rev-parse --verify --quiet "$1^{commit}")" || rl_die "tag $1 not found"
  git merge-base --is-ancestor "$c" origin/main || rl_die "$1 ($c) is not on origin/main"
  printf '%s\n' "$c"
}
