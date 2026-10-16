# shellcheck shell=bash
# Tests of the release signatures (lib.sh sig_verify/release_verify, binaries.sh, update.sh
# upd_handover, SEC-07) with throwaway keys generated per test. Run:
# bash scripts/install/test.sh tests/install_signature.sh
# shellcheck disable=SC2034,SC2317,SC2329

# sig_fixture_key [NAME]: a throwaway ed25519 key $WORK/NAME (default: key) that release_signers
# trusts from now on (instead of scripts/install/release-signers).
sig_fixture_key() {
  local k="$WORK/${1:-key}"
  ssh-keygen -q -t ed25519 -N '' -C test -f "$k" </dev/null
  SIG_TRUSTED="$SIGN_PRINCIPAL namespaces=\"$SIGN_NAMESPACE\" $(cut -d' ' -f1,2 "$k.pub")"
  release_signers() { printf '%s\n' "$SIG_TRUSTED"; }
}

# sig_sign FILE [KEY] [NAMESPACE]: writes FILE.sig, signed with KEY (default $WORK/key) in
# NAMESPACE (default the release namespace).
sig_sign() {
  rm -f -- "$1.sig"
  ssh-keygen -q -Y sign -f "${2:-$WORK/key}" -n "${3:-$SIGN_NAMESPACE}" "$1" </dev/null 2>/dev/null
}

# sig_reset_release: removes the fixture release of an earlier bin_fixture_release.
sig_reset_release() {
  rm -rf -- "${WORK:?}/srv" "${WORK:?}/rel-src"
}

