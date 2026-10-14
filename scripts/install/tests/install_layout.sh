# shellcheck shell=bash
# Tests of layout.sh (installer spec §4.3). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329

# lay_users_exist: both system users exist (nologin, UID < 1000); lay_users_missing: neither
# the users nor their groups exist.
lay_users_exist() {
  # shellcheck disable=SC2016 # the body is code for the stub
  stub lay_passwd 'printf "%s:x:998:998::/nonexistent:/usr/sbin/nologin\n" "$1"'
  stub lay_group_exists 'return 0'
}
lay_users_missing() {
  stub lay_passwd ':'
  stub lay_group_exists 'return 1'
}

test_install_lay_ensure_creates_users_when_missing() {
  lay_users_missing
  lay_ensure >/dev/null
  assert_called 'useradd --system --user-group --home-dir /var/lib/mc-panel --no-create-home --shell /usr/sbin/nologin mcpanel'
  assert_called 'useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin mcpanel-mc'
}

test_install_lay_ensure_dirs_modes_and_owners() {
  lay_users_exist
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
  lay_users_exist
  lay_ensure >/dev/null
  assert_not_called 'useradd'
}

test_install_lay_ensure_second_run_unchanged() {
  lay_users_exist
  lay_ensure >/dev/null
  : >"$WORK/calls.log"
  local out
  out="$(lay_ensure)"
  assert_contains "$out" "[skip]"
  assert_not_called 'chown'
}

test_install_lay_ensure_corrects_mode() {
  lay_users_exist
  lay_ensure >/dev/null
  chmod 0755 "$ROOT/var/lib/mc-panel"
  assert_contains "$(lay_ensure)" "[changed] /var/lib/mc-panel"
  assert_eq 700 "$(stat -c '%a' "$ROOT/var/lib/mc-panel")"
}

test_install_lay_ensure_refuses_symlink() {
  lay_users_exist
  mkdir -p "$ROOT/elsewhere" "$ROOT/etc"
  ln -s "$ROOT/elsewhere" "$ROOT/etc/mc-panel"
  assert_fails lay_ensure
  assert_contains "$(cat "$WORK/out")" "symlink"
}

test_install_lay_ensure_fails_when_useradd_fails() {
  lay_users_missing
  stub sys_useradd 'return 9'
  assert_fails lay_ensure
  assert_no_file "$ROOT/etc/mc-panel"
}

test_install_lay_ensure_uses_an_existing_group() {
  stub lay_passwd ':'
  # shellcheck disable=SC2016 # the body is code for the stub
  stub lay_group_exists '[[ $1 == mcpanel ]]'
  lay_ensure >/dev/null
  assert_called 'useradd --system -g mcpanel --home-dir /var/lib/mc-panel --no-create-home --shell /usr/sbin/nologin mcpanel'
  assert_called 'useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin mcpanel-mc'
}

test_install_lay_ensure_warns_about_login_users() {
  # shellcheck disable=SC2016 # the body is code for the stub
  stub lay_passwd '[[ $1 == mcpanel ]] && echo "mcpanel:x:1001:1001::/home/mcpanel:/bin/bash"; [[ $1 == mcpanel-mc ]] && echo "mcpanel-mc:x:997:997::/nonexistent:/usr/sbin/nologin"; :'
  stub lay_group_exists 'return 0'
  local out
  out="$(lay_ensure)"
  assert_contains "$out" "[warn] user mcpanel exists with UID 1001"
  assert_contains "$out" "[warn] user mcpanel has the login shell /bin/bash"
  assert_not_contains "$out" "user mcpanel-mc"
  assert_not_called 'useradd'
}
