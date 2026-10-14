# shellcheck shell=bash
# Tests of upgrades.sh (installer spec §4.9). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329

test_install_upg_ensure_writes_both_files_and_enables() {
  local out
  out="$(upg_ensure)"
  assert_contains "$out" "[changed]"
  assert_file_contains "$ROOT/etc/apt/apt.conf.d/21mc-panel-auto-upgrades" 'APT::Periodic::Unattended-Upgrade "1";'
  assert_file_contains "$ROOT/etc/apt/apt.conf.d/52mc-panel-unattended-upgrades" 'Unattended-Upgrade::Automatic-Reboot "false";'
  assert_eq 644 "$(stat -c '%a' "$ROOT/etc/apt/apt.conf.d/52mc-panel-unattended-upgrades")"
  assert_called 'systemctl enable --now unattended-upgrades.service'
}

test_install_upg_ensure_second_run_unchanged() {
  upg_ensure >/dev/null
  local out
  out="$(upg_ensure)"
  assert_contains "$out" "unchanged"
  assert_not_contains "$out" "[changed]"
}

test_install_upg_ensure_keeps_other_apt_files() {
  mkdir -p "$ROOT/etc/apt/apt.conf.d"
  printf 'APT::Periodic::Unattended-Upgrade "0";\n' >"$ROOT/etc/apt/apt.conf.d/20auto-upgrades"
  upg_ensure >/dev/null
  assert_file_eq "$ROOT/etc/apt/apt.conf.d/20auto-upgrades" 'APT::Periodic::Unattended-Upgrade "0";'
}