# sig_src_dir: scripts/install of this checkout.
sig_src_dir() {
  (cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
}

# sig_pin_unsigned VERSION: the fixture's checksums.txt becomes the pinned one of the unsigned
# release VERSION (instead of the real 0.1.0-0.4.0 in lib.sh).
sig_pin_unsigned() {
  UNSIGNED_RELEASES=("$1 $(sha256sum "$WORK/srv/checksums.txt" | awk '{ print $1 }')")
}

sig_fingerprint() {
  ssh-keygen -l -f "$1" | awk '{ print $2 }'
}

test_install_sig_good_signature_installs_and_records() {
  bin_fixture_release 1.2.3
  bin_install >/dev/null
  assert_called "fetch https://github.com/Bernotese/mc-panel/releases/download/v1.2.3/checksums.txt.sig"
  assert_file_contains "$ROOT$RELEASE_RECORD" "version=1.2.3"
  assert_file_contains "$ROOT$RELEASE_RECORD" "verified=signed"
  assert_file_contains "$ROOT$RELEASE_RECORD" "key=$(sig_fingerprint "$WORK/key.pub")"
  assert_eq 644 "$(stat -c '%a' "$ROOT$RELEASE_RECORD")"
}

test_install_sig_refuses_tampered_checksums() {
  bin_fixture_release 1.2.3
  # an attacker who controls the release replaces checksums.txt (and the tarball) but cannot sign
  printf '%064d  mc-panel_1.2.3_linux_amd64.tar.gz\n' 1 >"$WORK/srv/checksums.txt"
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "does not verify"
  assert_no_file "$ROOT$BIN_DIR"
  assert_no_file "$ROOT$RELEASE_RECORD"
}

test_install_sig_refuses_missing_signature() {
  bin_fixture_release 1.2.3
  rm -f -- "$WORK/srv/checksums.txt.sig"
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "cannot download the signature of release v1.2.3"
  assert_not_called "fetch https://github.com/Bernotese/mc-panel/releases/download/v1.2.3/mc-panel_1.2.3_linux_amd64.tar.gz"
  assert_no_file "$ROOT$BIN_DIR"
}

test_install_sig_refuses_empty_or_garbage_signature() {
  bin_fixture_release 1.2.3
  : >"$WORK/srv/checksums.txt.sig"
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "is empty"
  echo garbage >"$WORK/srv/checksums.txt.sig"
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "does not verify"
  assert_no_file "$ROOT$BIN_DIR"
}

test_install_sig_refuses_unknown_key() {
  bin_fixture_release 1.2.3
  ssh-keygen -q -t ed25519 -N '' -C other -f "$WORK/other" </dev/null
  sig_sign "$WORK/srv/checksums.txt" "$WORK/other"
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "does not verify"
  assert_no_file "$ROOT$BIN_DIR"
}

test_install_sig_refuses_wrong_namespace() {
  bin_fixture_release 1.2.3
  # a signature the owner made with the same key for something else (e.g. a git commit)
  sig_sign "$WORK/srv/checksums.txt" "$WORK/key" git
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "does not verify"
  assert_no_file "$ROOT$BIN_DIR"
}

test_install_sig_refuses_signature_of_another_release() {
  # the signed checksums.txt of 1.2.3, replayed under the tag v1.2.4
  bin_fixture_release 1.2.3
  cp "$WORK/srv/checksums.txt" "$WORK/sums-123"
  cp "$WORK/srv/checksums.txt.sig" "$WORK/sums-123.sig"
  sig_reset_release
  bin_fixture_release 1.2.4
  cp "$WORK/sums-123" "$WORK/srv/checksums.txt"
  cp "$WORK/sums-123.sig" "$WORK/srv/checksums.txt.sig"
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "does not belong to release v1.2.4"
  assert_no_file "$ROOT$BIN_DIR"
}

test_install_sig_any_listed_key_verifies() {
  # rotation: release_signers lists the old and the new key
  bin_fixture_release 1.2.3
  local old="$SIG_TRUSTED"
  sig_fixture_key new
  SIG_TRUSTED="$old"$'\n'"$SIG_TRUSTED"
  sig_sign "$WORK/srv/checksums.txt" "$WORK/new"
  bin_install >/dev/null
  assert_file_contains "$ROOT$RELEASE_RECORD" "key=$(sig_fingerprint "$WORK/new.pub")"
}

test_install_sig_honours_valid_before() {
  # a retired key (valid-before in the past) no longer verifies
  bin_fixture_release 1.2.3
  SIG_TRUSTED="${SIG_TRUSTED/namespaces=/valid-before=\"20200101\",namespaces=}"
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "does not verify"
}

test_install_sig_refuses_without_trusted_keys() {
  bin_fixture_release 1.2.3
  SIG_TRUSTED="# no keys"
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "has no release keys"
}

test_install_sig_old_release_needs_allow_unsigned() {
  bin_fixture_release 0.4.0
  rm -f -- "$WORK/srv/checksums.txt.sig"
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "older than $SIGNED_SINCE and has no signature"
  assert_contains "$(cat "$WORK/out")" "--allow-unsigned"
  assert_no_file "$ROOT$BIN_DIR"
  assert_not_called "checksums.txt.sig"
}

test_install_sig_old_release_with_allow_unsigned_warns() {
  bin_fixture_release 0.4.0
  rm -f -- "$WORK/srv/checksums.txt.sig"
  sig_pin_unsigned 0.4.0
  ALLOW_UNSIGNED=1
  bin_install >"$WORK/install.out" 2>&1
  assert_file_contains "$WORK/install.out" "release v0.4.0 is NOT signed"
  assert_file_contains "$ROOT$RELEASE_RECORD" "verified=unsigned"
  assert_file_contains "$ROOT$BIN_DIR/panel" 'panel 0.4.0'
  assert_not_called "checksums.txt.sig"
}

test_install_sig_old_release_still_checks_checksums() {
  bin_fixture_release 0.4.0
  printf '%064d  mc-panel_0.4.0_linux_amd64.tar.gz\n' 0 >"$WORK/srv/checksums.txt"
  sig_pin_unsigned 0.4.0
  ALLOW_UNSIGNED=1
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "checksum mismatch"
}

test_install_sig_old_release_needs_the_published_checksums() {
  # an attacker replaces checksums.txt (and the tarball) of an old, unsigned release
  bin_fixture_release 0.4.0
  sig_pin_unsigned 0.4.0
  printf '%064d  install.sh\n' 7 >>"$WORK/srv/checksums.txt"
  ALLOW_UNSIGNED=1
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "checksums.txt of release v0.4.0 is not the one published"
  assert_no_file "$ROOT$BIN_DIR"
}

test_install_sig_unknown_old_release_is_refused() {
  # only the closed set of unsigned releases; nothing else before SIGNED_SINCE
  bin_fixture_release 0.4.1
  rm -f -- "$WORK/srv/checksums.txt.sig"
  ALLOW_UNSIGNED=1
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "not one of the known unsigned releases (0.1.0 0.2.0 0.3.0 0.4.0)"
  assert_no_file "$ROOT$BIN_DIR"
}

test_install_sig_pinned_unsigned_releases() {
  # the closed set, fixed when signing began (SHA-256 of each release's checksums.txt)
  assert_eq 4 "${#UNSIGNED_RELEASES[@]}"
  assert_eq 5f84b72969fc7d288bd4fb0317daf8a8fadd6201e7e20d8f9e62ab69017b1b56 "$(unsigned_pin 0.4.0)"
  assert_eq 94047685dbefa6747463848508d9ab5c67553225f0d5967b0b69fccf1108f5c5 "$(unsigned_pin 0.1.0)"
  assert_exit 1 unsigned_pin 0.5.0
}

test_install_sig_allow_unsigned_does_not_cover_signed_releases() {
  local v
  ALLOW_UNSIGNED=1
  for v in "$SIGNED_SINCE" 0.5.0-0 0.5.1-rc.1 1.0.0; do
    sig_reset_release
    bin_fixture_release "$v"
    rm -f -- "$WORK/srv/checksums.txt.sig"
    assert_fails bin_install
    assert_contains "$(cat "$WORK/out")" "cannot download the signature of release v$v"
  done
  # the pre-releases of the first signed version must be signed, too
  sig_reset_release
  bin_fixture_release 0.5.0-rc.1
  rm -f -- "$WORK/srv/checksums.txt.sig"
  assert_fails bin_install
  assert_contains "$(cat "$WORK/out")" "cannot download the signature of release v0.5.0-rc.1"
  sig_sign "$WORK/srv/checksums.txt"
  bin_install >/dev/null
  assert_file_contains "$ROOT$RELEASE_RECORD" "verified=signed"
}

test_install_sig_from_dir_records_local_build() {
  bin_fixture_build "$WORK/build"
  mkdir -p "$ROOT$ETC_PANEL"
  FROM_DIR="$WORK/build" TARGET_VERSION=0.0.0-testvm+abc
  bin_install >/dev/null
  assert_file_contains "$ROOT$RELEASE_RECORD" "verified=from-dir"
  assert_not_called "fetch"
}

test_install_sig_update_keeps_the_download_result() {
  # update verifies the download (upd_prepare_source) and installs from it as FROM_DIR
  bin_fixture_release 1.2.3
  local src
  src="$(bin_source)"
  FROM_DIR="$src"
  bin_install >/dev/null
  assert_file_contains "$ROOT$RELEASE_RECORD" "verified=signed"
}

test_install_sig_parse_args_allow_unsigned() {
  parse_args install --allow-unsigned --domain x.example.com
  assert_eq 1 "$ALLOW_UNSIGNED"
  reset_globals
  parse_args update --allow-unsigned
  assert_eq 1 "$ALLOW_UNSIGNED"
  assert_exit 1 parse_args doctor --allow-unsigned
  assert_contains "$(usage)" "v$SIGNED_SINCE"
}

# --- update hand-over ------------------------------------------------------------------------

# sig_handover_fixture VERSION: install.sh, checksums.txt and its signature of VERSION served by
# a fake fetch; 1.0.0 installed.
sig_handover_fixture() {
  local v="$1"
  ops_fake_install 1.0.0
  ops_probes
  [[ -f $WORK/key ]] || sig_fixture_key
  mkdir -p "$WORK/srv"
  printf '#!/bin/bash\necho child "$@"\n' >"$WORK/srv/install.sh"
  (cd "$WORK/srv" && sha256sum install.sh >checksums.txt &&
    printf '%064d  mc-panel_%s_linux_amd64.tar.gz\n' 0 "$v" >>checksums.txt)
  sig_sign "$WORK/srv/checksums.txt"
  fetch() {
    printf 'fetch %s\n' "$1" >>"$WORK/calls.log"
    cp "$WORK/srv/${1##*/}" "$2" 2>/dev/null || die "download failed: $1"
  }
  upd_exec() { echo "exec $*" >>"$WORK/calls.log"; }
  MCPANEL_VERSION=1.0.0 TARGET_VERSION="$v" ARCH=amd64 YES=1
}

test_ops_update_handover_verifies_signature() {
  sig_handover_fixture 1.1.0
  assert_exit 0 cmd_update
  assert_called "fetch https://github.com/Bernotese/mc-panel/releases/download/v1.1.0/checksums.txt.sig"
  assert_called "exec bash $WORK/install.sh update --version v1.1.0 --yes"
  assert_contains "$(cat "$WORK/out")" "release v1.1.0: signed"
}

test_ops_update_handover_refuses_bad_signature() {
  sig_handover_fixture 1.1.0
  printf '#!/bin/bash\necho evil\n' >"$WORK/srv/install.sh"
  (cd "$WORK/srv" && sha256sum install.sh >checksums.txt &&
    printf '%064d  mc-panel_1.1.0_linux_amd64.tar.gz\n' 0 >>checksums.txt)
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "does not verify"
  assert_not_called "exec"
  assert_not_called "fetch https://github.com/Bernotese/mc-panel/releases/download/v1.1.0/install.sh"
  assert_no_file "$ROOT$ROLLBACK_DIR"
}

test_ops_update_handover_refuses_missing_signature() {
  sig_handover_fixture 1.1.0
  rm -f -- "$WORK/srv/checksums.txt.sig"
  ALLOW_UNSIGNED=1
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "cannot download the signature of release v1.1.0"
  assert_not_called "exec"
}

test_ops_update_handover_refuses_replayed_signature() {
  # the signed checksums.txt of 1.1.0 (its install.sh included) served under v1.2.0
  sig_handover_fixture 1.1.0
  TARGET_VERSION=1.2.0
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "does not belong to release v1.2.0"
  assert_not_called "exec"
}

test_ops_update_handover_to_unsigned_release() {
  sig_handover_fixture 0.4.0
  rm -f -- "$WORK/srv/checksums.txt.sig"
  sig_pin_unsigned 0.4.0
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "--allow-unsigned"
  assert_not_called "exec"
  ALLOW_UNSIGNED=1
  assert_exit 0 cmd_update
  assert_contains "$(cat "$WORK/out")" "release v0.4.0 is NOT signed"
  # 0.4.0 does not know --allow-unsigned: it is not passed on
  assert_called "exec bash $WORK/install.sh update --version v0.4.0 --yes"
  assert_not_called "--allow-unsigned"
}

test_ops_update_handover_to_tampered_unsigned_release() {
  sig_handover_fixture 0.4.0
  rm -f -- "$WORK/srv/checksums.txt.sig"
  UNSIGNED_RELEASES=("0.4.0 $(printf '%064d' 0)")
  ALLOW_UNSIGNED=1
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "is not the one published"
  assert_not_called "exec"
}

test_ops_update_rollback_restores_release_record() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  printf 'version=1.0.0\nverified=signed\nkey=SHA256:old\n' >"$ROOT$RELEASE_RECORD"
  chmod 0644 "$ROOT$RELEASE_RECORD"
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  # the new version writes its record, is unhealthy and is rolled back
  unit_ensure() { printf 'version=1.1.0\nverified=from-dir\nkey=\n' >"$ROOT$RELEASE_RECORD"; }
  upd_probe_health() { [[ $("$ROOT$BIN_DIR/panel" version) == 1.0.0 ]]; }
  UPD_HEALTH_SECS=0
  assert_exit 3 cmd_update
  assert_file_contains "$ROOT$RELEASE_RECORD" "key=SHA256:old"
  assert_eq 644 "$(stat -c '%a' "$ROOT$RELEASE_RECORD")"
}

