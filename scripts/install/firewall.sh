# shellcheck shell=bash
# firewall.sh - installer spec §4.8: the host firewall in its own nftables table inet mcpanel_fw
# (never the agent's table inet mcpanel). Order of safety (review focus 1): know the SSH
# port(s); every port a running SSH session uses must be among them ($SSH_CONNECTION and the
# established connections of sshd); nft -c checks the ruleset before it is written and again
# before it is loaded. The ruleset accepts established connections, so the running session
# survives the load. Only this step enables mcpanel-fw.service; main.sh skips it with
# --no-firewall. Needs KEY_VERSION from cfg_keys.

FW_RULES_FILE="$ETC_PANEL/mcpanel_fw.nft"

# fw_ssh_ports prints the SSH ports to allow (space separated, sorted): --ssh-port, else every
# port of sshd -T, else the port of the current session; nothing if none is known.
fw_ssh_ports() {
  if [[ -n $SSH_PORT ]]; then
    echo "$SSH_PORT"
    return 0
  fi
  local ports
  ports="$(sshd -T 2>/dev/null | awk '$1 == "port" { print $2 }' | LC_ALL=C sort -un | xargs)"
  if [[ -z $ports ]]; then
    ports="$(fw_session_port)"
  fi
  echo "$ports"
}

# fw_session_port prints the server port of the SSH session in $SSH_CONNECTION ("client port
# server port", IPv4 or IPv6); nothing without a session.
fw_session_port() {
  [[ -n ${SSH_CONNECTION:-} ]] || return 0
  local _client _cport _server sport=""
  read -r _client _cport _server sport <<<"$SSH_CONNECTION"
  echo "$sport"
}

# fw_live_ssh_ports prints the local ports of established connections that belong to sshd, one
# per line (sudo drops $SSH_CONNECTION, so the sessions are also taken from the socket table).
fw_live_ssh_ports() {
  ss -Htnp state established 2>/dev/null |
    awk '/users:\(\("sshd/ { n = split($3, a, ":"); print a[n] }'
}

fw_ensure() {
  log_step "firewall"
  local ports p list content tmp="$WORK/mcpanel_fw.nft" r
  local -a allowed sessions
  ports="$(fw_ssh_ports)"
  read -r -a allowed <<<"$ports"
  ((${#allowed[@]} > 0)) || die "cannot determine the SSH port (sshd -T failed and no SSH session); pass --ssh-port"
  for p in "${allowed[@]}"; do
    if ! [[ $p =~ ^[0-9]+$ ]] || ((10#$p < 1 || 10#$p > 65535)); then
      die "invalid SSH port $p"
    fi
  done
  mapfile -t sessions < <({ fw_session_port; fw_live_ssh_ports; } | grep -E '^[0-9]+$' | LC_ALL=C sort -un)
  for p in "${sessions[@]}"; do
    if [[ " $ports " != *" $p "* ]]; then
      die "an SSH session uses port $p, which the firewall would block (allowed: $ports); pass --ssh-port or --no-firewall"
    fi
  done
  list="$(printf '%s, ' "${allowed[@]}")"
  list="${list%, }"
  export KEY_SSH_PORT="$list"
  content="$(render_template nftables/mcpanel_fw.nft)" || return 1
  printf '%s\n' "$content" >"$tmp"
  sys_nft -c -f "$tmp" || die "nft rejected the ruleset; nothing was changed"
  r="$(write_file "$FW_RULES_FILE" 0644 root root <"$tmp")" || return 1
  sys_nft -c -f "$ROOT$FW_RULES_FILE" || {
    warn "nft rejected $FW_RULES_FILE; the firewall was not loaded"
    return 1
  }
  if [[ $r == changed ]]; then
    sys_systemctl enable mcpanel-fw.service || return 1
    sys_systemctl restart mcpanel-fw.service || {
      warn "mcpanel-fw.service failed (journalctl -u mcpanel-fw)"
      return 1
    }
    log_changed "allowed: SSH $list, 80, 443, 25565 (tcp), 443 (udp)"
  else
    sys_systemctl enable --now mcpanel-fw.service || {
      warn "mcpanel-fw.service failed (journalctl -u mcpanel-fw)"
      return 1
    }
    log_ok "rules unchanged (SSH $list)"
  fi
}
