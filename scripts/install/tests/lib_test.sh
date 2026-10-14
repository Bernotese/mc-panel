# shellcheck shell=bash
# Tests of lib.sh and main.sh (installer plan V2, V4). Run: bash scripts/install/test.sh
# Tests override functions (called by the code under test) and set globals and KEY_* values
# (read by it), which shellcheck cannot see from this file:
# shellcheck disable=SC2034,SC2329

test_version_ge() {
  version_ge 1.2.3 1.2.3
  version_ge v1.10.0 1.9.9
  version_ge 2.0.0 1.99.99
  version_ge 1.2.3 1.2.3-rc.1
  version_ge 1.2.3+build.5 1.2.3
  version_ge 1.2.3-rc.2 1.2.3-rc.1
  assert_exit 1 version_ge 1.2.2 1.2.3
  assert_exit 1 version_ge 1.2.3-rc.1 1.2.3
  assert_exit 2 version_ge 1.2 1.2.3
  assert_exit 2 version_ge 1.x.3 1.2.3
}

test_render_template_from_dir() {
  FROM_DIR="$ROOT/src"
  mkdir -p "$FROM_DIR/deploy/x"
  printf 'host {{DOMAIN}}\nport {{SSH_PORT}} and {{DOMAIN}}\n' >"$FROM_DIR/deploy/x/a.conf"
  KEY_DOMAIN=panel.example.com KEY_SSH_PORT=2222
  assert_eq $'host panel.example.com\nport 2222 and panel.example.com' "$(render_template x/a.conf)"
}

test_render_template_missing_value_dies() {
  FROM_DIR="$ROOT/src"
  mkdir -p "$FROM_DIR/deploy"
  printf 'x {{NOPE}}\n' >"$FROM_DIR/deploy/a"
  assert_exit 1 render_template a
  assert_contains "$(cat "$WORK/out")" "no value for {{NOPE}}"
}

test_render_template_embedded_wins_over_template_dir() {
  tpl_x_y_conf() { printf 'embedded {{VERSION}}\n'; }
  TEMPLATE_DIR="$ROOT/tpl"
  mkdir -p "$TEMPLATE_DIR/x"
  printf 'tree\n' >"$TEMPLATE_DIR/x/y.conf"
  KEY_VERSION=1.2.3
  assert_eq "embedded 1.2.3" "$(render_template x/y.conf)"
}

test_render_template_unresolved_braces_die() {
  FROM_DIR="$ROOT/src"
  mkdir -p "$FROM_DIR/deploy"
  printf 'x {{lower}}\n' >"$FROM_DIR/deploy/a"
  assert_exit 1 render_template a
}

test_every_deploy_template_renders() {
  local f rel
  KEY_DOMAIN=panel.example.com KEY_TLS="tls you@example.com" KEY_PANEL_UID=998 \
    KEY_PUBLIC_URL=https://panel.example.com KEY_INSTANCES_DIR=/srv/mc-panel KEY_SSH_PORT="22, 2222" \
    KEY_VERSION=1.2.3 KEY_RESTIC_BINARY=/usr/bin/restic KEY_PING_ADDR=127.0.0.1:25565
  while IFS= read -r f; do
    rel="${f#"$TEMPLATE_DIR"/}"
    assert_not_contains "$(render_template "$rel")" '{{'
  done < <(find "$TEMPLATE_DIR" -type f)
  assert_contains "$(render_template systemd/mcpanel-panel.socket)" "SocketGroup=caddy"
  assert_contains "$(render_template nftables/mcpanel_fw.nft)" "tcp dport { 22, 2222 } accept"
  assert_contains "$(render_template config/panel.toml)" 'public_url = "https://panel.example.com"'
}

test_write_file_changed_then_unchanged() {
  mkdir -p "$ROOT/etc"
  assert_eq changed "$(printf 'a\n' | write_file /etc/x.conf 0640 root root)"
  assert_eq 640 "$(stat -c '%a' "$ROOT/etc/x.conf")"
  assert_eq unchanged "$(printf 'a\n' | write_file /etc/x.conf 0640 root root)"
  assert_eq changed "$(printf 'a\n' | write_file /etc/x.conf 0600 root root)"
  assert_eq changed "$(printf 'b\n' | write_file /etc/x.conf 0600 root root)"
  assert_eq b "$(cat "$ROOT/etc/x.conf")"
  assert_called "chown root:root"
  assert_eq "" "$(find "$ROOT/etc" -name '.mcpanel-install.*')"
  assert_eq "root:root" "$(_owner_get /etc/x.conf)"
  # a simulated owner change makes the next write "changed" even with equal content and mode
  _owner_set /etc/x.conf mcpanel mcpanel 0600
  assert_eq changed "$(printf 'b\n' | write_file /etc/x.conf 0600 root root)"
}

