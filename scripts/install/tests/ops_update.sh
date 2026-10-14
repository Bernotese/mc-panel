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
  printf 'panel.example.com {\n\ttls you@example.com\n}\n' >"$ROOT$CADDY_DIR/mc-panel.caddy"
}

# ops_fake_new_build VERSION makes bin_install/cfg_ensure/unit_ensure install VERSION (they stand
# in for B1's steps).
ops_fake_new_build() {
  OPS_NEW_VERSION="$1"
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
  systemd_wait_active() { [[ -z ${OPS_UNHEALTHY:-} ]]; }
  sleep() { :; }
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

test_ops_update_finishes_unfinished_first() {
  ops_fake_install 1.1.0
  ops_probes
  # A previous run died in phase applying: its backup holds 1.0.0.
  local old="$ROOT$ROLLBACK_DIR/1.0.0-20261013T000000Z"
  mkdir -p "$old/bin" "$old/units"
  ops_fake_bin "$old/bin/panel" "echo 1.0.0"
  ops_fake_bin "$old/bin/agent" "echo 1.0.0"
  ops_fake_bin "$old/bin/files" "echo 1.0.0"
  cp "$ROOT$UNIT_DIR"/* "$old/units/"
  echo "db 1.0.0" >"$old/panel.db"
  printf 'phase=applying\nfrom=1.0.0\nto=1.1.0\n' >"$old/state"
  upd_latest_tag() { echo v1.0.0; }
  CHECK_ONLY=1
  assert_exit 0 cmd_update
  assert_file_contains "$old/state" "phase=rolled_back"
  assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
  assert_eq "db 1.0.0" "$(cat "$ROOT$DATA_DIR/panel.db")"
}

test_ops_update_unfinished_rollback_failure_exits_4() {
  ops_fake_install 1.1.0
  ops_probes
  OPS_UNHEALTHY=1
  UPD_HEALTH_SECS=0
  local old="$ROOT$ROLLBACK_DIR/1.0.0-20261013T000000Z"
  mkdir -p "$old/bin"
  printf 'phase=applying\nfrom=1.0.0\nto=1.1.0\n' >"$old/state"
  TARGET_VERSION=1.1.0
  FROM_DIR="$WORK/build"
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
  assert_called 'runuser -u mcpanel -- rm -f -- /var/lib/mc-panel/panel.db-wal /var/lib/mc-panel/panel.db-shm'
  assert_called 'runuser -u mcpanel -- sh -c umask 077'
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
