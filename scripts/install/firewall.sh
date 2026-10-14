# shellcheck shell=bash
# firewall.sh - installer spec §4.8: the host firewall in its own nftables table inet mcpanel_fw
# (never the agent's table inet mcpanel). Order of safety (review focus 1), fw_plan:
#   1. the SSH ports: --ssh-port, else sshd's configuration (sshd -T: port and listenaddress),
#      compared with the sockets sshd (or ssh.socket) really listens on - a difference aborts;
#      only without both the port of the running session, with a warning;
#   2. every port a running SSH session uses must be among them ($SSH_CONNECTION, which sudo
#      drops, and the established connections on a listening sshd port);
#   3. every other non-loopback listener that the rules would block is listed and must be
#      confirmed (or opened with --allow-port PORT[/udp]); without a terminal and --yes: abort.
# pre_check runs fw_plan before anything changes; fw_ensure runs it again right before nft -c
# checks the ruleset (before it is written and before it is loaded). The ruleset accepts
# established connections first, so the running session survives the load. Only this step
# enables mcpanel-fw.service; main.sh skips it with --no-firewall. Needs KEY_VERSION.

FW_RULES_FILE="$ETC_PANEL/mcpanel_fw.nft"
FW_ALLOW_PORTS=()       # --allow-port PORT[/tcp|/udp], filled by main.sh
FW_SSH_PORTS=()         # set by fw_plan
FW_ACCEPTED_EXPOSURE="" # listeners the operator agreed to block (fw_plan asks only once)
FW_BASE_TCP=(80 443 25565)
FW_BASE_UDP=(443 546) # HTTP/3; DHCPv6 client (the template allows it from fe80::/10)
FW_IGNORE_UDP=(68)    # DHCPv4 client: its replies are tracked connections

# fw_config_ports prints the ports of sshd's configuration, one per line, sorted: the ports of
# the listenaddress lines of sshd -T (it prints them with the port, e.g. 0.0.0.0:2222), else the
# port lines (a listenaddress with its own port overrides Port).
fw_config_ports() {
  sshd -T 2>/dev/null | awk '
    $1 == "port" { port[$2] = 1 }
    $1 == "listenaddress" { n = split($2, a, ":"); if (n > 1 && a[n] ~ /^[0-9]+$/) { la[a[n]] = 1; nla++ } }
    END { if (nla) { for (p in la) print p } else { for (p in port) print p } }' |
    LC_ALL=C sort -un
}

# fw_listen_ports prints the TCP ports sshd really listens on: its own sockets and, with socket
# activation, the Listen= entries of an active ssh.socket; one per line, sorted.
fw_listen_ports() {
  {
    ss -Hltnp 2>/dev/null | awk '/users:\(\("sshd/ { n = split($4, a, ":"); print a[n] }'
    if systemctl is-active --quiet ssh.socket 2>/dev/null; then
      systemctl show -p Listen --value ssh.socket 2>/dev/null | grep -oE ':[0-9]+ \(Stream\)' | tr -dc '0-9\n'
    fi
  } | grep -E '^[0-9]+$' | LC_ALL=C sort -un
}

# fw_session_port prints the server port of the SSH session in $SSH_CONNECTION ("client port
# server port", IPv4 or IPv6); nothing without a session.
fw_session_port() {
  [[ -n ${SSH_CONNECTION:-} ]] || return 0
  local _client _cport _server sport=""
  read -r _client _cport _server sport <<<"$SSH_CONNECTION"
  echo "$sport"
}

# fw_live_ssh_ports [PORT...] prints the local ports of established connections of sshd, one
# per line; with PORTs only those on one of them (forwarded connections of sshd use other
# local ports and are no sessions).
fw_live_ssh_ports() {
  ss -Htnp state established 2>/dev/null |
    awk -v want=" $* " '/users:\(\("sshd/ { n = split($3, a, ":"); if (want == "  " || index(want, " " a[n] " ")) print a[n] }'
}