test_ensure_dir() {
  assert_eq changed "$(ensure_dir /srv/mc-panel 0755 root root)"
  [[ -d $ROOT/srv/mc-panel ]] || fail "not created"
  assert_eq unchanged "$(ensure_dir /srv/mc-panel 0755 root root)"
  assert_eq changed "$(ensure_dir /srv/mc-panel 0750 root root)"
  assert_eq 750 "$(stat -c '%a' "$ROOT/srv/mc-panel")"
  assert_eq changed "$(ensure_dir /srv/mc-panel 0750 root mcpanel-mc)"
  assert_eq "root:mcpanel-mc" "$(_owner_get /srv/mc-panel)"
  mkdir -p "$ROOT/real"
  ln -s "$ROOT/real" "$ROOT/link"
  assert_exit 1 ensure_dir /link 0755 root root
  : >"$ROOT/file"
  assert_exit 1 ensure_dir /file 0755 root root
}

test_log_prefixes() {
  assert_eq "[ok] a" "$(log_ok a)"
  assert_eq "[changed] b" "$(log_changed b)"
  assert_eq "[skip] c" "$(log_skip c)"
  assert_eq "[warn] d" "$(warn d)"
}

test_stub_replaces_commands() {
  stub id 'echo 4242'
  assert_eq 4242 "$(id -u mcpanel)"
  # shellcheck disable=SC2016 # the body is code for the stub
  stub sshd '[[ $1 == -T ]] && printf "port 2222\n"'
  assert_eq "port 2222" "$(sshd -T)"
}

test_toml_ensure_key_adds_before_first_section() {
  mkdir -p "$ROOT/etc"
  printf 'a = 1\n\n[limits]\nmax = 2\n' >"$ROOT/etc/c.toml"
  chmod 0640 "$ROOT/etc/c.toml"
  assert_eq changed "$(toml_ensure_key /etc/c.toml restic_binary '"/usr/bin/restic"')"
  local got
  got="$(cat "$ROOT/etc/c.toml")"
  assert_contains "$got" $'restic_binary = "/usr/bin/restic"\n\n[limits]'
  assert_eq 640 "$(stat -c '%a' "$ROOT/etc/c.toml")"
}

test_toml_ensure_key_keeps_existing_value() {
  mkdir -p "$ROOT/etc"
  printf '  restic_binary="/opt/restic"\n[x]\nrestic_binary = "nested"\n' >"$ROOT/etc/c.toml"
  assert_eq unchanged "$(toml_ensure_key /etc/c.toml restic_binary '"/usr/bin/restic"')"
  assert_eq '  restic_binary="/opt/restic"' "$(head -n1 "$ROOT/etc/c.toml")"
}

test_toml_ensure_key_ignores_key_only_in_section() {
  mkdir -p "$ROOT/etc"
  printf '[x]\nmc_user = "a"\n' >"$ROOT/etc/c.toml"
  assert_eq changed "$(toml_ensure_key /etc/c.toml mc_user '"mcpanel-mc"')"
  assert_eq 'mc_user = "mcpanel-mc"' "$(sed -n 2p "$ROOT/etc/c.toml")"
}

test_toml_ensure_key_prefix_is_not_a_match() {
  mkdir -p "$ROOT/etc"
  printf 'instances_dir_old = "/x"\n' >"$ROOT/etc/c.toml"
  assert_eq changed "$(toml_ensure_key /etc/c.toml instances_dir '"/srv/mc-panel"')"
}

test_sha256_verify() {
  printf 'payload' >"$WORK/f.tar.gz"
  local sum
  sum="$(sha256sum "$WORK/f.tar.gz" | awk '{ print $1 }')"
  printf '%s  f.tar.gz\n%s  other\n' "$sum" "$sum" >"$WORK/checksums.txt"
  sha256_verify "$WORK/f.tar.gz" "$WORK/checksums.txt" f.tar.gz
  printf 'changed' >"$WORK/f.tar.gz"
  assert_exit 1 sha256_verify "$WORK/f.tar.gz" "$WORK/checksums.txt" f.tar.gz
  assert_contains "$(cat "$WORK/out")" "checksum mismatch"
}

