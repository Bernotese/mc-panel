# shellcheck shell=bash
# Tests of binaries.sh (installer spec §4.4, review focus 4). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329

# bin_fixture_release VERSION: tarball, checksums.txt and its signature (by a throwaway key
# that release_signers trusts, install_signature.sh) in $WORK/srv, served by a fake fetch that
# fails for a file that is not there.
bin_fixture_release() {
  local v="$1" d="$WORK/rel-src" b
  mkdir -p "$d/deploy/systemd" "$WORK/srv"
  for b in panel agent files; do
    printf '#!/bin/sh\necho "%s %s"\n' "$b" "$v" >"$d/$b"
    chmod 0755 "$d/$b"
  done
  echo tpl >"$d/deploy/systemd/x.service"
  tar -C "$d" -czf "$WORK/srv/mc-panel_${v}_linux_amd64.tar.gz" panel agent files deploy
  (cd "$WORK/srv" && sha256sum "mc-panel_${v}_linux_amd64.tar.gz" >checksums.txt)
  [[ -f $WORK/key ]] || sig_fixture_key
  mkdir -p "$ROOT$ETC_PANEL" # lay_ensure's; bin_install writes RELEASE_RECORD there
  sig_sign "$WORK/srv/checksums.txt"
  fetch() {
    printf 'fetch %s\n' "$1" >>"$WORK/calls.log"
    cp "$WORK/srv/${1##*/}" "$2" 2>/dev/null || die "download failed: $1"
  }
  TARGET_VERSION="$v" ARCH=amd64 FROM_DIR=''
}

# bin_fixture_build DIR: a local build like --from-dir expects it.
bin_fixture_build() {
  local b
  mkdir -p "$1/deploy" "$ROOT$ETC_PANEL"
  for b in panel agent files; do
    printf '#!/bin/sh\necho "%s 0.0.0-testvm+abc"\n' "$b" >"$1/$b"
    chmod 0755 "$1/$b"
  done
}

test_install_bin_url_uses_release_layout() {
  TARGET_VERSION=1.2.3 ARCH=arm64
  assert_eq https://github.com/Bernotese/mc-panel/releases/download/v1.2.3/mc-panel_1.2.3_linux_arm64.tar.gz \
    "$(bin_url "mc-panel_$(bin_version)_linux_$ARCH.tar.gz")"
  TARGET_VERSION=v1.2.3
  assert_eq 1.2.3 "$(bin_version)"
}

test_install_bin_install_from_release() {
  bin_fixture_release 1.2.3
  local out
  out="$(bin_install)"
  assert_contains "$out" "[changed] panel agent files 1.2.3"
  assert_called "fetch https://github.com/Bernotese/mc-panel/releases/download/v1.2.3/mc-panel_1.2.3_linux_amd64.tar.gz"
  assert_called "fetch https://github.com/Bernotese/mc-panel/releases/download/v1.2.3/checksums.txt"
  assert_file_contains "$ROOT$BIN_DIR/panel" 'panel 1.2.3'
  assert_eq 755 "$(stat -c '%a' "$ROOT$BIN_DIR/agent")"
  assert_eq root:root "$(_owner_get "$BIN_DIR/files")"
}

test_install_bin_install_sets_source_and_changed() {
  bin_fixture_release 1.2.3
  bin_install >/dev/null
  assert_eq "$WORK/release" "$BIN_SOURCE_DIR"
  assert_file "$BIN_SOURCE_DIR/deploy/systemd/x.service"
  assert_eq 1 "$BIN_CHANGED"
}

test_install_bin_install_second_run_unchanged() {
  bin_fixture_release 1.2.3
  bin_install >/dev/null
  BIN_CHANGED=0
  assert_contains "$(bin_install)" "unchanged"
  bin_install >/dev/null
  assert_eq 0 "$BIN_CHANGED"
}

test_install_bin_install_refuses_bad_checksum() {
  bin_fixture_release 1.2.3
  echo "0000000000000000000000000000000000000000000000000000000000000000  mc-panel_1.2.3_linux_amd64.tar.gz" >"$WORK/srv/checksums.txt"
  sig_sign "$WORK/srv/checksums.txt" # signed but wrong: the sha256 check refuses it
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "checksum mismatch"
  assert_no_file "$ROOT$BIN_DIR"
}

test_install_bin_install_refuses_missing_checksum_line() {
  bin_fixture_release 1.2.3
  echo "$(printf '%064d' 0)  install.sh" >"$WORK/srv/checksums.txt"
  sig_sign "$WORK/srv/checksums.txt"
  assert_fails bin_install
  assert_no_file "$ROOT$BIN_DIR"
}

test_install_bin_install_refuses_failed_download() {
  bin_fixture_release 1.2.3
  fetch() { return 22; }
  assert_fails bin_install
  assert_no_file "$ROOT$BIN_DIR"
}

test_install_bin_install_refuses_dev_version_without_from_dir() {
  TARGET_VERSION='' FROM_DIR='' MCPANEL_VERSION=dev ARCH=amd64
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "no release version"
}

test_install_bin_tar_safe_rejects_traversal() {
  local d="$WORK/evil"
  mkdir -p "$d/a"
  echo x >"$d/a/f"
  tar -C "$d" -czf "$WORK/evil.tar.gz" --transform 's,^a/f,../../etc/passwd,' a/f
  assert_fails bin_tar_safe "$WORK/evil.tar.gz"
  tar -C "$d" -czf "$WORK/abs.tar.gz" --transform 's,^a/f,/etc/passwd,' -P a/f
  assert_fails bin_tar_safe "$WORK/abs.tar.gz"
}

test_install_bin_tar_safe_rejects_links() {
  local d="$WORK/links"
  mkdir -p "$d"
  ln -s /etc "$d/deploy"
  tar -C "$d" -czf "$WORK/links.tar.gz" deploy
  assert_fails bin_tar_safe "$WORK/links.tar.gz"
}

test_install_bin_tar_safe_accepts_release() {
  bin_fixture_release 1.2.3
  bin_tar_safe "$WORK/srv/mc-panel_1.2.3_linux_amd64.tar.gz"
}

test_install_bin_install_refuses_unsafe_tarball() {
  bin_fixture_release 1.2.3
  local d="$WORK/evil"
  mkdir -p "$d"
  ln -s /etc "$d/deploy"
  tar -C "$d" -czf "$WORK/srv/mc-panel_1.2.3_linux_amd64.tar.gz" deploy
  (cd "$WORK/srv" && sha256sum "mc-panel_1.2.3_linux_amd64.tar.gz" >checksums.txt)
  sig_sign "$WORK/srv/checksums.txt"
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "unsafe entries"
  assert_no_file "$ROOT$BIN_DIR"
}

test_install_bin_install_from_dir() {
  local d="$WORK/build"
  bin_fixture_build "$d"
  FROM_DIR="$d" TARGET_VERSION=0.0.0-testvm+abc
  fetch() { echo "no download with --from-dir" >&2; return 1; }
  bin_install >/dev/null
  assert_file_contains "$ROOT$BIN_DIR/files" 'files 0.0.0-testvm+abc'
  assert_eq "$d" "$BIN_SOURCE_DIR"
}

test_install_bin_install_from_dir_requires_all_parts() {
  local d="$WORK/build"
  bin_fixture_build "$d"
  rm -- "$d/agent"
  FROM_DIR="$d"
  assert_fails bin_install
  assert_no_file "$ROOT$BIN_DIR/panel"
  bin_fixture_build "$d"
  rmdir -- "$d/deploy"
  assert_fails bin_install
  assert_no_file "$ROOT$BIN_DIR/panel"
}
