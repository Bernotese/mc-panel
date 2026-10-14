# shellcheck shell=bash
# preflight.sh - installer spec §4.1: refuse to touch a system the installer does not support.
# Runs before any change, so it may die; root, architecture and the arguments are checked by
# main.sh (require_root, detect_arch, validate_args). Also runs fw_plan (SSH ports, sessions,
# listeners) and checks an existing panel.toml against the domain.

# Ports the panel needs; the value is the process allowed to hold the port already (Caddy for
# HTTP(S), Docker's proxy for the published mc-router port).
declare -gA PRE_PORTS=([80]=caddy [443]=caddy [25565]=docker-proxy)

# pre_os_field NAME prints the value of NAME in $ROOT/etc/os-release without quotes (the file
# is parsed, never sourced).
pre_os_field() {
  awk -F= -v k="$1" '$1 == k { v = substr($0, length(k) + 2); gsub(/["\047]/, "", v); print v; exit }' \
    "$ROOT/etc/os-release" 2>/dev/null
}

# pre_os_ok: 0 on Ubuntu 26.04; other Ubuntu releases only with the CI override
# MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1, other distributions never.
pre_os_ok() {
  [[ -r $ROOT/etc/os-release ]] || return 1
  [[ $(pre_os_field ID) == ubuntu ]] || return 1
  if [[ $(pre_os_field VERSION_ID) == 26.04 ]]; then
    return 0
  fi
  [[ ${MCPANEL_INSTALL_ALLOW_UNSUPPORTED:-0} == 1 ]]
}

# pre_port_owner PORT prints the name of the process listening on TCP PORT ("" = free,
# "unknown" = in use by a process ss does not name).
pre_port_owner() {
  local line
  line="$(ss -Hltnp "sport = :$1" 2>/dev/null | head -n 1)"
  [[ -n $line ]] || return 0
  if [[ $line =~ users:\(\(\"([^\"]+)\" ]]; then
    echo "${BASH_REMATCH[1]}"
  else
    echo unknown
  fi
}

# pre_domain_matches: 0 if DOMAIN resolves (A/AAAA) to an address of this host: a global
# address of an interface or the source address of the default route (IPv4 and IPv6).
pre_domain_matches() {
  local resolved local_addrs
  resolved="$(getent ahosts "$DOMAIN" 2>/dev/null | awk '{ print $1 }' | LC_ALL=C sort -u)"
  [[ -n $resolved ]] || return 1
  local_addrs="$({
    ip -o addr show scope global 2>/dev/null | awk '{ print $4 }' | cut -d/ -f1
    ip route get 1.1.1.1 2>/dev/null | awk '{ for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1) }'
    ip -6 route get 2606:4700:4700::1111 2>/dev/null | awk '{ for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1) }'
  } | LC_ALL=C sort -u)"
  [[ -n $local_addrs ]] || return 1
  [[ -n "$(LC_ALL=C comm -12 <(printf '%s\n' "$resolved") <(printf '%s\n' "$local_addrs"))" ]]
}

# pre_domain_consistent: an existing panel.toml must name the same host in public_url as
# --domain (localhost with --tls internal and no --domain); otherwise Caddy would serve a site
# the panel rejects (origin checks) - abort before anything changes.
pre_domain_consistent() {
  local have
  have="$(cfg_domain_from_config)"
  if [[ -n $have && $have != "$DOMAIN" ]]; then
    die "$ETC_PANEL/panel.toml has public_url https://$have, but the domain is $DOMAIN; pass --domain $have or change public_url there first"
  fi
}

pre_check() {
  log_step "preflight"
  # The stdin password (--admin-user) is read before any other command could consume stdin.
  adm_preread
  pre_os_ok || die "this installer supports Ubuntu 26.04 LTS only"
  [[ "$(cat -- "$ROOT/proc/1/comm" 2>/dev/null)" == systemd ]] || die "systemd must be PID 1"
  local port owner
  for port in "${!PRE_PORTS[@]}"; do
    owner="$(pre_port_owner "$port")"
    if [[ -n $owner && $owner != "${PRE_PORTS[$port]}" ]]; then
      die "port $port is in use by $owner; mc-panel needs 80 and 443 (Caddy) and 25565 (Minecraft)"
    fi
  done
  pre_domain_consistent
  if [[ $TLS_MODE == letsencrypt ]] && ! pre_domain_matches; then
    warn "$DOMAIN does not resolve to an address of this host; Let's Encrypt fails until DNS points here"
    confirm "continue anyway?" || die "aborted: point the DNS record of $DOMAIN to this host first (or pass --yes)"
  fi
  # SSH ports and the running sessions before anything changes (fw_ensure checks again).
  if [[ $NO_FIREWALL != 1 ]]; then
    fw_plan
  fi
  log_ok "Ubuntu $(pre_os_field VERSION_ID), systemd, ports 80/443/25565"
}
