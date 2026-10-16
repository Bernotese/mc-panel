# shellcheck shell=bash
# Tests of update.sh (installer plan 03, task 8). Run: bash scripts/install/test.sh tests/ops_update.sh
# Tests override functions (called by the code under test) and set globals (read by it), which
# ShellCheck cannot see from this file (SC2317 is the code of ShellCheck 0.9, SC2329 that of
# newer versions):
# shellcheck disable=SC2034,SC2317,SC2329

# ops_fake_bin PATH BODY writes an executable shell script (a fake binary). Not fake_exec: that
# helper of the harness records an exec call.
ops_fake_bin() {
  mkdir -p -- "${1%/*}"
  printf '#!/bin/sh\n%s\n' "$2" >"$1"
  chmod +x -- "$1"
}

# ops_fake_install VERSION: an installed system in $ROOT - three fake binaries that print
# VERSION, the units, the panel configuration and a database file.
ops_fake_install() {
  local version="$1" b u
  mkdir -p "$ROOT$BIN_DIR" "$ROOT$UNIT_DIR" "$ROOT$DATA_DIR" "$ROOT$ETC_PANEL" "$ROOT$CADDY_DIR"
  for b in panel agent files; do
    ops_fake_bin "$ROOT$BIN_DIR/$b" "echo $version"
  done
  for u in "${UPD_UNITS[@]}"; do
    printf '# %s %s\n' "$u" "$version" >"$ROOT$UNIT_DIR/$u"
  done
  printf 'db %s\n' "$version" >"$ROOT$DATA_DIR/panel.db"
  printf 'public_url = "https://panel.example.com"\n' >"$ROOT$ETC_PANEL/panel.toml"
  chmod 0640 "$ROOT$ETC_PANEL/panel.toml"
  printf 'panel.example.com {\n\ttls you@example.com\n}\n' >"$ROOT$CADDY_DIR/mc-panel.caddy"
}

# ops_fake_new_build VERSION: a build of VERSION in $WORK/build (FROM_DIR), and
# bin_install/cfg_ensure/unit_ensure that install VERSION (they stand in for B1's steps).
ops_fake_new_build() {
  local b
  OPS_NEW_VERSION="$1"
  mkdir -p "$WORK/build/deploy"
  for b in panel agent files; do
    ops_fake_bin "$WORK/build/$b" "echo $OPS_NEW_VERSION"
  done
  FROM_DIR="$WORK/build"
  TARGET_VERSION="$1" # validate_args takes it from the build (--from-dir)
  # bin_source of B1 returns FROM_DIR as is; this stands in while config.sh/binaries.sh are stubs
  if ! declare -F bin_source >/dev/null; then
    bin_source() { echo "$FROM_DIR"; }
  fi
  bin_install() {
    local b
    for b in panel agent files; do
      ops_fake_bin "$ROOT$BIN_DIR/$b" "echo $OPS_NEW_VERSION"
    done
  }
  unit_ensure() {
    local u
    for u in "${UPD_UNITS[@]}"; do
      printf '# %s %s\n' "$u" "$OPS_NEW_VERSION" >"$ROOT$UNIT_DIR/$u"
    done
  }
  cfg_ensure() { :; }
}

# ops_probes: healthy unless OPS_UNHEALTHY is set; versions come from the fake binaries; the DB
# copies are plain cp (run_as only records in dry-run mode).
ops_probes() {
  upd_probe_health() { [[ -z ${OPS_UNHEALTHY:-} ]]; }
  upd_db_backup() { cp -- "$ROOT$DATA_DIR/panel.db" "$1"; }
  upd_db_restore() { cp -- "$1" "$ROOT$DATA_DIR/panel.db"; }
  upd_free_bytes() { echo "${OPS_FREE:-100000000000}"; }
  upd_probe_restarts() { echo 0; }
  upd_probe_unit_state() { echo "active/running (success)"; }
  upd_probe_unit_report() { echo "unit report since $1"; }
  # never the network in a test (tests that need a download define fetch themselves)
  fetch() { fail "network access in a test: $1"; }
  upd_fetch_latest_json() { fail "network access in a test: GitHub API"; }
  systemd_wait_active() { [[ -z ${OPS_UNHEALTHY:-} ]]; }
  sleep() { :; }
  # the steps after a successful swap (upd_system) only record themselves here
  pkg_caddy_ensure() { echo "step pkg_caddy_ensure" >>"$WORK/calls.log"; }
  cad_ensure() { echo "step cad_ensure $DOMAIN $TLS_MODE" >>"$WORK/calls.log"; }
  upg_ensure() { echo "step upg_ensure" >>"$WORK/calls.log"; }
}

