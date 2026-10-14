# shellcheck shell=bash
# Tests of firewall.sh (installer spec §4.8, review focus 1: never lock out a running SSH
# session). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329,SC2016

# fw_ss: a fake ss. FW_SS_LISTEN_SSHD (lines of ss -Hltnp), FW_SS_ESTABLISHED (ss -Htnp state
# established) and FW_SS_LISTENERS (ss -Hlntup) hold the answers.
fw_ss() {
  FW_SS_LISTEN_SSHD="" FW_SS_ESTABLISHED="" FW_SS_LISTENERS=""
  ss() {
    case "$1 ${2:-}" in
    "-Hltnp "*) printf '%s' "$FW_SS_LISTEN_SSHD" ;;
    "-Htnp state") printf '%s' "$FW_SS_ESTABLISHED" ;;
    "-Hlntup "*) printf '%s' "$FW_SS_LISTENERS" ;;
    esac
  }
}

# fw_base: no sshd sockets, no ssh.socket, no other listeners, no SSH session, real templates.
fw_base() {
  fw_ss
  stub systemctl 'return 1' # queries only (is-active ssh.socket); sys_systemctl just records
  unset SSH_CONNECTION      # the tests themselves may run over SSH
  DOMAIN=panel.example.com TLS_MODE=letsencrypt EMAIL=a@example.com NO_FIREWALL=0 SSH_PORT='' YES=0
  FW_ALLOW_PORTS=() FW_ACCEPTED_EXPOSURE=""
  mkdir -p "$ROOT$ETC_PANEL"
}

fw_fixture() {
  fw_base
  render_template() { printf 'table inet mcpanel_fw {}\ndelete table inet mcpanel_fw\ntable inet mcpanel_fw { chain input { tcp dport { %s } accept } }\n' "$KEY_SSH_PORT"; }
}

test_install_fw_config_ports_reads_port_lines() {
  sshd() { printf 'port 2222\nport 22\naddressfamily any\n'; }
  assert_eq "22 2222" "$(fw_config_ports | xargs)"
}

test_install_fw_config_ports_prefers_listenaddress() {
  # ListenAddress 0.0.0.0:2222 overrides Port 22; sshd -T prints both
  sshd() { printf 'port 22\nlistenaddress 0.0.0.0:2222\nlistenaddress [::]:2222\n'; }
  assert_eq 2222 "$(fw_config_ports | xargs)"
}

test_install_fw_listen_ports_reads_sshd_and_ssh_socket() {
  fw_ss
  FW_SS_LISTEN_SSHD='LISTEN 0 128 0.0.0.0:2200 0.0.0.0:* users:(("sshd",pid=1,fd=3))
LISTEN 0 4096 *:443 *:* users:(("caddy",pid=2,fd=8))
'
  stub systemctl 'case "$1" in is-active) return 0 ;; show) printf "%s\n" "[::]:22 (Stream)" "0.0.0.0:22 (Stream)" ;; esac'
  assert_eq "22 2200" "$(fw_listen_ports | xargs)"
}

test_install_fw_session_port_ipv4_and_ipv6() {
  SSH_CONNECTION="198.51.100.4 50122 203.0.113.7 2222"
  assert_eq 2222 "$(fw_session_port)"
  SSH_CONNECTION="2001:db8::4 50122 2001:db8::7 22"
  assert_eq 22 "$(fw_session_port)"
  unset SSH_CONNECTION
  assert_eq '' "$(fw_session_port)"
}

test_install_fw_live_ssh_ports_only_on_listening_ports() {
  fw_ss
  FW_SS_ESTABLISHED='0 0 203.0.113.7:2222 198.51.100.4:50122 users:(("sshd-session",pid=9,fd=4))
0 0 [2001:db8::7]:22 [2001:db8::4]:50 users:(("sshd",pid=7,fd=4))
0 0 10.0.0.5:41234 10.0.0.9:5432 users:(("sshd-session",pid=9,fd=7))
0 0 203.0.113.7:443 198.51.100.4:1 users:(("caddy",pid=5,fd=9))
'
  assert_eq "2222 22 41234" "$(fw_live_ssh_ports | xargs)"
  # 41234 is a port forward of sshd, no session
  assert_eq "2222 22" "$(fw_live_ssh_ports 22 2222 | xargs)"
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
  assert_called 'systemctl reload-or-restart mcpanel-fw.service'
}

test_install_fw_ensure_renders_the_real_template() {
  fw_base
  KEY_VERSION=1.2.3
  sshd() { echo "port 22"; }
  fw_ensure >/dev/null
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" 'tcp dport { 22 } accept'
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" 'ct state established,related accept'
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" '# no extra ports'
  assert_eq 'table inet mcpanel_fw {}' "$(grep -v '^#' "$ROOT$ETC_PANEL/mcpanel_fw.nft" | head -n 1)"
}

test_install_fw_ensure_aborts_when_config_and_sockets_differ() {
  # ssh.socket drop-in: ListenStream=2222 while sshd_config still says 22
  fw_fixture
  sshd() { echo "port 22"; }
  stub systemctl 'case "$1" in is-active) return 0 ;; show) echo "[::]:2222 (Stream)" ;; esac'
  assert_fails fw_ensure
  assert_contains "$(cat "$WORK/out")" "configured for port(s) 22 but listens on 2222"
  assert_not_called 'nft'
  assert_no_file "$ROOT$ETC_PANEL/mcpanel_fw.nft"
}

