# shellcheck shell=bash
# config.sh - installer spec §4.5: panel.toml, agent.toml and files.toml from the templates when
# missing; in existing files only missing required keys are added (toml_ensure_key), values are
# never changed; mode and owner are corrected. cfg_keys also sets every KEY_* of the templates
# (overview V3) for units.sh, caddy.sh and firewall.sh; doctor reads CFG_REQUIRED_* too.

# Required top-level keys and their TOML values, filled by cfg_keys. The lists match
# internal/config (panel: listen, public_url, data_dir, agent_socket, files_socket,
# trusted_proxies, monitoring_ping_addr, instances_dir; agent: socket, panel_uid, firewall,
# instances_dir, mc_user, web_proxy_port, web_ask_port; files: socket, panel_uid, instances_dir,
# restic_binary).
declare -gA CFG_REQUIRED_PANEL=() CFG_REQUIRED_AGENT=() CFG_REQUIRED_FILES=()
CFG_CHANGED=0 # 1 once cfg_ensure changed a file (units.sh restarts the services)
# The ports of mcpanel-web on 127.0.0.1 (internal/config defaults); agent.toml may move them, and
# mc-panel-web.caddy and the Caddyfile's on_demand_tls ask follow (cfg_web_ports).
CFG_WEB_PROXY_PORT=18100
CFG_WEB_ASK_PORT=18101

# cfg_restic_path prints the restic from PATH ("" if none; a query, tests stub it).
cfg_restic_path() {
  command -v restic 2>/dev/null || true
}

# cfg_domain_from_config prints the host of public_url in an existing panel.toml ("" if none):
# update and doctor have no --domain.
cfg_domain_from_config() {
  local f="$ROOT$ETC_PANEL/panel.toml"
  [[ -f $f && ! -L $f ]] || return 0
  awk '/^[[:space:]]*\[/ { exit }
    /^[[:space:]]*public_url[[:space:]]*=/ { sub(/^[^=]*=[[:space:]]*"/, ""); sub(/".*$/, ""); print; exit }' "$f" |
    sed -E 's,^https?://,,; s,[/:].*$,,'
}