# ops_only_dir: the single rollback dir.
ops_only_dir() {
  find "$ROOT$ROLLBACK_DIR" -mindepth 1 -maxdepth 1 -type d
}

test_ops_update_check_only_reports_newer() {
  ops_fake_install 1.0.0
  ops_probes
  upd_latest_tag() { echo v1.1.0; }
  CHECK_ONLY=1
  local out
  out="$(cmd_update 2>&1)"
  assert_contains "$out" "available 1.1.0"
  assert_not_called 'systemctl'
  assert_no_file "$ROOT$ROLLBACK_DIR"
  assert_eq "db 1.0.0" "$(cat "$ROOT$DATA_DIR/panel.db")"
}

test_ops_update_without_network_dies_without_version() {
  ops_fake_install 1.0.0
  ops_probes
  upd_fetch_latest_json() { return 6; }
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "--version"
}

test_ops_update_same_version_without_force_is_a_noop() {
  ops_fake_install 1.1.0
  ops_probes
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  assert_exit 0 cmd_update
  assert_contains "$(cat "$WORK/out")" "already at 1.1.0"
  assert_no_file "$ROOT$ROLLBACK_DIR"
  assert_not_called 'systemctl'
}

test_ops_update_not_installed_refuses() {
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "not installed"
  assert_no_file "$ROOT$BIN_DIR/panel"
}

test_ops_update_hands_over_to_target_installer() {
  ops_fake_install 1.0.0
  ops_probes
  MCPANEL_VERSION=1.0.0
  TARGET_VERSION=v1.1.0
  YES=1
  fetch() { printf '#!/bin/bash\necho child "$@"\n' >"$2"; }
  sha256_verify() { return 0; }
  upd_exec() { echo "exec $* handoff=$MCPANEL_UPDATE_HANDOFF work=$MCPANEL_HANDOFF_WORK" >>"$WORK/calls.log"; }
  assert_exit 0 cmd_update
  assert_called "exec bash $WORK/install.sh update --version v1.1.0 --yes handoff=1 work=$WORK"
  assert_no_file "$ROOT$ROLLBACK_DIR"
}

test_ops_update_child_does_not_hand_over_again() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  MCPANEL_VERSION=1.0.0 # an older script, but it was handed over to: no second hand-over
  MCPANEL_UPDATE_HANDOFF=1
  TARGET_VERSION=1.1.0
  upd_exec() { echo "exec $*" >>"$WORK/calls.log"; }
  assert_exit 0 cmd_update
  assert_not_called 'exec'
  assert_eq "1.1.0" "$("$ROOT$BIN_DIR/panel" version)"
}

test_ops_update_refuses_bad_checksum_before_any_change() {
  ops_fake_install 1.0.0
  ops_probes
  MCPANEL_VERSION=1.0.0
  TARGET_VERSION=1.1.0
  fetch() { echo x >"$2"; }
  sha256_verify() { die "checksum mismatch for $3"; }
  upd_exec() { echo "exec $*" >>"$WORK/calls.log"; }
  assert_exit 1 cmd_update
  assert_not_called 'exec'
  assert_no_file "$ROOT$ROLLBACK_DIR"
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
}

