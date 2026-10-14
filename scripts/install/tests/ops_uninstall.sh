# shellcheck shell=bash
# Tests of uninstall.sh (installer plan 03, task 10). Run: bash scripts/install/test.sh tests/ops_uninstall.sh
# Tests override functions (called by the code under test) and set globals (read by it), which
# ShellCheck cannot see from this file (SC2317 is the code of ShellCheck 0.9, SC2329 that of
# newer versions):
# shellcheck disable=SC2034,SC2317,SC2329

OPS_C1=0123456789ab
OPS_C2=ba9876543210

# ops_uninstall_system: an installed system with foreign objects next to mc-panel's.
ops_uninstall_system() {
  local u b
  mkdir -p "$ROOT$BIN_DIR" "$ROOT$UNIT_DIR" "$ROOT$CADDY_DIR" "$ROOT$ETC_PANEL" "$ROOT$ETC_FILES" \
    "$ROOT$DATA_DIR" "$ROOT$ROLLBACK_DIR/1.0.0-20261013T000000Z" "$ROOT$INSTANCES_DIR/instances/1" \
    "$ROOT$INSTANCES_DIR/backups/keys" "$ROOT/run/mc-panel-web" "$ROOT/run/other"
  for b in panel agent files; do ops_fake_bin "$ROOT$BIN_DIR/$b" "echo 1.0.0"; done
  ops_fake_bin "$ROOT$BIN_DIR/other-tool" "echo other"
  for u in "${UPD_UNITS[@]}"; do echo "# $u" >"$ROOT$UNIT_DIR/$u"; done
  echo "# foreign" >"$ROOT$UNIT_DIR/nginx.service"
  printf 'other.example.org {\n\trespond "hi"\n}\nimport mc-panel.caddy # managed by mc-panel\n' >"$ROOT$CADDY_DIR/Caddyfile"
  chmod 0644 "$ROOT$CADDY_DIR/Caddyfile"
  printf 'panel.example.com {\n}\n' >"$ROOT$CADDY_DIR/mc-panel.caddy"
  printf 'public_url = "https://panel.example.com"\n' >"$ROOT$ETC_PANEL/panel.toml"
  echo world >"$ROOT$INSTANCES_DIR/instances/1/level.dat"
  echo key >"$ROOT$INSTANCES_DIR/backups/keys/1.key"
  echo db >"$ROOT$DATA_DIR/panel.db"
  echo keep >"$ROOT/run/other/x.sock"
  uni_probe_containers() { printf '%s\n%s\n' "$OPS_C1" "$OPS_C2"; }
  uni_probe_running_containers() { printf '%s\n' "$OPS_C1"; }
  uni_probe_mountpoint() { return 1; }
  uni_probe_has_router() { return 0; }
  uni_probe_has_network() { return 0; }
  uni_probe_has_table() { return 0; }
  uni_probe_user_exists() { return 0; }
}

# ops_tty LINE: what the operator types on the terminal.
ops_tty() {
  printf '%s\n' "$1" >"$TTY"
}

test_ops_uninstall_removes_program_and_keeps_data() {
  ops_uninstall_system
  YES=1
  assert_exit 0 cmd_uninstall
  assert_no_file "$ROOT$BIN_DIR/panel"
  assert_no_file "$ROOT$BIN_DIR/agent"
  assert_no_file "$ROOT$BIN_DIR/files"
  assert_no_file "$ROOT$UNIT_DIR/mcpanel-panel.service"
  assert_no_file "$ROOT$UNIT_DIR/mcpanel-fw.service"
  assert_no_file "$ROOT$CADDY_DIR/mc-panel.caddy"
  assert_not_contains "$(cat "$ROOT$CADDY_DIR/Caddyfile")" "mc-panel.caddy"
  assert_called 'systemctl stop mcpanel-panel.service'
  assert_called 'systemctl disable mcpanel-agent.service'
  assert_called 'systemctl daemon-reload'
  assert_called 'nft delete table inet mcpanel_fw'
  assert_called 'nft delete table inet mcpanel'
  assert_called "docker rm -f $OPS_C1 $OPS_C2"
  assert_called 'docker rm -f mcpanel-router'
  assert_called 'docker network rm mcpanel-mc'
  assert_called 'systemctl reload caddy'
  assert_no_file "$ROOT/run/mc-panel-web"
  # data, configuration, rollback copies and users stay
  assert_file "$ROOT$INSTANCES_DIR/instances/1/level.dat"
  assert_file "$ROOT$INSTANCES_DIR/backups/keys/1.key"
  assert_file "$ROOT$DATA_DIR/panel.db"
  assert_file "$ROOT$ETC_PANEL/panel.toml"
  assert_file "$ROOT$ROLLBACK_DIR/1.0.0-20261013T000000Z"
  assert_not_called 'userdel'
  assert_contains "$(cat "$WORK/out")" "kept: /srv/mc-panel"
}

