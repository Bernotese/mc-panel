# shellcheck shell=bash
# Tests of doctor.sh (installer plan 03, task 9). Run: bash scripts/install/test.sh tests/ops_doctor.sh
# Tests override functions (called by the code under test) and set globals (read by it), which
# ShellCheck cannot see from this file (SC2317 is the code of ShellCheck 0.9, SC2329 that of
# newer versions):
# shellcheck disable=SC2034,SC2317,SC2329

# ops_doctor_healthy sets every probe to a healthy system and writes the configuration.
# OPS_STAT_PATH/OPS_STAT_VALUE override doc_probe_stat for one path.
ops_doctor_healthy() {
  doc_probe_euid() { echo 0; }
  doc_probe_unit_active() { return 0; }
  doc_probe_stat() {
    if [[ $1 == "${OPS_STAT_PATH:-}" ]]; then
      echo "$OPS_STAT_VALUE"
      return 0
    fi
    case "$1" in
    /run/mc-panel/agent.sock) echo "root:mcpanel 660" ;;
    /run/mc-panel-files/files.sock) echo "mcpanel-mc:mcpanel 660" ;;
    /run/mc-panel-web/panel.sock) echo "mcpanel:caddy 660" ;;
    /etc/mc-panel) echo "root:mcpanel 750" ;;
    /etc/mc-panel-files) echo "root:mcpanel-mc 750" ;;
    /etc/mc-panel/panel.toml) echo "root:mcpanel 640" ;;
    /etc/mc-panel/agent.toml) echo "root:root 600" ;;
    /etc/mc-panel-files/files.toml) echo "root:mcpanel-mc 640" ;;
    /var/lib/mc-panel) echo "mcpanel:mcpanel 700" ;;
    /srv/mc-panel) echo "root:root 755" ;;
    /srv/mc-panel/instances) echo "root:mcpanel-mc 750" ;;
    /srv/mc-panel/backups) echo "mcpanel-mc:mcpanel-mc 700" ;;
    *) echo missing ;;
    esac
  }
  doc_probe_files_creds() { echo ok; }
  upd_binary_versions() { echo "1.2.0 1.2.0 1.2.0"; }
  upd_latest_tag() { echo v1.2.0; }
  doc_probe_caddy_validate() { return 0; }
  doc_probe_cert_days() { echo 60; }
  doc_probe_resolve() { echo 203.0.113.5; }
  doc_probe_local_addrs() { printf '127.0.0.1\n203.0.113.5\n'; }
  doc_probe_nft_table() { printf 'table inet mcpanel_fw {\n\tchain input {\n\t\ttcp dport { 22, 80, 443, 25565 } accept\n\t}\n}\n'; }
  doc_probe_ssh_port() { echo 22; }
  doc_probe_docker() { return 0; }
  doc_probe_restic_version() { echo 0.18.1; }
  doc_probe_pkg() { return 0; }
  doc_probe_ntp() { echo yes; }
  doc_probe_unit_enabled() { return 0; }
  doc_probe_mountpoint() { echo /srv/mc-panel; }
  doc_probe_free_percent() { echo 40; }
  doc_probe_sshd() { printf 'port 22\npasswordauthentication no\npermitrootlogin prohibit-password\n'; }
  mkdir -p "$ROOT$ETC_PANEL" "$ROOT$ETC_FILES" "$ROOT$INSTANCES_DIR/backups/keys" "$ROOT$CADDY_DIR"
  printf '%s\n' 'listen = "unix:/run/mc-panel-web/panel.sock"' 'public_url = "https://panel.example.com"' \
    'data_dir = "/var/lib/mc-panel"' 'agent_socket = "/run/mc-panel/agent.sock"' \
    'files_socket = "/run/mc-panel-files/files.sock"' 'trusted_proxies = ["127.0.0.1/32", "::1/128"]' \
    'monitoring_ping_addr = "127.0.0.1:25565"' 'instances_dir = "/srv/mc-panel"' >"$ROOT$ETC_PANEL/panel.toml"
  printf '%s\n' 'socket = "/run/mc-panel/agent.sock"' 'panel_uid = 998' 'firewall = "nftables"' \
    'instances_dir = "/srv/mc-panel"' 'mc_user = "mcpanel-mc"' >"$ROOT$ETC_PANEL/agent.toml"
  printf '%s\n' 'socket = "/run/mc-panel-files/files.sock"' 'panel_uid = 998' 'instances_dir = "/srv/mc-panel"' \
    'restic_binary = "/usr/bin/restic"' >"$ROOT$ETC_FILES/files.toml"
  printf 'panel.example.com {\n\ttls you@example.com\n}\n' >"$ROOT$CADDY_DIR/mc-panel.caddy"
  printf ':80 {\n}\nimport mc-panel.caddy # managed by mc-panel\n' >"$ROOT$CADDY_DIR/Caddyfile"
}