test_ops_update_success_keeps_backup_and_marks_verified() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=v1.1.0
  FROM_DIR="$WORK/build"
  assert_exit 0 cmd_update
  local dir
  dir="$(ops_only_dir)"
  assert_file_contains "$dir/state" "phase=verified"
  assert_file_contains "$dir/state" "from=1.0.0"
  assert_file_contains "$dir/state" "to=1.1.0"
  assert_eq "db 1.0.0" "$(cat "$dir/panel.db")"
  assert_eq "1.0.0" "$("$dir/bin/panel" version)"
  assert_file_contains "$dir/units/mcpanel-panel.service" "1.0.0"
  assert_eq 700 "$(stat -c '%a' "$ROOT$ROLLBACK_DIR")"
  assert_eq root:root "$(_owner_get "$ROLLBACK_DIR")"
  assert_eq "1.1.0" "$("$ROOT$BIN_DIR/panel" version)"
  assert_called 'systemctl daemon-reload'
  assert_called 'systemctl restart mcpanel-agent.service'
  assert_called 'systemctl restart mcpanel-panel.service'
  assert_not_called 'systemctl stop'
  # restart order agent -> files -> panel (spec §5 step 4)
  assert_eq "mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service" \
    "$(awk '$2 == "restart" { print $3 }' "$WORK/calls.log" | paste -sd' ' -)"
}

test_ops_update_passes_installed_domain_to_cfg() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  cfg_ensure() { echo "cfg $DOMAIN $TLS_MODE $EMAIL" >>"$WORK/calls.log"; }
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  assert_exit 0 cmd_update
  assert_called 'cfg panel.example.com letsencrypt you@example.com'
}

test_ops_update_rolls_back_on_failed_health() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  # Unhealthy after the swap, healthy again once the old version is back.
  upd_probe_health() { [[ $("$ROOT$BIN_DIR/panel" version) == 1.0.0 ]]; }
  UPD_HEALTH_SECS=0
  # new units, and the new version's migration changed the DB
  unit_ensure() {
    local u
    for u in "${UPD_UNITS[@]}"; do
      printf '# %s 1.1.0\n' "$u" >"$ROOT$UNIT_DIR/$u"
    done
    echo "db migrated" >"$ROOT$DATA_DIR/panel.db"
  }
  assert_exit 3 cmd_update
  assert_contains "$(cat "$WORK/out")" "rolled back to 1.0.0"
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/files" -version)"
  assert_eq "db 1.0.0" "$(cat "$ROOT$DATA_DIR/panel.db")"
  assert_file_contains "$ROOT$UNIT_DIR/mcpanel-panel.service" "1.0.0"
  assert_file_contains "$(ops_only_dir)/state" "phase=rolled_back"
  assert_called 'systemctl stop mcpanel-panel.service'
}

test_ops_update_failing_step_stops_the_swap_and_rolls_back() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  # errexit must hold inside the swap: the line after a failing command never runs (overview V4)
  cfg_ensure() {
    false
    echo ran >"$WORK/after-false"
  }
  assert_exit 3 cmd_update
  assert_no_file "$WORK/after-false"
  assert_contains "$(cat "$WORK/out")" "installing 1.1.0 failed (exit 1)"
  # unit_ensure never ran: the only restarts are those of the rollback, after the stops
  assert_eq 1 "$(grep -c '^systemctl restart mcpanel-agent.service' "$WORK/calls.log")"
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
}

test_ops_update_rollback_failure_exits_4() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  OPS_UNHEALTHY=1
  UPD_HEALTH_SECS=0
  assert_exit 4 cmd_update
  assert_contains "$(cat "$WORK/out")" "rollback to 1.0.0 failed"
  assert_contains "$(cat "$WORK/out")" "systemctl daemon-reload"
  assert_file_contains "$(ops_only_dir)/state" "phase=rollback_failed"
  assert_called 'systemctl stop mcpanel-panel.service'
  # what the rollback could put back is back
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
}