test_install_fw_ensure_accepts_listenaddress_matching_sockets() {
  fw_fixture
  sshd() { printf 'port 22\nlistenaddress 0.0.0.0:2222\n'; }
  FW_SS_LISTEN_SSHD='LISTEN 0 128 0.0.0.0:2222 0.0.0.0:* users:(("sshd",pid=1,fd=3))
'
  fw_ensure >/dev/null
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" 'tcp dport { 2222 } accept'
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
  SSH_PORT=22
  FW_SS_LISTEN_SSHD='LISTEN 0 128 [::]:2022 [::]:* users:(("sshd",pid=1,fd=3))
'
  FW_SS_ESTABLISHED='0 0 [2001:db8::7]:2022 [2001:db8::4]:50122 users:(("sshd-session",pid=9,fd=4))
'
  assert_fails fw_ensure
  assert_contains "$(cat "$WORK/out")" "port 2022"
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

test_install_fw_ensure_explicit_port_warns_about_other_sshd_ports() {
  fw_fixture
  SSH_PORT=2200
  FW_SS_LISTEN_SSHD='LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=1,fd=3))
LISTEN 0 128 0.0.0.0:2200 0.0.0.0:* users:(("sshd",pid=1,fd=4))
'
  assert_contains "$(fw_ensure)" "[warn] sshd also listens on port 22"
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

test_install_fw_ensure_falls_back_to_the_session_port_with_a_warning() {
  fw_fixture
  sshd() { return 1; }
  SSH_CONNECTION="2001:db8::4 50122 2001:db8::7 2022"
  assert_contains "$(fw_ensure)" "[warn] sshd -T and the socket table name no SSH port"
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

# fw_listeners_fixture: nginx on 8080, avahi on 5353/udp, CUPS and the resolver on loopback
# only, the DHCP client on 68/udp, sshd on 22.
fw_listeners_fixture() {
  fw_base
  sshd() { echo "port 22"; }
  FW_SS_LISTENERS='tcp LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=1,fd=3))
tcp LISTEN 0 511 0.0.0.0:8080 0.0.0.0:* users:(("nginx",pid=4,fd=6))
tcp LISTEN 0 511 [::]:8080 [::]:* users:(("nginx",pid=4,fd=7))
tcp LISTEN 0 128 127.0.0.1:631 0.0.0.0:* users:(("cupsd",pid=5,fd=7))
tcp LISTEN 0 128 [::1]:631 [::]:* users:(("cupsd",pid=5,fd=8))
udp UNCONN 0 0 127.0.0.53%lo:53 0.0.0.0:* users:(("systemd-resolve",pid=6,fd=13))
udp UNCONN 0 0 0.0.0.0:5353 0.0.0.0:* users:(("avahi-daemon",pid=7,fd=12))
udp UNCONN 0 0 0.0.0.0%eth0:68 0.0.0.0:* users:(("systemd-network",pid=8,fd=21))
'
  KEY_VERSION=1.2.3
}

test_install_fw_listeners_skip_loopback() {
  fw_listeners_fixture
  assert_eq $'22/tcp sshd\n5353/udp avahi-daemon\n68/udp systemd-network\n8080/tcp nginx' "$(fw_listeners)"
}

test_install_fw_ensure_blocked_listeners_need_confirmation() {
  fw_listeners_fixture
  assert_fails fw_ensure
  assert_contains "$(cat "$WORK/out")" "[warn] the firewall will block these listening ports: 5353/udp avahi-daemon, 8080/tcp nginx"
  assert_not_called 'nft'
  YES=1
  fw_ensure >/dev/null
  assert_called 'reload-or-restart mcpanel-fw.service'
}

test_install_fw_ensure_asks_only_once() {
  fw_listeners_fixture
  TTY="$WORK/tty"
  printf 'y\n' >"$TTY"
  fw_plan >/dev/null # as in pre_check
  : >"$TTY"         # a second question would read no answer
  fw_ensure >/dev/null
}

test_install_fw_ensure_allow_port_opens_listeners() {
  fw_listeners_fixture
  FW_ALLOW_PORTS=(8080 5353/udp 9000/tcp)
  local out
  out="$(fw_ensure)"
  assert_not_contains "$out" "[warn]"
  assert_contains "$out" "--allow-port 8080 5353/udp 9000/tcp"
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" 'tcp dport { 8080, 9000 } accept # --allow-port'
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" 'udp dport { 5353 } accept # --allow-port'
}

test_install_main_parses_and_validates_allow_port() {
  reset_globals
  FW_ALLOW_PORTS=()
  parse_args install --tls internal --version 1.2.3 --allow-port 8080 --allow-port=5353/udp
  assert_eq "8080 5353/udp" "${FW_ALLOW_PORTS[*]}"
  validate_args >/dev/null
  FW_ALLOW_PORTS=(8080/sctp)
  assert_fails validate_args
  assert_contains "$(cat "$WORK/out")" "--allow-port: 8080/sctp"
  FW_ALLOW_PORTS=(0)
  assert_fails validate_args
  FW_ALLOW_PORTS=(65536/udp)
  assert_fails validate_args
}

# main never calls fw_ensure with --no-firewall (overview V4); unit_ensure only writes the
# unit (test_install_unit_ensure_never_enables_the_firewall_unit).

test_install_fw_ensure_second_run_does_not_reload() {
  fw_fixture
  sshd() { echo "port 22"; }
  fw_ensure >/dev/null
  : >"$WORK/calls.log"
  assert_contains "$(fw_ensure)" "[ok] rules unchanged"
  assert_not_called 'reload-or-restart mcpanel-fw'
  assert_called 'systemctl start mcpanel-fw.service'
}