# --- the production key ----------------------------------------------------------------------

# sig_check_signers FILE: 0 when FILE is fit to ship as release-signers, otherwise 1 with the
# reason on stderr: at least one key; every key line is principal SIGN_PRINCIPAL, options
# exactly namespaces="SIGN_NAMESPACE" plus at most valid-after/valid-before (no cert-authority
# or other options), an ed25519 key (plain or FIDO) that ssh-keygen accepts; no TEST-ONLY key.
sig_check_signers() {
  local f="$1" n=0 line principal opts type key opt ns
  if grep -q 'TEST-ONLY' "$f"; then
    echo "$f holds a TEST-ONLY key; put the owner's release key there (docs/dev/README.md, Release signing)" >&2
    return 1
  fi
  while IFS= read -r line; do
    [[ -n $line && $line != \#* ]] || continue
    read -r principal opts type key _ <<<"$line"
    if [[ $principal != "$SIGN_PRINCIPAL" ]]; then
      echo "unexpected principal: $line" >&2
      return 1
    fi
    ns=0
    while IFS= read -r opt; do
      case "$opt" in
      "namespaces=\"$SIGN_NAMESPACE\"") ns=$((ns + 1)) ;;
      valid-after=\"[0-9]*\" | valid-before=\"[0-9]*\")
        [[ $opt =~ ^valid-(after|before)=\"[0-9]{8}([0-9]{4}([0-9]{2})?)?Z?\"$ ]] || {
          echo "malformed option $opt: $line" >&2
          return 1
        }
        ;;
      *)
        echo "option not allowed ($opt): $line" >&2
        return 1
        ;;
      esac
    done < <(tr ',' '\n' <<<"$opts")
    if [[ $ns != 1 ]]; then
      echo "key needs exactly namespaces=\"$SIGN_NAMESPACE\": $line" >&2
      return 1
    fi
    case "$type" in
    ssh-ed25519 | sk-ssh-ed25519@openssh.com) ;;
    *)
      echo "key type $type not allowed (ed25519 only): $line" >&2
      return 1
      ;;
    esac
    printf '%s %s\n' "$type" "$key" >"$WORK/k.pub"
    if ! ssh-keygen -l -f "$WORK/k.pub" >/dev/null 2>&1; then
      echo "not a valid public key: $line" >&2
      return 1
    fi
    n=$((n + 1))
  done <"$f"
  if ((n == 0)); then
    echo "no key in $f" >&2
    return 1
  fi
}

