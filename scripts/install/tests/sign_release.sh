# shellcheck shell=bash
# Tests of scripts/sign-release.sh (SEC-07) against a fake gh (a draft release served from
# $WORK/gh) and a throwaway git repo with an origin, a tagged commit and a release key.
# Run: bash scripts/install/test.sh tests/sign_release.sh
# shellcheck disable=SC2034,SC2317,SC2329

# sr_fixture: repo $WORK/repo (scripts/install and deploy/ of this checkout plus a
# release-signers with the key $WORK/sr-key), tag v1.2.3 on origin's main, and a draft release
# 77 of it in $WORK/gh, made like the release workflow makes it. SR_* tune the fake gh.
sr_fixture() {
  local src repo="$WORK/repo" gh="$WORK/gh" g
  src="$(cd "$(sig_src_dir)/../.." && pwd)"
  g=(git -C "$repo" -c user.name=t -c user.email=t@example.invalid -c commit.gpgsign=false -c tag.gpgsign=false)
  mkdir -p "$repo/scripts" "$gh/assets"
  cp -R "$src/scripts/install" "$repo/scripts/install"
  cp -R "$src/deploy" "$repo/deploy"
  ssh-keygen -q -t ed25519 -N '' -C owner -f "$WORK/sr-key" </dev/null
  printf 'mc-panel-release namespaces="mc-panel-release" %s owner\n' "$(cut -d' ' -f1,2 "$WORK/sr-key.pub")" \
    >"$repo/scripts/install/release-signers"
  git init -q -b main "$repo"
  "${g[@]}" add -A
  "${g[@]}" commit -qm release
  "${g[@]}" tag v1.2.3
  git init -q --bare "$WORK/origin.git"
  "${g[@]}" remote add origin "$WORK/origin.git"
  "${g[@]}" push -q origin main v1.2.3
  # the draft
  bash "$repo/scripts/install/assemble.sh" 1.2.3 "$gh/assets/install.sh" >/dev/null
  echo amd64 >"$gh/assets/mc-panel_1.2.3_linux_amd64.tar.gz"
  echo arm64 >"$gh/assets/mc-panel_1.2.3_linux_arm64.tar.gz"
  (cd "$gh/assets" && sha256sum install.sh mc-panel_1.2.3_linux_amd64.tar.gz mc-panel_1.2.3_linux_arm64.tar.gz >checksums.txt)
  echo 77 >"$gh/ids"
  echo "true false github-actions[bot] v1.2.3" >"$gh/info"
  echo true >"$gh/draft"
  : >"$gh/calls"
  mkdir -p "$WORK/bin"
  cat >"$WORK/bin/gh" <<'GH'
#!/usr/bin/env bash
# fake gh for sign_release.sh: answers exactly the calls sign-release.sh makes
set -euo pipefail
gh="$SR_GH"
printf '%s\n' "$*" >>"$gh/calls"
last="${!#}" # the last argument (an asset URL or the file to upload)
assets() {
  local f n up
  for f in "$gh/assets"/*; do
    n="${f##*/}"
    up='github-actions[bot]'
    [[ ! -f $gh/uploader.$n ]] || up="$(cat "$gh/uploader.$n")"
    printf '%s %s %s sha256:%s\n' "$n" "$n" "$up" "$(sha256sum "$f" | awk '{ print $1 }')"
  done
}
case "$*" in
"api --paginate repos/$SR_REPO/releases?per_page=100 --jq "*) cat "$gh/ids" ;;
"api repos/$SR_REPO/releases/77 --jq .draft") cat "$gh/draft" ;;
"api repos/$SR_REPO/releases/77 --jq "*) cat "$gh/info" ;;
"api repos/$SR_REPO/releases/77/assets?per_page=100 --jq "*) assets ;;
"api -H Accept: application/octet-stream repos/$SR_REPO/releases/assets/"*) cat "$gh/assets/${last##*/}" ;;
"api user --jq .login") echo owner ;;
"release upload v1.2.3 -R $SR_REPO "*)
  cp "$last" "$gh/assets/"
  echo owner >"$gh/uploader.${last##*/}"
  # SR_SWAP_AFTER_UPLOAD=NAME: someone replaces asset NAME right after the upload
  if [[ -n ${SR_SWAP_AFTER_UPLOAD:-} ]]; then echo swapped >"$gh/assets/$SR_SWAP_AFTER_UPLOAD"; fi
  ;;