# cfg_agent_value KEY prints the raw value of a top-level KEY in agent.toml (trailing comment
# removed; "" when the key or the file is missing).
cfg_agent_value() {
  local f="$ROOT$ETC_PANEL/agent.toml"
  [[ -f $f && ! -L $f ]] || return 0
  awk -v k="$1" '
    /^[[:space:]]*\[/ { exit }
    { line = $0; sub(/^[[:space:]]+/, "", line)
      if (index(line, k) == 1) { r = substr(line, length(k) + 1); sub(/^[[:space:]]+/, "", r)
        if (substr(r, 1, 1) == "=") { r = substr(r, 2); sub(/^[[:space:]]+/, "", r)
          sub(/[[:space:]]*(#.*)?$/, "", r); print r; exit } } }' "$f"
}

# cfg_web_ports sets CFG_WEB_PROXY_PORT and CFG_WEB_ASK_PORT from agent.toml (the defaults when a
# key is missing). 1 with a warning for a value the agent would refuse (internal/config: decimal
# 1024-65535, not 25565 or 25575, the two different): Caddy must never get other ports than the
# agent.
cfg_web_ports() {
  local key v
  CFG_WEB_PROXY_PORT=18100
  CFG_WEB_ASK_PORT=18101
  for key in web_proxy_port web_ask_port; do
    v="$(cfg_agent_value "$key")"
    [[ -n $v ]] || continue
    if ! [[ $v =~ ^[1-9][0-9]{3,4}$ ]] || ((v < 1024 || v > 65535 || v == 25565 || v == 25575)); then
      warn "$ETC_PANEL/agent.toml: $key = $v is not a port in 1024-65535 (25565 and 25575 are taken)"
      return 1
    fi
    if [[ $key == web_proxy_port ]]; then CFG_WEB_PROXY_PORT="$v"; else CFG_WEB_ASK_PORT="$v"; fi
  done
  if [[ $CFG_WEB_PROXY_PORT == "$CFG_WEB_ASK_PORT" ]]; then
    warn "$ETC_PANEL/agent.toml: web_proxy_port and web_ask_port are both $CFG_WEB_ASK_PORT"
    return 1
  fi
}

# cfg_keys sets (and exports, render_template runs in subshells) the template values and the
# required-key lists. 1 if the user mcpanel does not exist or agent.toml has invalid web ports.
# shellcheck disable=SC2034 # CFG_REQUIRED_* are read through a nameref (cfg_file) and by doctor
cfg_keys() {
  local uid
  if ! uid="$(lay_uid mcpanel)" || [[ -z $uid ]]; then
    warn "user mcpanel is missing (did the layout step run?)"
    return 1
  fi
  if [[ -z $DOMAIN ]]; then
    DOMAIN="$(cfg_domain_from_config)"
  fi
  export KEY_DOMAIN="$DOMAIN"
  export KEY_PANEL_UID="$uid"
  export KEY_INSTANCES_DIR="$INSTANCES_DIR"
  # pkg_install found it (install); on update the restic from PATH, else Ubuntu's path.
  export KEY_RESTIC_BINARY="${PKG_RESTIC_BIN:-$(cfg_restic_path)}"
  KEY_RESTIC_BINARY="${KEY_RESTIC_BINARY:-/usr/bin/restic}"
  export KEY_PING_ADDR=127.0.0.1:25565
  export KEY_VERSION
  KEY_VERSION="$(bin_version)"
  if [[ $TLS_MODE == internal ]]; then
    export KEY_TLS="tls internal"
  else
    export KEY_TLS="tls $EMAIL"
  fi
  # mc-panel-web.caddy: "tls <issuer> {" with on_demand on a line of its own (Caddy rejects a
  # one-line block).
  local issuer="internal"
  if [[ $TLS_MODE != internal ]]; then issuer="$EMAIL"; fi
  export KEY_TLS_ON_DEMAND="${issuer:+$issuer }{"$'\n\t\ton_demand\n\t}'
  cfg_web_ports || return 1
  export KEY_WEB_PROXY_PORT="$CFG_WEB_PROXY_PORT"
  export KEY_WEB_ASK_PORT="$CFG_WEB_ASK_PORT"
  CFG_REQUIRED_PANEL=(
    [listen]='"unix:/run/mc-panel-web/panel.sock"'
    [data_dir]="\"$DATA_DIR\""
    [agent_socket]='"/run/mc-panel/agent.sock"'
    [files_socket]='"/run/mc-panel-files/files.sock"'
    [trusted_proxies]='["127.0.0.1/32", "::1/128"]'
    [monitoring_ping_addr]="\"$KEY_PING_ADDR\""
    [instances_dir]="\"$INSTANCES_DIR\""
  )
  # Without a domain (update of a panel.toml without public_url) the key cannot be added and
  # no KEY_PUBLIC_URL exists, so a missing panel.toml cannot be rendered either.
  unset KEY_PUBLIC_URL
  if [[ -n $DOMAIN ]]; then
    export KEY_PUBLIC_URL="https://$DOMAIN"
    CFG_REQUIRED_PANEL[public_url]="\"$KEY_PUBLIC_URL\""
  fi
  CFG_REQUIRED_AGENT=(
    [socket]='"/run/mc-panel/agent.sock"'
    [panel_uid]="$uid"
    [firewall]='"nftables"'
    [instances_dir]="\"$INSTANCES_DIR\""
    [mc_user]='"mcpanel-mc"'
    [web_proxy_port]="$CFG_WEB_PROXY_PORT"
    [web_ask_port]="$CFG_WEB_ASK_PORT"
  )
  CFG_REQUIRED_FILES=(
    [socket]='"/run/mc-panel-files/files.sock"'
    [panel_uid]="$uid"
    [instances_dir]="\"$INSTANCES_DIR\""
    [restic_binary]="\"$KEY_RESTIC_BINARY\""
  )
}

# cfg_file TEMPLATE DEST MODE OWNER GROUP ARRAYNAME prints changed/unchanged; 1 on failure.
# DEST is a system path without $ROOT (write_template, write_file and toml_ensure_key add it).
# Runs in $(...): every failure returns explicitly.
cfg_file() {
  local tpl="$1" dest="$2" mode="$3" owner="$4" group="$5" key r any=0 keys=()
  local -n req="$6"
  if [[ -L $ROOT$dest || (-e $ROOT$dest && ! -f $ROOT$dest) ]]; then
    warn "$dest is not a regular file; refusing to use it"
    return 1
  fi
  if [[ ! -e $ROOT$dest ]]; then
    write_template "$tpl" "$dest" "$mode" "$owner" "$group" >/dev/null || return 1
    echo changed
    return 0
  fi
  mapfile -t keys < <(printf '%s\n' "${!req[@]}" | LC_ALL=C sort)
  for key in "${keys[@]}"; do
    r="$(toml_ensure_key "$dest" "$key" "${req[$key]}")" || return 1
    [[ $r == unchanged ]] || any=1
  done
  # Mode and owner of an existing file: write_file with the same content corrects them.
  r="$(write_file "$dest" "$mode" "$owner" "$group" <"$ROOT$dest")" || return 1
  [[ $r == unchanged ]] || any=1
  if ((any)); then echo changed; else echo unchanged; fi
}

# shellcheck disable=SC2034 # CFG_CHANGED is read by units.sh
cfg_ensure() {
  log_step "configuration"
  cfg_keys || return 1
  if [[ -z $DOMAIN && ! -e $ROOT$ETC_PANEL/panel.toml ]]; then
    warn "$ETC_PANEL/panel.toml is missing and no domain is known; run install --domain"
    return 1
  fi
  local a b c
  a="$(cfg_file config/panel.toml "$ETC_PANEL/panel.toml" 0640 root mcpanel CFG_REQUIRED_PANEL)" || return 1
  b="$(cfg_file config/agent.toml "$ETC_PANEL/agent.toml" 0600 root root CFG_REQUIRED_AGENT)" || return 1
  c="$(cfg_file config/files.toml "$ETC_FILES/files.toml" 0640 root mcpanel-mc CFG_REQUIRED_FILES)" || return 1
  # The panel takes the socket of mcpanel-panel.socket only if listen names exactly that path.
  local listen
  listen="$(awk '/^[[:space:]]*\[/ { exit }
    /^[[:space:]]*listen[[:space:]]*=/ { sub(/^[^=]*=[[:space:]]*/, ""); sub(/[[:space:]]*(#.*)?$/, ""); print; exit }' \
    "$ROOT$ETC_PANEL/panel.toml")"
  if [[ $listen != '"unix:/run/mc-panel-web/panel.sock"' ]]; then
    warn "panel.toml: listen = $listen, but mcpanel-panel.socket listens on /run/mc-panel-web/panel.sock; Caddy cannot reach the panel"
  fi
  if [[ $a == changed || $b == changed || $c == changed ]]; then
    CFG_CHANGED=1
    log_changed "panel.toml: $a, agent.toml: $b, files.toml: $c"
  else
    log_skip "panel.toml, agent.toml, files.toml unchanged"
  fi
}
