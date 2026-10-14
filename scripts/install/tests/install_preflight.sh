# shellcheck shell=bash
# Tests of preflight.sh (installer spec §4.1). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329

pre_fixture_os() { # ID VERSION_ID
  mkdir -p "$ROOT/etc" "$ROOT/proc/1"
  printf 'NAME="Ubuntu"\nID=%s\nVERSION_ID="%s"\n' "$1" "$2" >"$ROOT/etc/os-release"
  printf 'systemd\n' >"$ROOT/proc/1/comm"
}

# pre_fixture: a supported host with free ports and matching DNS.
pre_fixture() {
  pre_fixture_os ubuntu 26.04
  stub pre_port_owner ':'
  stub pre_domain_matches 'return 0'
  DOMAIN=panel.example.com EMAIL=a@example.com TLS_MODE=letsencrypt CMD=install
}

test_install_pre_os_ok_accepts_ubuntu_2604() {
  pre_fixture_os ubuntu 26.04
  pre_os_ok
}

test_install_pre_os_ok_rejects_2404_without_override() {
  pre_fixture_os ubuntu 24.04
  assert_fails pre_os_ok
}

test_install_pre_os_ok_accepts_2404_with_override() {
  pre_fixture_os ubuntu 24.04
  MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1 pre_os_ok
}

test_install_pre_os_ok_rejects_debian_even_with_override() {
  pre_fixture_os debian 13
  MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1
  assert_fails pre_os_ok
}

test_install_pre_os_ok_never_sources_the_file() {
  mkdir -p "$ROOT/etc"
  printf 'touch %s/sourced\nID=ubuntu\nVERSION_ID=26.04\n' "$WORK" >"$ROOT/etc/os-release"
  pre_os_ok
  assert_no_file "$WORK/sourced"
}

test_install_pre_check_requires_systemd() {
  pre_fixture
  printf 'init\n' >"$ROOT/proc/1/comm"
  assert_fails pre_check
  assert_contains "$(cat "$WORK/out")" "systemd must be PID 1"
}

test_install_pre_port_owner_reads_ss() {
  stub ss "printf '%s\\n' 'LISTEN 0 4096 *:443 *:* users:((\"caddy\",pid=812,fd=8))'"
  assert_eq caddy "$(pre_port_owner 443)"
  stub ss "printf '%s\\n' 'LISTEN 0 4096 *:443 *:*'"
  assert_eq unknown "$(pre_port_owner 443)"
  stub ss ':'
  assert_eq '' "$(pre_port_owner 443)"
}

test_install_pre_check_refuses_foreign_port_user() {
  pre_fixture_os ubuntu 26.04
  stub pre_domain_matches 'return 0'
  DOMAIN=panel.example.com EMAIL=a@example.com TLS_MODE=letsencrypt CMD=install
  # the real pre_port_owner against a stubbed ss: nginx on port 80 only
  stub ss "[[ \$* == *':80' ]] && printf '%s\\n' 'LISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:((\"nginx\",pid=99,fd=6))'; :"
  assert_fails pre_check
  assert_contains "$(cat "$WORK/out")" "port 80 is in use by nginx"
}

test_install_pre_check_allows_own_services_on_ports() {
  pre_fixture
  pre_port_owner() { case "$1" in 80 | 443) echo caddy ;; 25565) echo docker-proxy ;; esac; }
  assert_contains "$(pre_check)" "[ok]"
}

test_install_pre_domain_matches_local_address() {
  stub getent "echo '203.0.113.7     STREAM panel.example.com'"
  stub ip "echo '2: eth0    inet 203.0.113.7/24 brd 203.0.113.255 scope global eth0'"
  DOMAIN=panel.example.com
  pre_domain_matches
}

test_install_pre_domain_matches_route_source() {
  # behind 1:1 NAT the interface has a private address; the route source is the public one
  stub getent "echo '2001:db8::7     STREAM panel.example.com'"
  # shellcheck disable=SC2016 # the body is code for the stub
  stub ip '[[ $1 == -6 ]] && echo "2606:4700:4700::1111 from :: via fe80::1 dev eth0 src 2001:db8::7 metric 100"; :'
  DOMAIN=panel.example.com
  pre_domain_matches
}

test_install_pre_domain_mismatch_and_unresolved() {
  stub getent "echo '198.51.100.9     STREAM panel.example.com'"
  stub ip "echo '2: eth0    inet 203.0.113.7/24 brd 203.0.113.255 scope global eth0'"
  DOMAIN=panel.example.com
  assert_fails pre_domain_matches
  stub getent 'return 2'
  assert_fails pre_domain_matches
}

test_install_pre_domain_mismatch_needs_confirmation() {
  pre_fixture
  stub pre_domain_matches 'return 1'
  YES=0
  assert_fails pre_check
  assert_contains "$(cat "$WORK/out")" "[warn]"
  TTY="$WORK/tty"
  printf 'y\n' >"$TTY"
  pre_check >/dev/null
  YES=1
  TTY="$WORK/no-tty"
  pre_check >/dev/null
}

test_install_pre_check_internal_tls_skips_dns() {
  pre_fixture
  stub pre_domain_matches 'echo "dns must not be checked" >&2; return 1'
  DOMAIN=panel.192-168-1-117.sslip.io EMAIL='' TLS_MODE=internal YES=0
  pre_check >/dev/null
}