"api -X PATCH repos/$SR_REPO/releases/77 -F draft=false -f make_latest="*) echo false >"$gh/draft" ;;
*)
  echo "fake gh: unexpected call: $*" >&2
  exit 1
  ;;
esac
GH
  chmod +x "$WORK/bin/gh"
  # fake verify-release-build.sh: records its call, fails when $WORK/build-differs exists
  cat >"$WORK/bin/verify" <<'VF'
#!/usr/bin/env bash
printf 'verify %s\n' "$*" >>"$SR_GH/calls"
[[ ! -f $SR_GH/../build-differs ]]
VF
  chmod +x "$WORK/bin/verify"
}

# sr_run [ANSWER [OPTION]]: runs sign-release.sh [OPTION] v1.2.3 in the repo with ANSWER
# (default y) on stdin; exit code in _rc, output in $WORK/out.
sr_run() {
  local script opts=()
  script="$(cd "$(sig_src_dir)/.." && pwd)/sign-release.sh"
  if [[ -n ${2:-} ]]; then opts=("$2"); fi
  _rc=0
  (cd "$WORK/repo" && printf '%s\n' "${1:-y}" |
    PATH="$WORK/bin:$PATH" SR_GH="$WORK/gh" SR_REPO=example/mc-panel SIGN_RELEASE_REPO=example/mc-panel \
      SIGN_RELEASE_VERIFY="$WORK/bin/verify" bash "$script" "${opts[@]}" v1.2.3 "$WORK/sr-key") >"$WORK/out" 2>&1 || _rc=$?
}

assert_gh_called() {
  grep -qF -- "$1" "$WORK/gh/calls" || fail "gh not called: $1" "$(cat "$WORK/gh/calls")"
}

# sr_unpublished: the release was not published.
sr_unpublished() {
  if grep -qF -- "PATCH" "$WORK/gh/calls"; then fail "the release was published" "$(cat "$WORK/out")"; fi
  assert_eq true "$(cat "$WORK/gh/draft")"
}

# sr_refused MSG: sign-release failed with MSG before uploading anything.
sr_refused() {
  [[ $_rc != 0 ]] || fail "sign-release succeeded" "$(cat "$WORK/out")"
  assert_contains "$(cat "$WORK/out")" "$1"
  sr_unpublished
  [[ ! -f $WORK/gh/assets/checksums.txt.sig ]] || fail "a signature was uploaded"
}

test_sign_release_signs_and_publishes() {
  sr_fixture
  sr_run
  [[ $_rc == 0 ]] || fail "exit $_rc" "$(cat "$WORK/out")"
  assert_gh_called "api -X PATCH repos/example/mc-panel/releases/77 -F draft=false -f make_latest=true"
  ssh-keygen -Y verify -f "$WORK/repo/scripts/install/release-signers" -I mc-panel-release -n mc-panel-release \
    -s "$WORK/gh/assets/checksums.txt.sig" <"$WORK/gh/assets/checksums.txt" >/dev/null 2>&1 ||
    fail "the uploaded signature does not verify"
  assert_contains "$(cat "$WORK/out")" "published v1.2.3 (release 77)"
  assert_gh_called "verify --assets "
  assert_contains "$(cat "$WORK/out")" "tarballs identical to the rebuild"
}

test_sign_release_refuses_when_the_build_differs() {
  sr_fixture
  touch "$WORK/build-differs"
  sr_run
  sr_refused "the build check failed; do not sign"
}