test_ops_doctor_required_keys_match_install() {
  # doctor checks exactly the keys install adds (config.sh CFG_REQUIRED_*)
  if ! declare -F cfg_keys >/dev/null; then
    echo "# skipped: cfg_keys (config.sh, phase B1) is not merged yet"
    return 0
  fi
  stub id 'echo 998'
  DOMAIN=panel.example.com TLS_MODE=internal PKG_RESTIC_BIN=/usr/bin/restic
  cfg_keys
  # shellcheck disable=SC2086 # the DOC_REQ_* lists are blank-separated on purpose
  {
    assert_eq "$(printf '%s\n' "${!CFG_REQUIRED_PANEL[@]}" | sort | xargs)" "$(printf '%s\n' $DOC_REQ_PANEL | sort | xargs)"
    assert_eq "$(printf '%s\n' "${!CFG_REQUIRED_AGENT[@]}" | sort | xargs)" "$(printf '%s\n' $DOC_REQ_AGENT | sort | xargs)"
    assert_eq "$(printf '%s\n' "${!CFG_REQUIRED_FILES[@]}" | sort | xargs)" "$(printf '%s\n' $DOC_REQ_FILES | sort | xargs)"
  }
}

test_ops_doctor_required_keys_match_templates() {
  # every required key is in the template that install writes for a new system
  local k
  for k in $DOC_REQ_PANEL; do doc_toml_has_key "$TEMPLATE_DIR/config/panel.toml" "$k" || fail "panel.toml template lacks $k"; done
  for k in $DOC_REQ_AGENT; do doc_toml_has_key "$TEMPLATE_DIR/config/agent.toml" "$k" || fail "agent.toml template lacks $k"; done
  for k in $DOC_REQ_FILES; do doc_toml_has_key "$TEMPLATE_DIR/config/files.toml" "$k" || fail "files.toml template lacks $k"; done
}

test_ops_doctor_healthy_system_is_ok() {
  ops_doctor_healthy
  assert_exit 0 cmd_doctor
  local out
  out="$(cat "$WORK/out")"
  assert_contains "$out" "✓ all mc-panel services are running"
  assert_contains "$out" "23 checks: 0 failed, 0 warnings"
  assert_not_contains "$out" "✗"
  assert_not_called 'systemctl'
}

test_ops_doctor_reports_missing_instances_dir_key() {
  ops_doctor_healthy
  grep -v '^instances_dir' "$ROOT$ETC_PANEL/panel.toml" >"$WORK/p" && mv "$WORK/p" "$ROOT$ETC_PANEL/panel.toml"
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "/etc/mc-panel/panel.toml: instances_dir"
}

test_ops_doctor_key_in_a_section_does_not_count() {
  ops_doctor_healthy
  grep -v '^mc_user' "$ROOT$ETC_PANEL/agent.toml" >"$WORK/a"
  printf '[extra]\nmc_user = "x"\n' >>"$WORK/a"
  mv "$WORK/a" "$ROOT$ETC_PANEL/agent.toml"
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "agent.toml: mc_user"
}

test_ops_doctor_fails_on_missing_config_key_with_hint() {
  ops_doctor_healthy
  sed -i '/public_url/d' "$ROOT$ETC_PANEL/panel.toml"
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "public_url"
  assert_contains "$(cat "$WORK/out")" "-> install.sh install"
}

test_ops_doctor_warns_on_shared_filesystem_and_ssh() {
  ops_doctor_healthy
  doc_probe_mountpoint() { echo /; }
  doc_probe_sshd() { printf 'passwordauthentication yes\npermitrootlogin yes\n'; }
  assert_exit 1 cmd_doctor
  local out
  out="$(cat "$WORK/out")"
  assert_contains "$out" "! /srv/mc-panel is not its own file system"
  assert_contains "$out" "PasswordAuthentication"
  assert_contains "$out" "PermitRootLogin"
}

test_ops_doctor_warns_on_low_free_space() {
  ops_doctor_healthy
  doc_probe_free_percent() { echo 7; }
  assert_exit 1 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "only 7 % free"
}