test_ops_uninstall_stops_the_agent_before_removing_containers() {
  ops_uninstall_system
  YES=1
  assert_exit 0 cmd_uninstall
  local stop rm
  stop="$(grep -n 'systemctl stop mcpanel-agent.service' "$WORK/calls.log" | cut -d: -f1)"
  rm="$(grep -n "docker rm -f $OPS_C1" "$WORK/calls.log" | cut -d: -f1)"
  ((stop < rm)) || fail "agent stopped at line $stop, containers removed at line $rm"
  # running servers get time to save their worlds before they are removed
  local dstop
  dstop="$(grep -n "docker stop -t 60 $OPS_C1" "$WORK/calls.log" | cut -d: -f1)"
  if [[ -z $dstop ]] || ((dstop >= rm)); then
    fail "docker stop at line '$dstop', docker rm at line $rm"
  fi
  # a socket stops before its service (socket activation would start it again)
  assert_eq "mcpanel-panel.socket mcpanel-panel.service mcpanel-files.socket mcpanel-files.service mcpanel-agent.service mcpanel-fw.service" \
    "$(awk '$1 == "systemctl" && $2 == "stop" { print $3 }' "$WORK/calls.log" | paste -sd' ' -)"
}

test_ops_uninstall_leaves_foreign_objects() {
  ops_uninstall_system
  YES=1
  assert_exit 0 cmd_uninstall
  assert_file "$ROOT$BIN_DIR/other-tool"
  assert_file "$ROOT$UNIT_DIR/nginx.service"
  assert_file "$ROOT/run/other/x.sock"
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" "other.example.org"
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'respond "hi"'
  assert_eq 644 "$(stat -c '%a' "$ROOT$CADDY_DIR/Caddyfile")"
  assert_not_called 'apt-get'
  assert_not_called 'nginx'
  assert_not_called 'nft flush'
  assert_not_called 'nft delete ruleset'
  assert_not_called 'docker system'
  assert_not_called 'docker rmi'
  # exactly the two removals of mc-panel containers: the labelled ones and the router
  assert_eq 2 "$(grep -c '^docker rm -f' "$WORK/calls.log")"
  assert_eq 1 "$(grep -c '^docker stop' "$WORK/calls.log")"
}

test_ops_uninstall_ignores_odd_container_ids() {
  ops_uninstall_system
  uni_probe_containers() { printf '%s\n--all\n\n' "$OPS_C1"; }
  YES=1
  assert_exit 0 cmd_uninstall
  assert_called "docker rm -f $OPS_C1"
  assert_not_called '--all'
}

test_ops_uninstall_keeps_an_unmarked_import_line() {
  ops_uninstall_system
  printf 'import mc-panel.caddy\nother.example.org {\n}\n' >"$ROOT$CADDY_DIR/Caddyfile"
  YES=1
  assert_exit 0 cmd_uninstall
  # only the marked line is ours (overview V3); the operator's own line stays, and so does a
  # loadable (comment-only) mc-panel.caddy, with a loud warning
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'import mc-panel.caddy'
  assert_file_contains "$ROOT$CADDY_DIR/mc-panel.caddy" '# mc-panel was uninstalled'
  assert_not_contains "$(cat "$ROOT$CADDY_DIR/mc-panel.caddy")" 'panel.example.com'
  assert_contains "$(cat "$WORK/out")" "still imports mc-panel.caddy"
  assert_called 'runuser -u caddy -- env HOME=/var/lib/caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile'
  assert_called 'systemctl reload caddy.service'
}

test_ops_uninstall_does_not_reload_an_invalid_caddy_config() {
  ops_uninstall_system
  run_as() {
    echo "runuser -u $*" >>"$WORK/calls.log"
    [[ $1 != caddy ]]
  }
  YES=1
  assert_exit 0 cmd_uninstall
  assert_contains "$(cat "$WORK/out")" "Caddy was not reloaded"
  assert_not_called 'systemctl reload caddy'
}

test_ops_uninstall_leaves_unlabelled_router_and_network() {
  ops_uninstall_system
  uni_probe_has_router() { return 1; }
  uni_probe_has_network() { return 1; }
  YES=1
  assert_exit 0 cmd_uninstall
  assert_not_called 'docker rm -f mcpanel-router'
  assert_not_called 'docker network rm'
}

test_ops_uninstall_without_yes_and_without_terminal_aborts() {
  ops_uninstall_system
  assert_exit 1 cmd_uninstall
  assert_contains "$(cat "$WORK/out")" "nothing was removed"
  assert_file "$ROOT$BIN_DIR/panel"
  assert_not_called 'systemctl'
  assert_not_called 'docker'
}

test_ops_uninstall_asks_on_the_terminal() {
  ops_uninstall_system
  ops_tty n
  assert_exit 1 cmd_uninstall
  assert_file "$ROOT$BIN_DIR/panel"
  ops_tty y
  assert_exit 0 cmd_uninstall
  assert_no_file "$ROOT$BIN_DIR/panel"
}