test_ops_update_rolls_back_on_signal() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  # A TERM arrives right after the binaries were swapped.
  cfg_ensure() { kill -TERM "$BASHPID"; }
  assert_exit 3 cmd_update
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
  assert_file_contains "$(ops_only_dir)/state" "phase=rolled_back"
}

test_ops_update_rolls_back_on_interrupt_of_the_script() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  # The signal reaches the shell running cmd_update (not only the swap): the swap completes,
  # the new version is still rolled back because the operator asked to stop. TERM, not INT:
  # a test run started in the background ignores INT, and an ignored signal cannot be trapped.
  unit_ensure() { kill -TERM "$OPS_CMD_PID"; }
  ops_run() {
    OPS_CMD_PID=$BASHPID
    cmd_update
  }
  assert_exit 3 ops_run
  assert_contains "$(cat "$WORK/out")" "update interrupted"
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
}

# ops_unfinished PHASE: a previous run from 1.0.0 to 1.1.0 that stopped in PHASE; its backup
# holds 1.0.0 (binaries, units, configuration, DB). Prints the dir.
ops_unfinished() {
  local old="$ROOT$ROLLBACK_DIR/1.0.0-20261013T000000Z" b
  mkdir -p "$old/bin" "$old/units" "$old/config"
  for b in panel agent files; do
    ops_fake_bin "$old/bin/$b" "echo 1.0.0"
  done
  local u
  for u in "${UPD_UNITS[@]}"; do
    printf '# %s 1.0.0\n' "$u" >"$old/units/$u"
  done
  printf 'public_url = "https://panel.example.com"\n' >"$old/config/panel.toml"
  printf '%s 640 root mcpanel\n' "$ETC_PANEL/panel.toml" >"$old/config/files"
  echo "db 1.0.0" >"$old/panel.db"
  printf 'phase=%s\nfrom=1.0.0\nto=1.1.0\n' "$1" >"$old/state"
  echo "$old"
}

test_ops_update_check_only_reports_unfinished_without_change() {
  ops_fake_install 1.1.0
  ops_probes
  local old
  old="$(ops_unfinished applying)"
  upd_latest_tag() { echo v1.1.0; }
  CHECK_ONLY=1
  assert_exit 0 cmd_update
  assert_contains "$(cat "$WORK/out")" "did not finish"
  assert_file_contains "$old/state" "phase=applying"
  assert_eq "1.1.0" "$("$ROOT$BIN_DIR/panel" version)"
  assert_not_called 'systemctl'
}