test_sha256_verify_missing_double_or_malformed_entry() {
  printf 'x' >"$WORK/f"
  printf 'abc  other\n' >"$WORK/c1"
  assert_exit 1 sha256_verify "$WORK/f" "$WORK/c1" f
  printf '%064d  f\n%064d  f\n' 0 0 >"$WORK/c2"
  assert_exit 1 sha256_verify "$WORK/f" "$WORK/c2" f
  printf 'zz  f\n' >"$WORK/c3"
  assert_exit 1 sha256_verify "$WORK/f" "$WORK/c3" f
  assert_exit 1 sha256_verify "$WORK/f" "$WORK/nope" f
}

test_fetch_https_only() {
  curl() { printf '%s\n' "$*" >"$WORK/curl.args"; }
  fetch https://github.com/x "$WORK/x"
  assert_file_contains "$WORK/curl.args" "--proto =https --proto-redir =https --tlsv1.2"
  assert_exit 1 fetch http://github.com/x "$WORK/x"
  assert_exit 1 fetch file:///etc/passwd "$WORK/x"
}

test_fetch_failure_dies() {
  curl() { return 22; }
  assert_exit 1 fetch https://github.com/x "$WORK/x"
  assert_contains "$(cat "$WORK/out")" "download failed"
}

test_confirm() {
  YES=1
  confirm "go?"
  YES=0
  TTY="$WORK/tty"
  printf 'j\n' >"$TTY"
  confirm "go?"
  printf 'n\n' >"$TTY"
  assert_exit 1 confirm "go?"
  TTY="$WORK/none"
  assert_exit 1 confirm "go?"
}

test_sys_wrappers_record_in_dry_run() {
  sys_apt install docker.io
  sys_systemctl daemon-reload
  sys_userdel mcpanel
  run_as mcpanel /usr/local/bin/panel admin list
  assert_called "apt-get -y -q install docker.io"
  assert_called "systemctl daemon-reload"
  assert_called "userdel mcpanel"
  assert_called "runuser -u mcpanel -- /usr/local/bin/panel admin list"
}

test_systemd_wait_active() {
  systemd_wait_active mcpanel-panel 0
  local n=0
  sys_systemctl() {
    n=$((n + 1))
    [[ $n -ge 2 ]]
  }
  systemd_wait_active mcpanel-panel 3
  sys_systemctl() { return 3; }
  assert_exit 1 systemd_wait_active mcpanel-panel 0
}

test_parse_args_install() {
  parse_args install --domain panel.example.com --email=you@example.com --ssh-port 2222 --no-firewall --yes
  assert_eq install "$CMD"
  assert_eq panel.example.com "$DOMAIN"
  assert_eq you@example.com "$EMAIL"
  assert_eq 2222 "$SSH_PORT"
  assert_eq 1 "$NO_FIREWALL"
  assert_eq 1 "$YES"
}

test_parse_args_rejects() {
  assert_exit 1 parse_args
  assert_exit 1 parse_args frobnicate
  assert_exit 1 parse_args install --domain
  assert_exit 1 parse_args install --domain --email x@y.z
  assert_exit 1 parse_args doctor --purge
  assert_exit 1 parse_args update --no-firewall
  assert_exit 0 parse_args --help
}

test_validate_args_install() {
  MCPANEL_VERSION=1.2.3
  parse_args install --domain panel.example.com --email you@example.com
  validate_args
  assert_eq 1.2.3 "$TARGET_VERSION"
  reset_globals
  parse_args install --domain Panel.Example.com --email you@example.com
  assert_exit 1 validate_args
  reset_globals
  parse_args install --domain panel.example.com
  assert_exit 1 validate_args
  reset_globals
  parse_args install --tls internal
  validate_args
  assert_eq localhost "$DOMAIN"
  reset_globals
  parse_args install --domain panel.example.com --email you@example.com --version 1.2
  assert_exit 1 validate_args
}

