# shellcheck shell=bash
# Tests of scripts/verify-release-build.sh (SEC-07): the comparison of the draft's tarballs
# with a rebuild (taken from VERIFY_RELEASE_PREBUILT, so no real build runs) and the Node
# version check, in a throwaway git repo with an origin and a tag.
# Run: bash scripts/install/test.sh tests/verify_release_build.sh
# shellcheck disable=SC2034,SC2317,SC2329

# vb_fixture: repo $WORK/repo (deploy/ of this checkout, .goreleaser.yaml, go.mod, a
# .node-version of 24.21.0), tag v1.2.3 on origin's main, a "rebuild" in $WORK/pre/<arch>/ and
# matching draft tarballs in $WORK/assets; fake node (v24.21.0) and go in $WORK/bin.
vb_fixture() {
  local src repo="$WORK/repo" g arch b
  src="$(cd "$(sig_src_dir)/../.." && pwd)"
  g=(git -C "$repo" -c user.name=t -c user.email=t@example.invalid -c commit.gpgsign=false -c tag.gpgsign=false)
  mkdir -p "$repo" "$WORK/assets" "$WORK/bin"
  cp -R "$src/deploy" "$repo/deploy"
  cp "$src/.goreleaser.yaml" "$repo/.goreleaser.yaml"
  printf 'module example\n\ngo 1.27.2\n' >"$repo/go.mod"
  echo 24.21.0 >"$repo/.node-version"
  git init -q -b main "$repo"
  "${g[@]}" add -A
  "${g[@]}" commit -qm release
  "${g[@]}" tag v1.2.3
  git init -q --bare "$WORK/origin.git"
  "${g[@]}" remote add origin "$WORK/origin.git"
  "${g[@]}" push -q origin main v1.2.3
  for arch in amd64 arm64; do
    mkdir -p "$WORK/pre/$arch" "$WORK/rel-$arch"
    for b in panel agent files; do
      printf '%s %s binary\n' "$b" "$arch" >"$WORK/pre/$arch/$b"
      cp "$WORK/pre/$arch/$b" "$WORK/rel-$arch/$b"
    done
    cp -R "$repo/deploy" "$WORK/rel-$arch/deploy"
    vb_pack "$arch"
  done
  printf '#!/bin/sh\necho "%s"\n' "${VB_NODE:-v24.21.0}" >"$WORK/bin/node"
  printf '#!/bin/sh\necho "fake go" >&2\nexit 1\n' >"$WORK/bin/go"
  chmod +x "$WORK/bin/node" "$WORK/bin/go"
}

# vb_pack ARCH: the draft tarball of ARCH from $WORK/rel-ARCH (as goreleaser packs it).
vb_pack() {
  tar -C "$WORK/rel-$1" -czf "$WORK/assets/mc-panel_1.2.3_linux_$1.tar.gz" .
}

# vb_run: runs verify-release-build.sh --assets in the repo; exit code in _rc, output in
# $WORK/out. PATH has $WORK/bin first and no fnm unless a test puts one there.
vb_run() {
  local script
  script="$(cd "$(sig_src_dir)/.." && pwd)/verify-release-build.sh"
  _rc=0
  (cd "$WORK/repo" && PATH="$WORK/bin:/usr/bin:/bin" VERIFY_RELEASE_PREBUILT="$WORK/pre" \
    bash "$script" --assets "$WORK/assets" v1.2.3) >"$WORK/out" 2>&1 || _rc=$?
}

vb_refused() {
  [[ $_rc != 0 ]] || fail "verify-release-build succeeded" "$(cat "$WORK/out")"
  assert_contains "$(cat "$WORK/out")" "$1"
}

test_verify_build_identical_release_passes() {
  vb_fixture
  vb_run
  [[ $_rc == 0 ]] || fail "exit $_rc" "$(cat "$WORK/out")"
  assert_contains "$(cat "$WORK/out")" "OK: the tarballs of v1.2.3 are exactly what"
  assert_contains "$(cat "$WORK/out")" "panel: identical"
}

test_verify_build_refuses_a_different_binary() {
  vb_fixture
  echo "evil agent" >"$WORK/rel-arm64/agent"
  vb_pack arm64
  vb_run
  vb_refused "agent: DIFFERS from the rebuild"
  assert_contains "$(cat "$WORK/out")" "do not sign"
}

test_verify_build_refuses_an_extra_entry() {
  vb_fixture
  echo x >"$WORK/rel-amd64/backdoor"
  vb_pack amd64
  vb_run
  vb_refused "entries differ"
  assert_contains "$(cat "$WORK/out")" "< backdoor"
}

test_verify_build_refuses_a_missing_entry() {
  vb_fixture
  rm -f -- "$WORK/rel-amd64/files"
  vb_pack amd64
  vb_run
  vb_refused "entries differ"
}

test_verify_build_refuses_a_symlink() {
  vb_fixture
  ln -s /etc/passwd "$WORK/rel-amd64/deploy/link"
  vb_pack amd64
  vb_run
  vb_refused "neither files nor directories"
}

test_verify_build_refuses_a_changed_deploy_file() {
  vb_fixture
  echo "# changed" >>"$WORK/rel-arm64/deploy/systemd/mcpanel-agent.service"
  vb_pack arm64
  vb_run
  vb_refused "deploy/systemd/mcpanel-agent.service: DIFFERS from v1.2.3"
}

test_verify_build_refuses_another_node_version() {
  VB_NODE=v26.1.0
  vb_fixture
  vb_run
  vb_refused "built with Node 24.21.0, but this machine runs v26.1.0; get it with: fnm install 24.21.0"
}

test_verify_build_uses_fnm_for_the_pinned_node() {
  VB_NODE=v26.1.0 # the shell's default node; fnm picks the pinned one
  vb_fixture
  cat >"$WORK/bin/fnm" <<'FNM'
#!/bin/sh
echo "fnm $*" >>"$(dirname "$0")/../fnm.calls"
[ "$1 $2 $3" = "exec --using=24.21.0 node" ] && echo v24.21.0
FNM
  chmod +x "$WORK/bin/fnm"
  vb_run
  [[ $_rc == 0 ]] || fail "exit $_rc" "$(cat "$WORK/out")"
  assert_file_contains "$WORK/fnm.calls" "fnm exec --using=24.21.0 node --version"
}

test_verify_build_fnm_without_the_version() {
  vb_fixture
  printf '#!/bin/sh\nexit 1\n' >"$WORK/bin/fnm"
  chmod +x "$WORK/bin/fnm"
  vb_run
  vb_refused "fnm has no Node 24.21.0; get it with: fnm install 24.21.0"
}

test_verify_build_refuses_changed_goreleaser_flags() {
  vb_fixture
  sed -i 's/-trimpath/-race/' "$WORK/repo/.goreleaser.yaml"
  git -C "$WORK/repo" -c user.name=t -c user.email=t@example.invalid commit -qam flags
  git -C "$WORK/repo" tag -f v1.2.3 >/dev/null
  git -C "$WORK/repo" push -q -f origin main v1.2.3
  vb_run
  vb_refused "no longer has '-trimpath'"
}
