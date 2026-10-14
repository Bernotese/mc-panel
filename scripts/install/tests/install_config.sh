# shellcheck shell=bash
# Tests of config.sh (installer spec §4.5, review focus 2). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329

cfg_fixture() {
  # shellcheck disable=SC2016 # the body is code for the stub
  stub lay_uid '[[ $1 == mcpanel ]] && echo 998'
  PKG_RESTIC_BIN=/usr/bin/restic
  DOMAIN=panel.example.com TLS_MODE=letsencrypt EMAIL=a@example.com TARGET_VERSION=1.2.3
  mkdir -p "$ROOT$ETC_PANEL" "$ROOT$ETC_FILES"
}

test_install_cfg_keys_exports_template_values() {
  cfg_fixture
  cfg_keys
  assert_eq 998 "$KEY_PANEL_UID"
  assert_eq panel.example.com "$KEY_DOMAIN"
  assert_eq https://panel.example.com "$KEY_PUBLIC_URL"
  assert_eq /srv/mc-panel "$KEY_INSTANCES_DIR"
  assert_eq /usr/bin/restic "$KEY_RESTIC_BINARY"
  assert_eq 127.0.0.1:25565 "$KEY_PING_ADDR"
  assert_eq 1.2.3 "$KEY_VERSION"
  assert_eq "tls a@example.com" "$KEY_TLS"
  TLS_MODE=internal
  cfg_keys
  assert_eq "tls internal" "$KEY_TLS"
  # exported: render_template sees them in subshells
  assert_eq 998 "$(bash -c 'echo "$KEY_PANEL_UID"')"
}

test_install_cfg_keys_fails_without_user() {
  cfg_fixture
  stub lay_uid 'return 1'
  assert_fails cfg_keys
}

test_install_cfg_ensure_writes_missing_files() {
  cfg_fixture
  local out
  out="$(cfg_ensure)"
  assert_contains "$out" "[changed]"
  assert_file_contains "$ROOT$ETC_PANEL/panel.toml" 'public_url = "https://panel.example.com"'
  assert_file_contains "$ROOT$ETC_PANEL/panel.toml" 'listen = "unix:/run/mc-panel-web/panel.sock"'
  assert_file_contains "$ROOT$ETC_PANEL/panel.toml" 'install.sh 1.2.3'
  assert_file_contains "$ROOT$ETC_PANEL/agent.toml" 'panel_uid = 998'
  assert_file_contains "$ROOT$ETC_FILES/files.toml" 'restic_binary = "/usr/bin/restic"'
  assert_eq 640 "$(stat -c '%a' "$ROOT$ETC_PANEL/panel.toml")"
  assert_eq 600 "$(stat -c '%a' "$ROOT$ETC_PANEL/agent.toml")"
  assert_eq 640 "$(stat -c '%a' "$ROOT$ETC_FILES/files.toml")"
  assert_eq root:mcpanel "$(_owner_get "$ETC_PANEL/panel.toml")"
  assert_eq root:root "$(_owner_get "$ETC_PANEL/agent.toml")"
  assert_eq root:mcpanel-mc "$(_owner_get "$ETC_FILES/files.toml")"
}

test_install_cfg_ensure_rendered_files_have_every_required_key() {
  cfg_fixture
  cfg_ensure >/dev/null
  local key
  for key in "${!CFG_REQUIRED_PANEL[@]}"; do
    assert_eq unchanged "$(toml_ensure_key "$ETC_PANEL/panel.toml" "$key" x)" "panel.toml: $key"
  done
  for key in "${!CFG_REQUIRED_AGENT[@]}"; do
    assert_eq unchanged "$(toml_ensure_key "$ETC_PANEL/agent.toml" "$key" x)" "agent.toml: $key"
  done
  for key in "${!CFG_REQUIRED_FILES[@]}"; do
    assert_eq unchanged "$(toml_ensure_key "$ETC_FILES/files.toml" "$key" x)" "files.toml: $key"
  done
}

test_install_cfg_ensure_keeps_operator_values() {
  cfg_fixture
  cfg_ensure >/dev/null
  sed -i 's|^public_url = .*|public_url = "https://other.example.org"|' "$ROOT$ETC_PANEL/panel.toml"
  printf '\n[session]\nidle_timeout = "1h"\n' >>"$ROOT$ETC_PANEL/panel.toml"
  cp "$ROOT$ETC_PANEL/panel.toml" "$WORK/before"
  cfg_ensure >/dev/null
  assert_file_eq "$ROOT$ETC_PANEL/panel.toml" "$(cat "$WORK/before")"
}

test_install_cfg_ensure_adds_only_missing_keys() {
  cfg_fixture
  cfg_ensure >/dev/null
  grep -v '^monitoring_ping_addr' "$ROOT$ETC_PANEL/panel.toml" >"$WORK/p"
  cat "$WORK/p" >"$ROOT$ETC_PANEL/panel.toml"
  assert_contains "$(cfg_ensure)" "panel.toml: changed, agent.toml: unchanged"
  assert_file_contains "$ROOT$ETC_PANEL/panel.toml" 'monitoring_ping_addr = "127.0.0.1:25565"'
  assert_file_contains "$ROOT$ETC_PANEL/panel.toml" '# added by install.sh'
  assert_eq 1 "$(grep -c '^listen' "$ROOT$ETC_PANEL/panel.toml")"
}

test_install_cfg_ensure_corrects_mode_of_existing_file() {
  cfg_fixture
  cfg_ensure >/dev/null
  chmod 0644 "$ROOT$ETC_PANEL/agent.toml"
  assert_contains "$(cfg_ensure)" "agent.toml: changed"
  assert_eq 600 "$(stat -c '%a' "$ROOT$ETC_PANEL/agent.toml")"
}

test_install_cfg_ensure_refuses_symlinked_config() {
  cfg_fixture
  printf 'x = 1\n' >"$WORK/elsewhere.toml"
  ln -s "$WORK/elsewhere.toml" "$ROOT$ETC_PANEL/agent.toml"
  assert_fails cfg_ensure
  assert_file_eq "$WORK/elsewhere.toml" 'x = 1'
}

test_install_cfg_restic_binary_from_path_on_update() {
  cfg_fixture
  PKG_RESTIC_BIN="" # update does not run pkg_install
  stub cfg_restic_path 'echo /usr/local/bin/restic'
  cfg_keys
  assert_eq /usr/local/bin/restic "$KEY_RESTIC_BINARY"
}

test_install_cfg_update_takes_domain_from_panel_toml() {
  cfg_fixture
  cfg_ensure >/dev/null
  sed -i 's|^public_url = .*|public_url = "https://other.example.org/"|' "$ROOT$ETC_PANEL/panel.toml"
  DOMAIN='' CMD=update
  cfg_keys
  assert_eq other.example.org "$KEY_DOMAIN"
  assert_eq '"https://other.example.org"' "${CFG_REQUIRED_PANEL[public_url]}"
}

test_install_cfg_update_without_domain_needs_existing_panel_toml() {
  cfg_fixture
  DOMAIN='' CMD=update
  assert_fails cfg_ensure
  assert_no_file "$ROOT$ETC_PANEL/panel.toml"
}

test_install_cfg_ensure_second_run_unchanged() {
  cfg_fixture
  cfg_ensure >/dev/null
  CFG_CHANGED=0
  assert_contains "$(cfg_ensure)" "unchanged"
  cfg_ensure >/dev/null
  assert_eq 0 "$CFG_CHANGED"
}
