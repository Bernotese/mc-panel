# shellcheck shell=bash
# Tests of firewall.sh (installer spec §4.8, review focus 1: never lock out a running SSH
# session). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329

# fw_base: no established sshd connections, no SSH session, the real templates.
fw_base() {
  stub ss ':'
  unset SSH_CONNECTION # the tests themselves may run over SSH
  DOMAIN=panel.example.com TLS_MODE=letsencrypt EMAIL=a@example.com NO_FIREWALL=0 SSH_PORT=''
  mkdir -p "$ROOT$ETC_PANEL"
}

fw_fixture() {
  fw_base
  render_template() { printf 'table inet mcpanel_fw {}\ndelete table inet mcpanel_fw\ntable inet mcpanel_fw { chain input { tcp dport { %s } accept } }\n' "$KEY_SSH_PORT"; }
}

test_install_fw_ssh_ports_reads_all_ports() {
  SSH_PORT=''
  sshd() { printf 'port 2222\nport 22\naddressfamily any\n'; }
  assert_eq "22 2222" "$(fw_ssh_ports)"
}

test_install_fw_ssh_ports_prefers_argument() {
  SSH_PORT=2200
  sshd() { echo "port 22"; }
  assert_eq 2200 "$(fw_ssh_ports)"
}

test_install_fw_session_port_ipv4_and_ipv6() {
  SSH_CONNECTION="198.51.100.4 50122 203.0.113.7 2222"
  assert_eq 2222 "$(fw_session_port)"
  SSH_CONNECTION="2001:db8::4 50122 2001:db8::7 22"
  assert_eq 22 "$(fw_session_port)"
  unset SSH_CONNECTION
  assert_eq '' "$(fw_session_port)"
}

test_install_fw_live_ssh_ports_reads_ss() {
  # shellcheck disable=SC2016 # the body is code for the stub
  stub ss 'printf "%s\n" "0 0 203.0.113.7:2222 198.51.100.4:50122 users:((\"sshd-session\",pid=9,fd=4))" "0 0 [2001:db8::7]:22 [2001:db8::4]:50 users:((\"sshd\",pid=7,fd=4))" "0 0 203.0.113.7:443 198.51.100.4:1 users:((\"caddy\",pid=5,fd=9))"'
  assert_eq "2222 22" "$(fw_live_ssh_ports | xargs)"
}

test_install_fw_ensure_loads_rules_with_ssh_ports() {
  fw_fixture
  sshd() { printf 'port 22\nport 2222\n'; }
  SSH_CONNECTION="198.51.100.4 50122 203.0.113.7 2222"
  local out
  out="$(fw_ensure)"
  assert_contains "$out" "[changed] allowed: SSH 22, 2222"
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" 'tcp dport { 22, 2222 } accept'
  assert_eq 644 "$(stat -c '%a' "$ROOT$ETC_PANEL/mcpanel_fw.nft")"
  assert_called "nft -c -f $WORK/mcpanel_fw.nft"
  assert_called "nft -c -f $ROOT$ETC_PANEL/mcpanel_fw.nft"
  assert_called 'systemctl enable mcpanel-fw.service'
  assert_called 'systemctl restart mcpanel-fw.service'
}

test_install_fw_ensure_renders_the_real_template() {
  fw_base
  KEY_VERSION=1.2.3
  sshd() { echo "port 22"; }
  fw_ensure >/dev/null
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" 'tcp dport { 22 } accept'
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" 'ct state established,related accept'
  assert_eq 'table inet mcpanel_fw {}' "$(grep -v '^#' "$ROOT$ETC_PANEL/mcpanel_fw.nft" | head -n 1)"
}

test_install_fw_ensure_aborts_when_session_port_not_allowed() {
  fw_fixture
  sshd() { echo "port 22"; }
  SSH_CONNECTION="198.51.100.4 50122 203.0.113.7 2222"
  assert_fails fw_ensure
  assert_contains "$(cat "$WORK/out")" "port 2222"
  assert_not_called 'nft'
  assert_not_called 'mcpanel-fw'
  assert_no_file "$ROOT$ETC_PANEL/mcpanel_fw.nft"
}

test_install_fw_ensure_aborts_when_a_live_session_is_not_allowed() {
  # sudo drops SSH_CONNECTION; the socket table still shows the session on port 2022 (IPv6)
  fw_fixture
  sshd() { echo "port 22"; }
  # shellcheck disable=SC2016 # the body is code for the stub
  stub ss 'printf "%s\n" "0 0 [2001:db8::7]:2022 [2001:db8::4]:50122 users:((\"sshd-session\",pid=9,fd=4))"'
  assert_fails fw_ensure
  assert_not_called 'mcpanel-fw'
  assert_no_file "$ROOT$ETC_PANEL/mcpanel_fw.nft"
}

test_install_fw_ensure_explicit_port_must_cover_the_session() {
  fw_fixture
  SSH_PORT=2200
  SSH_CONNECTION="198.51.100.4 50122 203.0.113.7 22"
  assert_fails fw_ensure
  assert_not_called 'mcpanel-fw'
}

test_install_fw_ensure_aborts_without_known_ssh_port() {
  fw_fixture
  sshd() { return 1; }
  assert_fails fw_ensure
  assert_contains "$(cat "$WORK/out")" "--ssh-port"
  assert_not_called 'mcpanel-fw'
}

test_install_fw_ensure_rejects_invalid_port() {
  fw_fixture
  SSH_PORT=70000
  assert_fails fw_ensure
  assert_not_called 'nft'
}

test_install_fw_ensure_uses_session_port_when_sshd_unavailable() {
  fw_fixture
  sshd() { return 1; }
  SSH_CONNECTION="2001:db8::4 50122 2001:db8::7 2022"
  fw_ensure >/dev/null
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" 'tcp dport { 2022 } accept'
}

test_install_fw_ensure_aborts_when_ruleset_invalid() {
  fw_fixture
  sshd() { echo "port 22"; }
  sys_nft() { return 1; }
  assert_fails fw_ensure
  assert_no_file "$ROOT$ETC_PANEL/mcpanel_fw.nft"
  assert_not_called 'mcpanel-fw'
}

# main never calls fw_ensure with --no-firewall (overview V4); unit_ensure only writes the
# unit (test_install_unit_ensure_never_enables_the_firewall_unit).

test_install_fw_ensure_second_run_does_not_reload() {
  fw_fixture
  sshd() { echo "port 22"; }
  fw_ensure >/dev/null
  : >"$WORK/calls.log"
  assert_contains "$(fw_ensure)" "[ok] rules unchanged"
  assert_not_called 'restart mcpanel-fw'
  assert_called 'systemctl enable --now mcpanel-fw.service'
}
