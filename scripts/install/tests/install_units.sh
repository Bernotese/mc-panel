# shellcheck shell=bash
# Tests of units.sh (installer spec §4.6). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329

unit_fixture() {
  render_template() { printf '# %s\n' "$1"; }
  systemd_wait_active() { return 0; }
  stub unit_settled 'return 0'
  stub unit_panel_health 'return 0'
  CMD=install
}

# unit_started: the units started or restarted, in call order.
unit_started() {
  grep -E '^systemctl (restart|start) ' "$WORK/calls.log" | awk '{ print $3 }' | xargs
}

test_install_unit_ensure_writes_reloads_enables_starts_in_order() {
  unit_fixture
  unit_ensure >/dev/null
  assert_file_contains "$ROOT$UNIT_DIR/mcpanel-panel.socket" '# systemd/mcpanel-panel.socket'
  assert_file_contains "$ROOT$UNIT_DIR/mcpanel-fw.service" '# systemd/mcpanel-fw.service'
  assert_eq 644 "$(stat -c '%a' "$ROOT$UNIT_DIR/mcpanel-agent.service")"
  assert_eq root:root "$(_owner_get "$UNIT_DIR/mcpanel-agent.service")"
  assert_called 'systemctl daemon-reload'
  assert_called 'systemctl enable mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service'
  assert_eq "mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service" "$(unit_started)"
}

test_install_unit_ensure_renders_the_real_templates() {
  stub systemd_wait_active 'return 0'
  stub unit_settled 'return 0'
  stub unit_panel_health 'return 0'
  CMD=install
  KEY_VERSION=1.2.3 KEY_INSTANCES_DIR=/srv/mc-panel
  unit_ensure >/dev/null
  assert_file_contains "$ROOT$UNIT_DIR/mcpanel-files.service" 'ReadWritePaths=/srv/mc-panel/instances /srv/mc-panel/backups'
  assert_file_contains "$ROOT$UNIT_DIR/mcpanel-panel.socket" 'SocketGroup=caddy'
}

test_install_unit_ensure_never_enables_the_firewall_unit() {
  # Only fw_ensure enables/starts mcpanel-fw.service (never with --no-firewall).
  unit_fixture
  unit_ensure >/dev/null
  assert_file "$ROOT$UNIT_DIR/mcpanel-fw.service"
  assert_not_called 'mcpanel-fw.service'
}

test_install_unit_ensure_on_update_only_writes_and_reloads() {
  # update restarts and checks health itself (overview V4).
  unit_fixture
  CMD=update
  unit_ensure >/dev/null
  assert_called 'systemctl daemon-reload'
  assert_not_called 'systemctl enable'
  assert_not_called 'systemctl start'
  assert_not_called 'systemctl restart'
}

test_install_unit_ensure_second_run_does_not_restart() {
  unit_fixture
  unit_ensure >/dev/null
  : >"$WORK/calls.log"
  assert_contains "$(unit_ensure)" "[ok] units unchanged"
  assert_not_called 'daemon-reload'
  assert_not_called 'systemctl restart'
  assert_called 'systemctl start mcpanel-agent.service'
}

test_install_unit_ensure_restarts_only_changed() {
  unit_fixture
  unit_ensure >/dev/null
  : >"$WORK/calls.log"
  render_template() { if [[ $1 == systemd/mcpanel-files.service ]]; then echo changed; else printf '# %s\n' "$1"; fi; }
  unit_ensure >/dev/null
  assert_called 'systemctl daemon-reload'
  assert_called 'systemctl restart mcpanel-files.service'
  assert_not_called 'systemctl restart mcpanel-agent.service'
  assert_called 'systemctl start mcpanel-agent.service'
}

test_install_unit_ensure_restarts_services_after_new_binaries() {
  unit_fixture
  unit_ensure >/dev/null
  : >"$WORK/calls.log"
  BIN_CHANGED=1
  unit_ensure >/dev/null
  assert_called 'systemctl restart mcpanel-agent.service'
  assert_called 'systemctl restart mcpanel-panel.service'
  assert_called 'systemctl start mcpanel-panel.socket'
  assert_not_called 'systemctl restart mcpanel-panel.socket'
}

test_install_unit_ensure_fails_when_unit_does_not_come_up() {
  unit_fixture
  systemd_wait_active() { [[ $1 != mcpanel-files.service ]]; }
  assert_fails unit_ensure
  assert_contains "$(cat "$WORK/out")" "mcpanel-files.service did not become active"
  # nothing after the failing unit was started
  assert_eq "mcpanel-agent.service mcpanel-files.socket mcpanel-files.service" "$(unit_started)"
}

test_install_unit_ensure_fails_without_template_value() {
  CMD=install
  unset KEY_INSTANCES_DIR
  KEY_VERSION=1.2.3
  assert_fails unit_ensure
  assert_no_file "$ROOT$UNIT_DIR/mcpanel-files.service"
  assert_not_called 'systemctl'
}

test_install_unit_ensure_fails_when_a_service_does_not_stay_up() {
  unit_fixture
  # shellcheck disable=SC2016 # the body is code for the stub
  stub unit_settled '[[ $1 != mcpanel-agent.service ]]'
  assert_fails unit_ensure
  assert_contains "$(cat "$WORK/out")" "mcpanel-agent.service does not stay active"
  assert_eq mcpanel-agent.service "$(unit_started)"
}

test_install_unit_settled_reads_state_and_restarts() {
  UNIT_SETTLE=0
  stub systemctl 'printf "NRestarts=0\nActiveState=active\n"'
  unit_settled mcpanel-panel.service
  stub systemctl 'printf "ActiveState=active\nNRestarts=2\n"'
  assert_fails unit_settled mcpanel-panel.service
  stub systemctl 'printf "ActiveState=activating\nNRestarts=0\n"'
  assert_fails unit_settled mcpanel-panel.service
  stub systemctl 'printf "ActiveState=active\n"' # sockets have no NRestarts
  unit_settled mcpanel-panel.socket
}

test_install_unit_ensure_fails_when_the_panel_does_not_answer() {
  unit_fixture
  stub unit_panel_health 'return 1'
  assert_fails unit_ensure
  assert_contains "$(cat "$WORK/out")" "does not answer on /run/mc-panel-web/panel.sock"
}