# signers_production: scripts/install/release-signers is what every released install.sh
# trusts. The release workflow runs this test before every release (SEC-07).
test_install_sig_signers_production() {
  local f
  f="$(sig_src_dir)/release-signers"
  [[ -f $f ]] || fail "missing $f"
  sig_check_signers "$f" || fail "release-signers is not fit to ship"
}

test_install_sig_check_signers_rejects_bad_files() {
  local key good
  ssh-keygen -q -t ed25519 -N '' -C t -f "$WORK/k1" </dev/null
  key="$(cut -d' ' -f1,2 "$WORK/k1.pub")"
  good="$SIGN_PRINCIPAL namespaces=\"$SIGN_NAMESPACE\" $key owner"
  printf '# comment\n%s\n' "$good" >"$WORK/s"
  sig_check_signers "$WORK/s" || fail "a good file was refused"
  printf '%s\n' "$SIGN_PRINCIPAL valid-after=\"20261101\",namespaces=\"$SIGN_NAMESPACE\",valid-before=\"202712310000Z\" $key" >"$WORK/s"
  sig_check_signers "$WORK/s" || fail "valid-after/valid-before were refused"
  local bad
  for bad in \
    "$SIGN_PRINCIPAL cert-authority,namespaces=\"$SIGN_NAMESPACE\" $key" \
    "$SIGN_PRINCIPAL namespaces=\"$SIGN_NAMESPACE\",namespaces=\"git\" $key" \
    "$SIGN_PRINCIPAL namespaces=\"$SIGN_NAMESPACE,git\" $key" \
    "$SIGN_PRINCIPAL namespaces=\"git\" $key" \
    "$SIGN_PRINCIPAL valid-after=\"yesterday\",namespaces=\"$SIGN_NAMESPACE\" $key" \
    "* namespaces=\"$SIGN_NAMESPACE\" $key" \
    "$SIGN_PRINCIPAL namespaces=\"$SIGN_NAMESPACE\" ssh-rsa AAAAB3NzaC1yc2E" \
    "$SIGN_PRINCIPAL namespaces=\"$SIGN_NAMESPACE\" ssh-ed25519 AAAAnotakey" \
    "$good TEST-ONLY"; do
    printf '%s\n' "$bad" >"$WORK/s"
    if sig_check_signers "$WORK/s" 2>/dev/null; then fail "accepted: $bad"; fi
  done
  printf '# only comments\n' >"$WORK/s"
  if sig_check_signers "$WORK/s" 2>/dev/null; then fail "accepted a file without keys"; fi
}