test_ops_update_unfinished_without_change_is_marked_aborted() {
  ops_fake_install 1.0.0
  ops_probes
  local old
  old="$(ops_unfinished applying)"
  cp "$ROOT$BIN_DIR"/* "$old/bin/"
  cp "$ROOT$UNIT_DIR"/* "$old/units/"
  cp "$ROOT$ETC_PANEL/panel.toml" "$old/config/panel.toml"
  echo "db changed after the crash" >"$ROOT$DATA_DIR/panel.db"
  TARGET_VERSION=1.0.0
  assert_exit 0 cmd_update
  assert_file_contains "$old/state" "phase=aborted"
  assert_eq "db changed after the crash" "$(cat "$ROOT$DATA_DIR/panel.db")"
  assert_not_called 'systemctl'
}

test_ops_update_unfinished_but_healthy_target_is_verified() {
  ops_fake_install 1.1.0
  ops_probes
  local old
  old="$(ops_unfinished applying)"
  echo "db 1.1.0 with new data" >"$ROOT$DATA_DIR/panel.db"
  TARGET_VERSION=1.1.0
  assert_exit 0 cmd_update
  assert_file_contains "$old/state" "phase=verified"
  # the DB copy is never used without a yes
  assert_eq "db 1.1.0 with new data" "$(cat "$ROOT$DATA_DIR/panel.db")"
  assert_eq "1.1.0" "$("$ROOT$BIN_DIR/panel" version)"
}

test_ops_update_unfinished_and_unhealthy_needs_a_yes_for_the_db() {
  ops_fake_install 1.1.0
  ops_probes
  ops_fake_new_build 1.1.0
  local old
  old="$(ops_unfinished applying)"
  upd_probe_health() { [[ $("$ROOT$BIN_DIR/panel" version) == 1.0.0 ]]; }
  UPD_HEALTH_SECS=0
  TARGET_VERSION=1.1.0
  # no terminal, no --yes, no --force: nothing changes
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "database copy from"
  assert_contains "$(cat "$WORK/out")" "install.sh update --force"
  assert_file_contains "$old/state" "phase=applying"
  assert_eq "db 1.1.0" "$(cat "$ROOT$DATA_DIR/panel.db")"
  assert_not_called 'systemctl'
  # answered yes on the terminal: rolled back including the DB; then the update to 1.1.0 runs
  # again, is unhealthy again and is rolled back (exit 3)
  printf 'y\n' >"$TTY"
  assert_exit 3 cmd_update
  assert_file_contains "$old/state" "phase=rolled_back"
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
  assert_eq "db 1.0.0" "$(cat "$ROOT$DATA_DIR/panel.db")"
}

test_ops_update_unfinished_with_yes_rolls_back() {
  ops_fake_install 1.1.0
  ops_probes
  local old
  old="$(ops_unfinished applying)"
  upd_probe_health() { [[ $("$ROOT$BIN_DIR/panel" version) == 1.0.0 ]]; }
  UPD_HEALTH_SECS=0
  upd_latest_tag() { echo v1.0.0; }
  YES=1
  assert_exit 0 cmd_update
  assert_file_contains "$old/state" "phase=rolled_back"
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
  assert_eq "db 1.0.0" "$(cat "$ROOT$DATA_DIR/panel.db")"
  assert_file_contains "$ROOT$UNIT_DIR/mcpanel-panel.service" "1.0.0"
}

test_ops_update_unfinished_rollback_is_finished_without_asking() {
  ops_fake_install 1.1.0
  ops_probes
  local old
  old="$(ops_unfinished rolling_back)"
  upd_latest_tag() { echo v1.0.0; }
  assert_exit 0 cmd_update
  assert_file_contains "$old/state" "phase=rolled_back"
  assert_eq "db 1.0.0" "$(cat "$ROOT$DATA_DIR/panel.db")"
}

test_ops_update_unfinished_rollback_failure_exits_4() {
  ops_fake_install 1.1.0
  ops_probes
  OPS_UNHEALTHY=1
  UPD_HEALTH_SECS=0
  local old
  old="$(ops_unfinished applying)"
  TARGET_VERSION=1.1.0
  FORCE=1
  assert_exit 4 cmd_update
  assert_file_contains "$old/state" "phase=rollback_failed"
}

test_ops_update_refuses_without_space() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  OPS_FREE=10
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "not enough free space"
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
  assert_not_called 'systemctl'
  assert_eq "" "$(ops_only_dir)"
}

test_ops_update_refuses_when_db_backup_fails() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  upd_db_backup() { return 1; }
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "database backup failed"
  assert_eq "" "$(ops_only_dir)"
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
  assert_not_called 'systemctl'
}

test_ops_update_db_backup_and_restore_run_as_mcpanel() {
  # dry-run: run_as only records; the backup then finds no file of mcpanel and fails safely
  mkdir -p "$ROOT$DATA_DIR"
  assert_fails upd_db_backup "$WORK/copy.db"
  assert_called "runuser -u mcpanel -- sqlite3 -bail -cmd .timeout 10000 /var/lib/mc-panel/panel.db .backup '/var/lib/mc-panel/.rollback-"
  assert_called "runuser -u mcpanel -- rm -f -- /var/lib/mc-panel/.rollback-"
  echo db >"$WORK/copy.db"
  upd_db_restore "$WORK/copy.db"
  # shellcheck disable=SC2016 # the literal script of the inner sh
  assert_called 'runuser -u mcpanel -- sh -c umask 077 && cat >"$1.restore" && sync -- "$1.restore" &&'
  # the WAL goes only after the complete copy is on disk, right before the rename
  # shellcheck disable=SC2016 # the literal script of the inner sh
  assert_called 'rm -f -- "$1-wal" "$1-shm" && mv -f -- "$1.restore" "$1" && sync -- "$1" sh /var/lib/mc-panel/panel.db'
  # root itself never created anything below DATA_DIR
  assert_eq "" "$(find "$ROOT$DATA_DIR" -mindepth 1)"
}

test_ops_update_refuses_symlinked_rollback_dir() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  mkdir -p "$ROOT/elsewhere"
  ln -s "$ROOT/elsewhere" "$ROOT$ROLLBACK_DIR"
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  assert_exit 1 cmd_update
  assert_eq "" "$(find "$ROOT/elsewhere" -mindepth 1)"
}

test_ops_update_keeps_three_backups() {
  ops_fake_install 1.0.0
  ops_probes
  local i d
  for i in 1 2 3 4; do
    d="$ROOT$ROLLBACK_DIR/0.$i.0-2026101${i}T000000Z"
    mkdir -p "$d"
    printf 'phase=verified\n' >"$d/state"
    touch -d "2026-10-1$i" "$d"
  done
  d="$ROOT$ROLLBACK_DIR/0.0.9-20261001T000000Z"
  mkdir -p "$d"
  printf 'phase=rollback_failed\n' >"$d/state"
  touch -d "2026-10-01" "$d"
  upd_prune >/dev/null
  assert_no_file "$ROOT$ROLLBACK_DIR/0.1.0-20261011T000000Z"
  assert_file "$ROOT$ROLLBACK_DIR/0.2.0-20261012T000000Z"
  assert_file "$ROOT$ROLLBACK_DIR/0.3.0-20261013T000000Z"
  assert_file "$ROOT$ROLLBACK_DIR/0.4.0-20261014T000000Z"
  # A failed rollback is never pruned: it is the only copy of the old state.
  assert_file "$ROOT$ROLLBACK_DIR/0.0.9-20261001T000000Z"
}

test_ops_update_latest_tag_parses_the_api() {
  upd_fetch_latest_json() { printf '{\n  "url": "x",\n  "tag_name": "v1.4.2",\n  "name": "v1.4.2"\n}\n' >"$1"; }
  assert_eq v1.4.2 "$(upd_latest_tag)"
}

test_ops_update_rejects_unexpected_tag() {
  upd_fetch_latest_json() { printf '{"tag_name": "v1.2.3; rm -rf /"}' >"$1"; }
  assert_exit 1 upd_latest_tag
}

test_ops_update_unfinished_dirs_lists_applying_and_failed() {
  mkdir -p "$ROOT$ROLLBACK_DIR/a" "$ROOT$ROLLBACK_DIR/b" "$ROOT$ROLLBACK_DIR/c"
  printf 'phase=applying\n' >"$ROOT$ROLLBACK_DIR/a/state"
  printf 'phase=verified\n' >"$ROOT$ROLLBACK_DIR/b/state"
  printf 'phase=rollback_failed\n' >"$ROOT$ROLLBACK_DIR/c/state"
  assert_eq "$ROOT$ROLLBACK_DIR/a $ROOT$ROLLBACK_DIR/c" "$(upd_unfinished_dirs | paste -sd' ' -)"
}

test_ops_update_rollback_restores_the_configuration() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  printf 'public_url = "https://panel.example.com"\nold_key = 1\n' >"$ROOT$ETC_PANEL/panel.toml"
  # the new version adds a key and changes the mode; then it is not healthy
  cfg_ensure() {
    printf 'new_key = 2\n' >>"$ROOT$ETC_PANEL/panel.toml"
    chmod 0644 "$ROOT$ETC_PANEL/panel.toml"
  }
  upd_probe_health() { [[ $("$ROOT$BIN_DIR/panel" version) == 1.0.0 ]]; }
  UPD_HEALTH_SECS=0
  assert_exit 3 cmd_update
  assert_eq "$(printf 'public_url = "https://panel.example.com"\nold_key = 1')" "$(cat "$ROOT$ETC_PANEL/panel.toml")"
  assert_eq 640 "$(stat -c '%a' "$ROOT$ETC_PANEL/panel.toml")"
  assert_file "$(ops_only_dir)/config/panel.toml"
  assert_file_contains "$(ops_only_dir)/config/files" "/etc/mc-panel/panel.toml 640 "
}

test_ops_update_failed_db_restore_is_a_failed_rollback() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  upd_probe_health() { [[ $("$ROOT$BIN_DIR/panel" version) == 1.0.0 ]]; }
  UPD_HEALTH_SECS=0
  upd_db_restore() { return 1; }
  assert_exit 4 cmd_update
  assert_contains "$(cat "$WORK/out")" "could not put the database back"
  assert_file_contains "$(ops_only_dir)/state" "phase=rollback_failed"
  # the services stay stopped: no restart after the stops of the rollback
  assert_eq "systemctl stop mcpanel-panel.socket" "$(grep -E '^systemctl (stop|restart)' "$WORK/calls.log" | tail -n 5 | head -n 1)"
}

test_ops_update_rollback_ignores_a_second_signal() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  upd_probe_health() { [[ $("$ROOT$BIN_DIR/panel" version) == 1.0.0 ]]; }
  UPD_HEALTH_SECS=0
  # a TERM in the middle of the rollback (while the DB goes back) changes nothing
  upd_db_restore() {
    kill -TERM "$OPS_CMD_PID"
    cp -- "$1" "$ROOT$DATA_DIR/panel.db"
  }
  ops_run() {
    OPS_CMD_PID=$BASHPID
    cmd_update
  }
  assert_exit 3 ops_run
  assert_file_contains "$(ops_only_dir)/state" "phase=rolled_back"
  assert_eq "db 1.0.0" "$(cat "$ROOT$DATA_DIR/panel.db")"
}

test_ops_update_interrupt_during_backup_changes_nothing() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  upd_db_backup() {
    kill -TERM "$OPS_CMD_PID"
    cp -- "$ROOT$DATA_DIR/panel.db" "$1"
  }
  ops_run() {
    OPS_CMD_PID=$BASHPID
    cmd_update
  }
  assert_exit 1 ops_run
  assert_contains "$(cat "$WORK/out")" "interrupted during the backup"
  assert_eq "" "$(ops_only_dir)"
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
  assert_not_called 'systemctl'
}

test_ops_update_checks_the_release_before_the_backup() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  ops_fake_bin "$WORK/build/agent" "echo 1.0.9"
  TARGET_VERSION=1.1.0
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "report 1.1.0 1.0.9 1.1.0"
  assert_no_file "$ROOT$ROLLBACK_DIR"
  assert_not_called 'systemctl'
}

test_ops_update_source_failure_changes_nothing() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  bin_source() { die "download of mc-panel_1.1.0_linux_amd64.tar.gz failed"; }
  assert_exit 1 cmd_update
  assert_contains "$(cat "$WORK/out")" "cannot get release 1.1.0"
  assert_no_file "$ROOT$ROLLBACK_DIR"
}

test_ops_update_restarting_service_is_not_healthy() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  UPD_HEALTH_SECS=0
  # the new panel crashes and is restarted by systemd
  upd_probe_restarts() {
    if [[ $1 == mcpanel-panel.service && $("$ROOT$BIN_DIR/panel" version) == 1.1.0 ]]; then echo 2; else echo 0; fi
  }
  assert_exit 3 cmd_update
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
  assert_contains "$(cat "$WORK/out")" "1.1.0 is not healthy after 0s: systemd restarted mcpanel-panel.service 2 times"
}

test_ops_update_checks_the_socket_units() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  UPD_HEALTH_SECS=0
  systemd_wait_active() { [[ $1 != mcpanel-panel.socket || $("$ROOT$BIN_DIR/panel" version) == 1.0.0 ]]; }
  upd_probe_unit_state() { echo "failed/failed (exit-code)"; }
  assert_exit 3 cmd_update
  assert_contains "$(cat "$WORK/out")" "not healthy after 0s: mcpanel-panel.socket is failed/failed (exit-code)"
}

test_ops_update_names_the_failing_endpoint_and_logs_the_units() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  : >"$ROOT$LOG_FILE"
  UPD_HEALTH_SECS=0
  upd_probe_health() {
    if [[ $("$ROOT$BIN_DIR/panel" version) == 1.0.0 ]]; then return 0; fi
    echo "panel: curl: (56) Recv failure: Connection reset by peer"
    return 1
  }
  assert_exit 3 cmd_update
  assert_contains "$(cat "$WORK/out")" "1.1.0 is not healthy after 0s: health check failed: panel: curl: (56) Recv failure"
  assert_file_contains "$ROOT$LOG_FILE" "not healthy after 0s: health check failed: panel"
  assert_file_contains "$ROOT$LOG_FILE" "unit report since"
}

test_ops_update_failed_rollback_names_the_reason() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  UPD_HEALTH_SECS=0
  upd_probe_restarts() { [[ $1 == mcpanel-agent.service ]] && echo 1 || echo 0; }
  assert_exit 4 cmd_update
  assert_contains "$(cat "$WORK/out")" "1.0.0 is not healthy after 0s: systemd restarted mcpanel-agent.service 1 times"
}

test_ops_update_from_dir_hands_over_to_its_install_sh() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  printf '#!/bin/bash\n' >"$WORK/build/install.sh"
  TARGET_VERSION=1.1.0
  FORCE=1
  upd_exec() { echo "exec $* handoff=$MCPANEL_UPDATE_HANDOFF" >>"$WORK/calls.log"; }
  assert_exit 0 cmd_update
  assert_called "exec bash $WORK/build/install.sh update --from-dir $WORK/build --force handoff=1"
  assert_no_file "$ROOT$ROLLBACK_DIR"
}

test_ops_update_prune_removes_incomplete_backups() {
  mkdir -p "$ROOT$ROLLBACK_DIR/1.0.0-20261010T000000Z" "$ROOT$ROLLBACK_DIR/1.0.0-20261011T000000Z"
  printf 'phase=verified\n' >"$ROOT$ROLLBACK_DIR/1.0.0-20261011T000000Z/state"
  upd_prune >/dev/null
  assert_no_file "$ROOT$ROLLBACK_DIR/1.0.0-20261010T000000Z"
  assert_file "$ROOT$ROLLBACK_DIR/1.0.0-20261011T000000Z"
}

test_ops_update_success_brings_caddy_and_upgrades_up_to_date() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  assert_exit 0 cmd_update
  # after the swap, in this order, with the installed domain
  assert_eq "pkg_caddy_ensure|cad_ensure panel.example.com letsencrypt|upg_ensure" \
    "$(sed -n 's/^step //p' "$WORK/calls.log" | paste -sd'|' -)"
}

test_ops_update_failing_system_step_only_warns() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  pkg_caddy_ensure() { die "download failed: https://dl.cloudsmith.io/public/caddy/stable/gpg.key"; }
  assert_exit 0 cmd_update
  assert_contains "$(cat "$WORK/out")" "[warn] pkg_caddy_ensure failed (exit 1)"
  assert_called 'step cad_ensure'
  assert_called 'step upg_ensure'
  assert_file_contains "$(ops_only_dir)/state" "phase=verified"
}

test_ops_update_rollback_skips_the_system_steps() {
  ops_fake_install 1.0.0
  ops_probes
  ops_fake_new_build 1.1.0
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
  upd_probe_health() { [[ $("$ROOT$BIN_DIR/panel" version) == 1.0.0 ]]; }
  UPD_HEALTH_SECS=0
  assert_exit 3 cmd_update
  assert_not_called 'step '
}