test_ops_doctor_fails_on_stopped_service() {
  ops_doctor_healthy
  doc_probe_unit_active() { [[ $1 != mcpanel-panel.service ]]; }
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "not running: mcpanel-panel.service"
  assert_contains "$(cat "$WORK/out")" "systemctl status mcpanel-panel.service"
}

test_ops_doctor_fails_on_socket_mode() {
  ops_doctor_healthy
  OPS_STAT_PATH=/run/mc-panel-files/files.sock
  OPS_STAT_VALUE="mcpanel-mc:mcpanel 666"
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "/run/mc-panel-files/files.sock is mcpanel-mc:mcpanel 666 (expected mcpanel-mc:mcpanel 660)"
}

test_ops_doctor_fails_on_wrong_permissions() {
  ops_doctor_healthy
  OPS_STAT_PATH=/etc/mc-panel/agent.toml
  OPS_STAT_VALUE="root:mcpanel 640"
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "/etc/mc-panel/agent.toml is root:mcpanel 640 (expected root:root 600)"
}

test_ops_doctor_fails_on_files_process() {
  ops_doctor_healthy
  doc_probe_files_creds() { echo "has capabilities"; }
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "✗ files service has capabilities"
}

test_ops_doctor_files_creds_parses_proc_status() {
  systemctl() { echo 4242; }
  id() { echo 997; }
  getent() { echo "mcpanel:x:998:"; }
  cat() { printf 'Name:\tfiles\nUid:\t997\t997\t997\t997\nGroups:\t997\nCapEff:\t0000000000000000\nNoNewPrivs:\t1\n'; }
  assert_eq ok "$(doc_probe_files_creds)"
  cat() { printf 'Uid:\t997\t997\t997\t997\nGroups:\t997 998\nCapEff:\t0000000000000000\nNoNewPrivs:\t1\n'; }
  assert_eq "is a member of the group mcpanel" "$(doc_probe_files_creds)"
  cat() { printf 'Uid:\t0\t0\t0\t0\nCapEff:\t000001ffffffffff\nNoNewPrivs:\t0\n'; }
  assert_eq "does not run as mcpanel-mc" "$(doc_probe_files_creds)"
}

test_ops_doctor_reports_version_mismatch_and_newer_release() {
  ops_doctor_healthy
  upd_binary_versions() { echo "1.2.0 1.1.0 1.2.0"; }
  upd_latest_tag() { echo v1.3.0; }
  assert_exit 2 cmd_doctor
  local out
  out="$(cat "$WORK/out")"
  assert_contains "$out" "versions differ: panel 1.2.0, agent 1.1.0, files 1.2.0"
  assert_contains "$out" "! release 1.3.0 is available"
}

test_ops_doctor_offline_only_warns() {
  ops_doctor_healthy
  upd_latest_tag() { return 1; }
  assert_exit 1 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "could not check for a newer release"
}

test_ops_doctor_certificate_thresholds() {
  ops_doctor_healthy
  doc_probe_cert_days() { echo 10; }
  assert_exit 1 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "expires in 10 days"
  doc_probe_cert_days() { echo -1; }
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "has expired"
  doc_probe_cert_days() { :; }
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "no certificate for panel.example.com"
}

test_ops_doctor_internal_tls_needs_no_certificate() {
  ops_doctor_healthy
  printf 'panel.example.com {\n\ttls internal\n}\n' >"$ROOT$CADDY_DIR/mc-panel.caddy"
  doc_probe_cert_days() { :; }
  assert_exit 0 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "tls internal"
}

test_ops_doctor_dns_elsewhere_warns() {
  ops_doctor_healthy
  doc_probe_resolve() { echo 198.51.100.7; }
  assert_exit 1 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "panel.example.com resolves to 198.51.100.7"
}

test_ops_doctor_missing_import_line_fails() {
  ops_doctor_healthy
  printf ':80 {\n}\nimport mc-panel.caddy\n' >"$ROOT$CADDY_DIR/Caddyfile"
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "not imported"
}

test_ops_doctor_firewall_without_ssh_port_fails() {
  ops_doctor_healthy
  doc_probe_ssh_port() { echo 2222; }
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "does not allow the SSH port 2222"
}

test_ops_doctor_firewall_all_ssh_ports_needed() {
  ops_doctor_healthy
  doc_probe_ssh_port() { printf '22\n2200\n'; }
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "SSH port 2200"
}