test_sign_release_skip_build_check_warns_loudly() {
  sr_fixture
  touch "$WORK/build-differs"
  sr_run y --skip-build-check
  [[ $_rc == 0 ]] || fail "exit $_rc" "$(cat "$WORK/out")"
  if grep -q '^verify' "$WORK/gh/calls"; then fail "the build check ran"; fi
  assert_contains "$(cat "$WORK/out")" "WARNING: --skip-build-check"
  assert_contains "$(cat "$WORK/out")" "NOT CHECKED (--skip-build-check)"
}

test_sign_release_answer_no_signs_nothing() {
  sr_fixture
  sr_run n
  sr_refused "not signed; the draft stays unpublished"
}

test_sign_release_refuses_two_releases_with_the_tag() {
  sr_fixture
  printf '77\n78\n' >"$WORK/gh/ids"
  sr_run
  sr_refused "expected exactly one release with tag v1.2.3, found 2"
}

test_sign_release_refuses_a_release_not_made_by_the_workflow() {
  sr_fixture
  echo "true false github-actions[bot]x v1.2.3" >"$WORK/gh/info"
  sr_run
  sr_refused "not by the release workflow"
}

test_sign_release_refuses_a_published_release() {
  sr_fixture
  echo "false false github-actions[bot] v1.2.3" >"$WORK/gh/info"
  sr_run
  sr_refused "already published"
}

test_sign_release_refuses_an_asset_uploaded_by_someone_else() {
  sr_fixture
  echo mallory >"$WORK/gh/uploader.mc-panel_1.2.3_linux_amd64.tar.gz"
  sr_run
  sr_refused "mc-panel_1.2.3_linux_amd64.tar.gz was uploaded by 'mallory'"
}

test_sign_release_refuses_an_extra_asset() {
  sr_fixture
  echo x >"$WORK/gh/assets/checksums.txt.sig"
  sr_run
  [[ $_rc != 0 ]] || fail "accepted an extra asset"
  assert_contains "$(cat "$WORK/out")" "are not what the release workflow makes"
  sr_unpublished
}

test_sign_release_refuses_a_checksum_mismatch() {
  sr_fixture
  echo changed >"$WORK/gh/assets/mc-panel_1.2.3_linux_arm64.tar.gz"
  sr_run
  sr_refused "mc-panel_1.2.3_linux_arm64.tar.gz does not match checksums.txt"
}

test_sign_release_refuses_an_install_sh_not_built_from_the_tag() {
  # an attacker with the GitHub account swaps install.sh and fixes checksums.txt to match
  sr_fixture
  printf '\necho evil\n' >>"$WORK/gh/assets/install.sh"
  (cd "$WORK/gh/assets" && sha256sum install.sh mc-panel_1.2.3_linux_amd64.tar.gz mc-panel_1.2.3_linux_arm64.tar.gz >checksums.txt)
  sr_run
  sr_refused "install.sh of the draft differs from install.sh assembled from"
}

test_sign_release_refuses_a_test_key_at_the_tag() {
  sr_fixture
  echo "# TEST-ONLY" >>"$WORK/repo/scripts/install/release-signers"
  git -C "$WORK/repo" -c user.name=t -c user.email=t@example.invalid commit -qam test
  git -C "$WORK/repo" tag -f v1.2.3 >/dev/null
  git -C "$WORK/repo" push -q -f origin main v1.2.3
  bash "$WORK/repo/scripts/install/assemble.sh" 1.2.3 "$WORK/gh/assets/install.sh" >/dev/null
  (cd "$WORK/gh/assets" && sha256sum install.sh mc-panel_1.2.3_linux_amd64.tar.gz mc-panel_1.2.3_linux_arm64.tar.gz >checksums.txt)
  sr_run
  sr_refused "still holds a TEST-ONLY key"
}

test_sign_release_refuses_an_asset_swapped_after_the_upload() {
  sr_fixture
  # between the check and the publish, someone replaces a tarball in the draft
  export SR_SWAP_AFTER_UPLOAD=mc-panel_1.2.3_linux_amd64.tar.gz
  sr_run
  [[ $_rc != 0 ]] || fail "published a changed release" "$(cat "$WORK/out")"
  assert_contains "$(cat "$WORK/out")" "changed since it was checked"
  sr_unpublished
}