test_install_sig_assembled_script_embeds_signers() {
  local repo f
  repo="$(cd "$(sig_src_dir)/../.." && pwd)"
  "$repo/scripts/install/assemble.sh" 1.2.3 "$WORK/install.sh" >/dev/null
  f="$(MCPANEL_INSTALL_NO_MAIN=1 bash -c '. "$1"; release_signers' _ "$WORK/install.sh")"
  assert_eq "$(cat "$repo/scripts/install/release-signers")" "$f"
  # test builds only (ci-installer.sh): another signers file and release base, baked in
  printf '%s\n' "$SIGN_PRINCIPAL namespaces=\"$SIGN_NAMESPACE\" ssh-ed25519 AAAAtest" >"$WORK/signers"
  ASSEMBLE_SIGNERS="$WORK/signers" ASSEMBLE_RELEASE_BASE=https://127.0.0.1:8443/rel \
    "$repo/scripts/install/assemble.sh" 1.2.3 "$WORK/test.sh" >/dev/null
  f="$(MCPANEL_INSTALL_NO_MAIN=1 bash -c '. "$1"; release_signers; echo "$RELEASE_BASE"' _ "$WORK/test.sh")"
  assert_eq "$(cat "$WORK/signers")"$'\n'"https://127.0.0.1:8443/rel" "$f"
  printf '# nothing\n' >"$WORK/empty"
  assert_exit 1 env ASSEMBLE_SIGNERS="$WORK/empty" "$repo/scripts/install/assemble.sh" 1.2.3 "$WORK/x.sh"
  assert_exit 1 env ASSEMBLE_RELEASE_BASE=http://example.com "$repo/scripts/install/assemble.sh" 1.2.3 "$WORK/x.sh"
}

# --- no downgrade through "latest" -----------------------------------------------------------

test_ops_update_refuses_a_latest_release_older_than_installed() {
  # whoever controls the GitHub release points "latest" at an old signed release
  ops_fake_install 1.2.0
  ops_probes
  upd_latest_tag() { echo v1.1.0; }
  upd_exec() { echo "exec $*" >>"$WORK/calls.log"; }
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "the latest release (1.1.0) is older than the installed 1.2.0; not downgrading"
  assert_not_called "exec"
  assert_no_file "$ROOT$ROLLBACK_DIR"
  CHECK_ONLY=1
  assert_exit 0 cmd_update
  assert_contains "$(cat "$WORK/out")" "update will not downgrade"
}

test_ops_update_downgrades_only_with_version() {
  sig_handover_fixture 1.1.0
  ops_fake_install 1.2.0
  TARGET_VERSION=1.1.0
  assert_exit 0 cmd_update
  assert_called "exec bash $WORK/install.sh update --version v1.1.0 --yes"
}