test_ops_doctor_without_firewall_warns() {
  ops_doctor_healthy
  doc_probe_nft_table() { :; }
  assert_exit 1 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "! host firewall (table inet mcpanel_fw) is not loaded"
}

test_ops_doctor_system_checks_fail() {
  ops_doctor_healthy
  doc_probe_restic_version() { echo 0.16.4; }
  doc_probe_pkg() { [[ $1 != tzdata ]]; }
  doc_probe_ntp() { echo no; }
  doc_probe_unit_enabled() { return 1; }
  assert_exit 2 cmd_doctor
  local out
  out="$(cat "$WORK/out")"
  assert_contains "$out" "✗ restic 0.16.4"
  assert_contains "$out" "✗ tzdata is missing"
  assert_contains "$out" "! the system clock is not synchronized"
  assert_contains "$out" "! unattended-upgrades is not enabled"
}

test_ops_doctor_unfinished_update_fails() {
  ops_doctor_healthy
  mkdir -p "$ROOT$ROLLBACK_DIR/1.0.0-20261013T000000Z"
  printf 'phase=applying\nfrom=1.0.0\nto=1.1.0\n' >"$ROOT$ROLLBACK_DIR/1.0.0-20261013T000000Z/state"
  assert_exit 2 cmd_doctor
  assert_contains "$(cat "$WORK/out")" "1.0.0-20261013T000000Z"
  assert_contains "$(cat "$WORK/out")" "install.sh update"
}

test_ops_doctor_without_root_skips_root_checks() {
  ops_doctor_healthy
  doc_probe_euid() { echo 1000; }
  doc_probe_files_creds() { fail "files_process must not run without root"; }
  doc_probe_sshd() { fail "sshd -T must not run without root"; }
  assert_exit 1 cmd_doctor
  local out
  out="$(cat "$WORK/out")"
  assert_contains "$out" "! files_process: skipped (needs root)"
  assert_contains "$out" "! ssh_root: skipped (needs root)"
  assert_contains "$out" "✓ all mc-panel services are running"
  assert_not_contains "$out" "✗"
}

test_ops_doctor_json_is_valid_and_complete() {
  ops_doctor_healthy
  doc_probe_sshd() { printf 'passwordauthentication yes\n'; }
  JSON=1
  assert_exit 1 cmd_doctor
  local out
  out="$(cat "$WORK/out")"
  assert_eq '{"status":"warn","checks":[{"id":"services","status":"ok"' "${out:0:57}"
  assert_contains "$out" '{"id":"ssh_password","status":"warn","message":"SSH allows password logins (PasswordAuthentication yes)","hint":"use SSH keys and set PasswordAuthentication no"}'
  assert_not_contains "$out" "==>"
  local id
  for id in services sockets files_process versions update_available update_state config permissions \
    caddy_config caddy_import certificate dns firewall docker restic tzdata time_sync upgrades \
    storage_fs storage_free backup_keys ssh_password ssh_root; do
    assert_contains "$out" "\"id\":\"$id\""
  done
  if command -v python3 >/dev/null; then
    printf '%s' "$out" | python3 -I -c '
import json, sys
d = json.load(sys.stdin)
assert d["status"] == "warn"
assert len(d["checks"]) == 23
for c in d["checks"]:
    assert set(c) == {"id", "status", "message", "hint"}, c
    assert c["status"] in ("ok", "warn", "fail"), c
' || fail "invalid JSON: $out"
  fi
}

test_ops_doctor_json_escapes_messages() {
  assert_eq '"a \"quoted\"\\ path\nnext\tcol"' "$(doc_json_str "a \"quoted\"\\ path"$'\n'"next"$'\t'"col")"
}

test_ops_doctor_summary_prints_only_problems() {
  ops_doctor_healthy
  doc_probe_docker() { return 1; }
  DOC_SUMMARY=1
  assert_exit 2 cmd_doctor
  local out
  out="$(cat "$WORK/out")"
  assert_contains "$out" "✗ Docker is not reachable"
  assert_not_contains "$out" "✓"
}

test_ops_doctor_domain_from_public_url() {
  mkdir -p "$ROOT$ETC_PANEL"
  printf 'listen = "x"\npublic_url = "https://panel.example.com:8443/x" # comment\n' >"$ROOT$ETC_PANEL/panel.toml"
  assert_eq panel.example.com "$(doc_domain)"
  rm "$ROOT$ETC_PANEL/panel.toml"
  assert_eq "" "$(doc_domain)"
}
