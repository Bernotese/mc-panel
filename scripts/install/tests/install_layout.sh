# shellcheck shell=bash
# Tests of layout.sh (installer spec §4.3). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329

test_install_lay_ensure_creates_users_when_missing() {
  stub lay_uid 'return 1'
  lay_ensure >/dev/null
  assert_called 'useradd --system --user-group --home-dir /var/lib/mc-panel --no-create-home --shell /usr/sbin/nologin mcpanel'
  assert_called 'useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin mcpanel-mc'
}

test_install_lay_ensure_dirs_modes_and_owners() {
  stub lay_uid 'echo 998'
  lay_ensure >/dev/null
  assert_eq root:mcpanel "$(_owner_get /etc/mc-panel)"
  assert_eq 750 "$(stat -c '%a' "$ROOT/etc/mc-panel")"
  assert_eq root:mcpanel-mc "$(_owner_get /etc/mc-panel-files)"
  assert_eq mcpanel:mcpanel "$(_owner_get /var/lib/mc-panel)"
  assert_eq 700 "$(stat -c '%a' "$ROOT/var/lib/mc-panel")"
  assert_eq root:root "$(_owner_get /srv/mc-panel)"
  assert_eq 755 "$(stat -c '%a' "$ROOT/srv/mc-panel")"
  # instances/ and backups/ must exist before mcpanel-files starts (ReadWritePaths)
  assert_eq root:mcpanel-mc "$(_owner_get /srv/mc-panel/instances)"
  assert_eq 750 "$(stat -c '%a' "$ROOT/srv/mc-panel/instances")"
  assert_eq mcpanel-mc:mcpanel-mc "$(_owner_get /srv/mc-panel/backups)"
  assert_eq 700 "$(stat -c '%a' "$ROOT/srv/mc-panel/backups")"
  assert_called 'chown -h mcpanel:mcpanel '"$ROOT"'/var/lib/mc-panel'
}

test_install_lay_ensure_keeps_existing_users() {
  stub lay_uid 'echo 998'
  lay_ensure >/dev/null
  assert_not_called 'useradd'
}

test_install_lay_ensure_second_run_unchanged() {
  stub lay_uid 'echo 998'
  lay_ensure >/dev/null
  : >"$WORK/calls.log"
  local out
  out="$(lay_ensure)"
  assert_contains "$out" "[skip]"
  assert_not_called 'chown'
}

test_install_lay_ensure_corrects_mode() {
  stub lay_uid 'echo 998'
  lay_ensure >/dev/null
  chmod 0755 "$ROOT/var/lib/mc-panel"
  assert_contains "$(lay_ensure)" "[changed] /var/lib/mc-panel"
  assert_eq 700 "$(stat -c '%a' "$ROOT/var/lib/mc-panel")"
}

test_install_lay_ensure_refuses_symlink() {
  stub lay_uid 'echo 998'
  mkdir -p "$ROOT/elsewhere" "$ROOT/etc"
  ln -s "$ROOT/elsewhere" "$ROOT/etc/mc-panel"
  assert_fails lay_ensure
  assert_contains "$(cat "$WORK/out")" "symlink"
}

test_install_lay_ensure_fails_when_useradd_fails() {
  stub lay_uid 'return 1'
  stub sys_useradd 'return 9'
  assert_fails lay_ensure
  assert_no_file "$ROOT/etc/mc-panel"
}