test_validate_args_dev_build_needs_source() {
  MCPANEL_VERSION=dev
  parse_args install --domain panel.example.com --email you@example.com
  assert_exit 1 validate_args
  assert_contains "$(cat "$WORK/out")" "development build"
  reset_globals
  mkdir -p "$ROOT/build"
  printf '#!/bin/sh\necho v1.4.0\n' >"$ROOT/build/panel"
  chmod +x "$ROOT/build/panel"
  parse_args install --domain panel.example.com --email you@example.com --from-dir "$ROOT/build"
  validate_args
  assert_eq "$ROOT/build" "$FROM_DIR"
  assert_eq 1.4.0 "$TARGET_VERSION" "--from-dir takes the version of the build"
  assert_eq v1.4.0 "$(release_tag "$TARGET_VERSION")"
}

test_main_keeps_errexit_in_commands() {
  # cmd_* run in a subshell with set -e: a failing step stops the command with its code.
  MCPANEL_VERSION=1.2.3
  cmd_doctor() {
    false
    echo "not reached"
  }
  require_root() { :; }
  setup_log() { :; }
  assert_exit 1 main doctor
  assert_not_contains "$(cat "$WORK/out")" "not reached"
}

test_validate_args_admin_password_file() {
  MCPANEL_VERSION=1.2.3
  printf 'secret\n' >"$WORK/pw"
  chmod 0644 "$WORK/pw"
  parse_args install --domain panel.example.com --email you@example.com --admin-user alice --admin-password-file "$WORK/pw"
  assert_exit 1 validate_args
  assert_contains "$(cat "$WORK/out")" "mode 0600"
  chmod 0600 "$WORK/pw"
  validate_args
  reset_globals
  parse_args install --domain panel.example.com --email you@example.com --admin-password-file "$WORK/pw"
  assert_exit 1 validate_args
}

test_setup_work_takes_handoff_dir() {
  local parent
  parent="$(mktemp -d "${TMPDIR:-/tmp}/mc-panel-install.XXXXXX")"
  MCPANEL_HANDOFF_WORK="$parent"
  setup_work
  assert_eq "$parent" "$WORK"
  trap 'rm -rf -- "$ROOT"' EXIT # setup_work replaced the harness's cleanup trap
  rm -rf -- "$parent"
  MCPANEL_HANDOFF_WORK=/etc
  setup_work
  [[ $WORK != /etc ]] || fail "a foreign directory was taken as WORK"
  trap 'rm -rf -- "$ROOT"' EXIT
  rm -rf -- "$WORK"
}

test_validate_args_update_from_dir_takes_build_version() {
  mkdir -p "$ROOT/build"
  printf '#!/bin/sh\necho 0.0.0-testvm+abc123\n' >"$ROOT/build/panel"
  chmod +x "$ROOT/build/panel"
  parse_args update --from-dir "$ROOT/build" --force
  validate_args
  assert_eq 0.0.0-testvm+abc123 "$TARGET_VERSION"
  printf '#!/bin/sh\necho dev\n' >"$ROOT/build/panel"
  TARGET_VERSION=""
  assert_exit 1 validate_args
  assert_contains "$(cat "$WORK/out")" "no release version"
}

test_validate_args_version_and_from_dir_exclude() {
  mkdir -p "$ROOT/build"
  parse_args update --version v1.2.3 --from-dir "$ROOT/build"
  assert_exit 1 validate_args
}

test_detect_arch() {
  uname() { echo aarch64; }
  assert_eq arm64 "$(detect_arch)"
  uname() { echo x86_64; }
  assert_eq amd64 "$(detect_arch)"
  uname() { echo riscv64; }
  assert_exit 1 detect_arch
}

test_cmd_install_calls_every_step_in_order() {
  local order=""
  pre_check() { order+="pre "; }
  pkg_install() { order+="pkg "; }
  lay_ensure() { order+="lay "; }
  bin_install() { order+="bin "; }
  cfg_ensure() { order+="cfg "; }
  unit_ensure() { order+="unit "; }
  cad_ensure() { order+="cad "; }
  fw_ensure() { order+="fw "; }
  upg_ensure() { order+="upg "; }
  adm_ensure() { order+="adm "; }
  cmd_doctor() {
    order+="doc"
    [[ $DOC_SUMMARY == 1 ]] || order+="(long)"
  }
  DOMAIN=panel.example.com
  cmd_install >/dev/null
  assert_eq "pre pkg lay bin cfg unit cad fw upg adm doc" "$order"
  order=""
  NO_FIREWALL=1
  cmd_install >/dev/null
  assert_eq "pre pkg lay bin cfg unit cad upg adm doc" "$order"
}