# fw_listeners prints "PORT/PROTO PROCESS" for every TCP/UDP socket listening on a non-loopback
# address, sorted and unique.
fw_listeners() {
  ss -Hlntup 2>/dev/null | awk '
    { local = $5; proc = "?"
      if (match($0, /users:\(\("[^"]+"/)) proc = substr($0, RSTART + 9, RLENGTH - 10)
      if (local ~ /^127\./ || local ~ /^\[::1\]/ || local ~ /^\[::ffff:127\./ || local ~ /%lo:/) next
      n = split(local, a, ":"); print a[n] "/" $1 " " proc }' | LC_ALL=C sort -u
}

# fw_allowed PORT PROTO: 0 if the rules (SSH ports, base ports, --allow-port) accept it.
fw_allowed() {
  local p proto
  local -a fixed=("${FW_BASE_UDP[@]}" "${FW_IGNORE_UDP[@]}")
  if [[ $2 == tcp ]]; then
    fixed=("${FW_SSH_PORTS[@]}" "${FW_BASE_TCP[@]}")
  fi
  for p in "${fixed[@]}"; do
    if [[ $p == "$1" ]]; then return 0; fi
  done
  for p in "${FW_ALLOW_PORTS[@]}"; do
    proto=tcp
    if [[ $p == */* ]]; then proto="${p#*/}"; fi
    if [[ ${p%/*} == "$1" && $proto == "$2" ]]; then return 0; fi
  done
  return 1
}

# fw_extra_rules prints the rule lines for --allow-port (template placeholder EXTRA_RULES).
fw_extra_rules() {
  local p tcp="" udp="" out=""
  for p in "${FW_ALLOW_PORTS[@]}"; do
    if [[ $p == */udp ]]; then udp+="${udp:+, }${p%/*}"; else tcp+="${tcp:+, }${p%/*}"; fi
  done
  [[ -z $tcp ]] || out+="tcp dport { $tcp } accept # --allow-port"
  [[ -z $udp ]] || out+="${out:+$'\n\t\t'}udp dport { $udp } accept # --allow-port"
  printf '%s\n' "${out:-# no extra ports (install.sh install --allow-port PORT[/udp])}"
}

# fw_plan sets FW_SSH_PORTS or dies; it changes nothing (pre_check and fw_ensure run it).
fw_plan() {
  local p list line missing=""
  local -a cfg real sessions
  mapfile -t cfg < <(fw_config_ports)
  mapfile -t real < <(fw_listen_ports)
  if [[ -n $SSH_PORT ]]; then
    FW_SSH_PORTS=("$SSH_PORT")
    for p in "${real[@]}"; do
      [[ $p == "$SSH_PORT" ]] || warn "sshd also listens on port $p, which --ssh-port $SSH_PORT does not allow"
    done
  elif ((${#cfg[@]} > 0 && ${#real[@]} > 0)); then
    if [[ "${cfg[*]}" != "${real[*]}" ]]; then
      die "sshd is configured for port(s) ${cfg[*]} but listens on ${real[*]} (restart ssh or pass --ssh-port)"
    fi
    FW_SSH_PORTS=("${cfg[@]}")
  elif ((${#cfg[@]} > 0)); then
    FW_SSH_PORTS=("${cfg[@]}")
  elif ((${#real[@]} > 0)); then
    FW_SSH_PORTS=("${real[@]}")
  else
    mapfile -t FW_SSH_PORTS < <({ fw_session_port; fw_live_ssh_ports; } | grep -E '^[0-9]+$' | LC_ALL=C sort -un)
    ((${#FW_SSH_PORTS[@]} > 0)) ||
      die "cannot determine the SSH port (sshd -T failed, no sshd socket, no SSH session); pass --ssh-port"
    warn "sshd -T and the socket table name no SSH port; allowing the port of this session: ${FW_SSH_PORTS[*]}"
  fi
  for p in "${FW_SSH_PORTS[@]}"; do
    if ! [[ $p =~ ^[0-9]+$ ]] || ((10#$p < 1 || 10#$p > 65535)); then
      die "invalid SSH port $p"
    fi
  done
  list="${FW_SSH_PORTS[*]}"
  mapfile -t sessions < <({ fw_session_port; fw_live_ssh_ports "${real[@]}"; } | grep -E '^[0-9]+$' | LC_ALL=C sort -un)
  for p in "${sessions[@]}"; do
    if [[ " $list " != *" $p "* ]]; then
      die "an SSH session uses port $p, which the firewall would block (allowed: $list); pass --ssh-port or --no-firewall"
    fi
  done
  while IFS= read -r line; do
    [[ -n $line ]] || continue
    p="${line%% *}"
    if ! fw_allowed "${p%/*}" "${p#*/}"; then
      missing+="${missing:+, }$line"
    fi
  done < <(fw_listeners)
  if [[ -n $missing && $missing != "$FW_ACCEPTED_EXPOSURE" ]]; then
    warn "the firewall will block these listening ports: $missing (open them with --allow-port PORT[/udp])"
    confirm "block them and continue?" ||
      die "aborted: pass --allow-port for the ports to keep open, --yes to block them, or --no-firewall"
    FW_ACCEPTED_EXPOSURE="$missing"
  fi
}

fw_ensure() {
  log_step "firewall"
  local list content tmp="$WORK/mcpanel_fw.nft" r
  fw_plan
  list="$(printf '%s, ' "${FW_SSH_PORTS[@]}")"
  list="${list%, }"
  export KEY_SSH_PORT="$list"
  export KEY_EXTRA_RULES
  KEY_EXTRA_RULES="$(fw_extra_rules)"
  content="$(render_template nftables/mcpanel_fw.nft)" || return 1
  printf '%s\n' "$content" >"$tmp"
  sys_nft -c -f "$tmp" </dev/null || die "nft rejected the ruleset; nothing was changed"
  r="$(write_file "$FW_RULES_FILE" 0644 root root <"$tmp")" || return 1
  sys_nft -c -f "$ROOT$FW_RULES_FILE" </dev/null || {
    warn "nft rejected $FW_RULES_FILE; the firewall was not loaded"
    return 1
  }
  sys_systemctl enable mcpanel-fw.service </dev/null || return 1
  # reload-or-restart: an active unit reloads (ExecReload, nft -f replaces the table
  # atomically), an inactive one starts.
  if [[ $r == changed ]]; then
    sys_systemctl reload-or-restart mcpanel-fw.service </dev/null || {
      warn "mcpanel-fw.service failed (journalctl -u mcpanel-fw)"
      return 1
    }
    log_changed "allowed: SSH $list, 80, 443, 25565 (tcp), 443 (udp)${FW_ALLOW_PORTS[*]:+, --allow-port ${FW_ALLOW_PORTS[*]}}"
  else
    sys_systemctl start mcpanel-fw.service </dev/null || {
      warn "mcpanel-fw.service failed (journalctl -u mcpanel-fw)"
      return 1
    }
    log_ok "rules unchanged (SSH $list)"
  fi
}