test_ops_uninstall_is_idempotent() {
  ops_uninstall_system
  YES=1
  assert_exit 0 cmd_uninstall
  uni_probe_containers() { :; }
  uni_probe_running_containers() { :; }
  uni_probe_has_router() { return 1; }
  uni_probe_has_network() { return 1; }
  uni_probe_has_table() { return 1; }
  : >"$WORK/calls.log"
  assert_exit 0 cmd_uninstall
  assert_not_called 'docker'
  assert_not_called 'nft'
  assert_not_called 'systemctl'
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" "other.example.org"
}

test_ops_uninstall_without_docker_and_nft() {
  ops_uninstall_system
  uni_probe_containers() { return 127; }
  uni_probe_running_containers() { return 127; }
  uni_probe_has_router() { return 127; }
  uni_probe_has_network() { return 127; }
  uni_probe_has_table() { return 127; }
  YES=1
  assert_exit 0 cmd_uninstall
  assert_not_called 'docker'
  assert_not_called 'nft'
}

test_ops_purge_needs_typed_domain_even_with_yes() {
  ops_uninstall_system
  YES=1
  PURGE=1
  ops_tty yes
  assert_exit 1 cmd_uninstall
  assert_contains "$(cat "$WORK/out")" "did not match"
  assert_file "$ROOT$INSTANCES_DIR/instances/1/level.dat"
  assert_file "$ROOT$BIN_DIR/panel"
  assert_not_called 'systemctl'
  # the prompt went to the terminal, not to stdout
  assert_file_contains "$TTY" 'Type "panel.example.com" to delete everything'
}

test_ops_purge_with_typed_domain_removes_data_and_users() {
  ops_uninstall_system
  PURGE=1
  ops_tty panel.example.com
  assert_exit 0 cmd_uninstall
  assert_no_file "$ROOT$INSTANCES_DIR"
  assert_no_file "$ROOT$DATA_DIR"
  assert_no_file "$ROOT$ROLLBACK_DIR"
  assert_no_file "$ROOT$ETC_PANEL"
  assert_no_file "$ROOT$ETC_FILES"
  assert_no_file "$ROOT$BIN_DIR/panel"
  assert_called 'userdel mcpanel-mc'
  assert_called 'userdel mcpanel'
  assert_file "$ROOT$BIN_DIR/other-tool"
  assert_file "$ROOT$CADDY_DIR/Caddyfile"
}

test_ops_purge_without_tty_aborts() {
  ops_uninstall_system
  PURGE=1
  YES=1
  assert_exit 1 cmd_uninstall
  assert_contains "$(cat "$WORK/out")" "no terminal"
  assert_file "$ROOT$DATA_DIR/panel.db"
  assert_not_called 'systemctl'
}

test_ops_purge_confirm_word_without_domain() {
  ops_uninstall_system
  rm -f "$ROOT$ETC_PANEL/panel.toml"
  assert_eq "mc-panel" "$(uni_purge_word)"
  PURGE=1
  ops_tty mc-panel
  assert_exit 0 cmd_uninstall
  assert_no_file "$ROOT$DATA_DIR"
}

test_ops_purge_refuses_unexpected_path() {
  assert_exit 1 uni_rm_tree /
  assert_exit 1 uni_rm_tree /var/lib
  assert_exit 1 uni_rm_tree "$INSTANCES_DIR/"
}

test_ops_purge_skips_missing_users() {
  ops_uninstall_system
  uni_probe_user_exists() { [[ $1 == mcpanel ]]; }
  PURGE=1
  ops_tty panel.example.com
  assert_exit 0 cmd_uninstall
  assert_not_called 'userdel mcpanel-mc'
  assert_called 'userdel mcpanel'
}

test_ops_purge_keeps_a_mount_point_and_deletes_its_content() {
  ops_uninstall_system
  uni_probe_mountpoint() { [[ $1 == "$ROOT$INSTANCES_DIR" ]]; }
  PURGE=1
  ops_tty panel.example.com
  assert_exit 0 cmd_uninstall
  assert_file "$ROOT$INSTANCES_DIR"
  assert_eq "" "$(find "$ROOT$INSTANCES_DIR" -mindepth 1)"
  assert_no_file "$ROOT$DATA_DIR"
  assert_contains "$(cat "$WORK/out")" "a mount point; it stays"
}

test_ops_purge_reports_what_it_could_not_delete() {
  ops_uninstall_system
  uni_userdel() { [[ $1 != mcpanel-mc ]]; }
  # an undeletable tree: rm fails for DATA_DIR
  rm() {
    if [[ " $* " == *" $ROOT$DATA_DIR "* ]]; then return 1; fi
    command rm "$@"
  }
  PURGE=1
  ops_tty panel.example.com
  assert_exit 1 cmd_uninstall
  assert_contains "$(cat "$WORK/out")" "could not delete: /var/lib/mc-panel user mcpanel-mc"
  # the others are gone anyway
  assert_no_file "$ROOT$INSTANCES_DIR"
  assert_no_file "$ROOT$ETC_PANEL"
}
