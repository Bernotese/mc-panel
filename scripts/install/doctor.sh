# shellcheck shell=bash
# doctor.sh - install.sh doctor (installer spec §6): one result per check (✓ ok, ! warn,
# ✗ fail) with a one-line hint. cmd_doctor returns 0 (all ok), 1 (warnings) or 2 (failures)
# instead of exiting (main uses it as exit code); --json (JSON=1) prints
# {"status":"ok|warn|fail","checks":[{"id","status","message","hint"}]} (contract for the CI);
# DOC_SUMMARY=1 (install's summary) prints only what is not ok. Without root, the checks that
# need root report "warn: skipped".
#
# Check ids (stable): services sockets files_process versions update_available update_state
# config permissions caddy_config caddy_import caddy_package caddy_admin certificate dns firewall
# docker docker_version docker_live_restore docker_isolation docker_dns restic tzdata time_sync upgrades reboot_required
# storage_fs storage_free backup_keys ssh_password ssh_root ssh_kbdinteractive

DOC_IDS=()
DOC_STATUS=()
DOC_MSG=()
DOC_HINT=()
DOC_UNITS=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket
  mcpanel-panel.service caddy.service)
DOC_IMPORT_LINE='import mc-panel.caddy # managed by mc-panel'
DOC_CERT_WARN_DAYS=14
# Docker of Ubuntu 26.04's -security pocket (docker.io 29.1.3-0ubuntu4.1); older ones lack its fixes.
DOC_DOCKER_MIN=29.1.3
DOC_DOCKER_ROOT=/var/lib/docker # when docker info cannot tell
# The agent's default container_egress_deny (section A, SEC-02); agent.toml may override it.
DOC_EGRESS_DENY_DEFAULT="169.254.0.0/16 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 fc00::/7 fe80::/10"

# Required top-level keys: the same lists install adds (config.sh CFG_REQUIRED_*; a test compares
# them).
DOC_REQ_PANEL="listen public_url data_dir agent_socket files_socket trusted_proxies monitoring_ping_addr instances_dir"
DOC_REQ_AGENT="socket panel_uid firewall instances_dir mc_user"
DOC_REQ_FILES="socket panel_uid instances_dir restic_binary"

# doc_result STATUS ID MSG [HINT] records one result (STATUS ok|warn|fail).
doc_result() {
  DOC_IDS+=("$2")
  DOC_STATUS+=("$1")
  DOC_MSG+=("$3")
  DOC_HINT+=("${4:-}")
}

# --- probes (tests redefine these) ---------------------------------------------------------

doc_probe_euid() { id -u; }
doc_probe_unit_active() { systemctl is-active --quiet "$1" 2>/dev/null; }
doc_probe_unit_enabled() { systemctl is-enabled --quiet "$1" 2>/dev/null; }
# doc_probe_stat PATH: "owner:group mode" of PATH itself (a symlink is not followed), else "missing".
doc_probe_stat() { stat -c '%U:%G %a' -- "$1" 2>/dev/null || echo missing; }
# doc_probe_files_creds: "ok" when the files service runs as mcpanel-mc without capabilities,
# with no_new_privs and without the group mcpanel; else what is wrong.
doc_probe_files_creds() {
  local pid uid gid status
  pid="$(systemctl show -p MainPID --value mcpanel-files.service 2>/dev/null)" || pid=""
  if [[ -z $pid || $pid == 0 ]]; then
    echo "not running"
    return 0
  fi
  uid="$(id -u mcpanel-mc 2>/dev/null)" || uid=""
  gid="$(getent group mcpanel 2>/dev/null | cut -d: -f3)" || gid=""
  status="$(cat -- "/proc/$pid/status" 2>/dev/null)" || status=""
  awk -v uid="$uid" -v gid="$gid" '
    $1 == "Uid:" { u = ($2 == uid && $3 == uid && $4 == uid && $5 == uid) }
    $1 == "CapEff:" { c = ($2 ~ /^0+$/) }
    $1 == "NoNewPrivs:" { n = ($2 == 1) }
    $1 == "Groups:" { for (i = 2; i <= NF; i++) if ($i == gid) g = 1 }
    END {
      if (!u) print "does not run as mcpanel-mc"
      else if (!c) print "has capabilities"
      else if (!n) print "runs without no_new_privs"
      else if (g) print "is a member of the group mcpanel"
      else print "ok"
    }' <<<"$status"
}
# As caddy with its own HOME, like cad_ensure (B1): validate provisions the TLS storage below
# HOME, and root must not create files there.
doc_probe_caddy_validate() {
  runuser -u caddy -- env HOME=/var/lib/caddy timeout 30 caddy validate --config "$CADDY_DIR/Caddyfile" \
    --adapter caddyfile </dev/null >/dev/null 2>&1
}
# doc_probe_cert_days DOMAIN: days until the certificate Caddy stores for DOMAIN expires
# (negative = expired); nothing when there is none.
doc_probe_cert_days() {
  local crt end
  crt="$(find /var/lib/caddy/.local/share/caddy/certificates -name "$1.crt" -type f 2>/dev/null | head -n 1)" || crt=""
  [[ -n $crt ]] || return 0
  end="$(openssl x509 -enddate -noout -in "$crt" 2>/dev/null | cut -d= -f2)" || end=""
  [[ -n $end ]] || return 0
  end="$(date -d "$end" +%s 2>/dev/null)" || return 0
  echo $(((end - $(date +%s)) / 86400))
}
doc_probe_resolve() { getent ahosts "$1" 2>/dev/null | awk '{ print $1 }' | sort -u; }
doc_probe_local_addrs() { ip -o addr show 2>/dev/null | awk '{ print $4 }' | cut -d/ -f1; }
doc_probe_nft_table() { nft list table inet mcpanel_fw 2>/dev/null; }
# doc_probe_ssh_port: the ports of sshd's configuration, one per line, with the logic of the
# firewall step (fw_config_ports, B1: listenaddress ports win over Port).
doc_probe_ssh_port() { fw_config_ports; }
doc_probe_docker() { timeout 20 docker info >/dev/null 2>&1; }
doc_probe_docker_version() { timeout 20 docker version --format '{{.Server.Version}}' 2>/dev/null; }
doc_probe_docker_live_restore() { timeout 20 docker info --format '{{.LiveRestoreEnabled}}' 2>/dev/null; }
# doc_probe_shared_servers: the containers on the router's network mcpanel-mc, one per line.
doc_probe_shared_servers() { timeout 20 docker network inspect -f '{{range .Containers}}{{.Name}}{{"\n"}}{{end}}' mcpanel-mc 2>/dev/null </dev/null; }
doc_probe_docker_root() { timeout 20 docker info --format '{{.DockerRootDir}}' 2>/dev/null; }
doc_probe_caddy_version() { pkg_caddy_version; }
doc_probe_caddy_from_repo() { pkg_caddy_from_repo; }
# doc_probe_mtime FILE: modification time of FILE in Unix seconds.
doc_probe_mtime() { stat -c %Y -- "$1" 2>/dev/null; }
doc_probe_now() { date +%s; }
doc_probe_restic_version() { restic version 2>/dev/null | awk '{ print $2; exit }'; }
doc_probe_pkg() { dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q 'install ok installed'; }
doc_probe_ntp() { timedatectl show -p NTPSynchronized --value 2>/dev/null; }
# doc_probe_mountpoint PATH: the mount point of the file system PATH is on.
doc_probe_mountpoint() { findmnt -n -o TARGET --target "$1" 2>/dev/null; }
# doc_probe_free_percent: free space on INSTANCES_DIR in percent (df reports the used share).
doc_probe_free_percent() {
  local used
  used="$(df --output=pcent -- "$INSTANCES_DIR" 2>/dev/null | tail -n 1 | tr -dc '0-9')" || used=""
  [[ -n $used ]] || return 0
  echo $((100 - used))
}
doc_probe_sshd() { sshd -T 2>/dev/null; }

# --- helpers -------------------------------------------------------------------------------

# doc_ip6_hex ADDR prints the 32 hex digits of an IPv6 address ("" if it is none).
doc_ip6_hex() {
  local a="${1%%%*}" head tail g n out="" i
  local -a hg tg
  [[ $a == *:* && $a =~ ^[0-9A-Fa-f:]+$ ]] || return 0
  if [[ $a == *::* ]]; then
    head="${a%%::*}"
    tail="${a#*::}"
  else
    head="$a"
    tail=""
  fi
  IFS=: read -r -a hg <<<"$head"
  IFS=: read -r -a tg <<<"$tail"
  n=$((8 - ${#hg[@]} - ${#tg[@]}))
  if [[ $a != *::* ]]; then n=0; fi
  ((n >= 0 && ${#hg[@]} + ${#tg[@]} + n == 8)) || return 0
  for g in "${hg[@]}"; do out+="$(printf '%04x' "0x${g:-0}")"; done
  for ((i = 0; i < n; i++)); do out+='0000'; done
  for g in "${tg[@]}"; do out+="$(printf '%04x' "0x${g:-0}")"; done
  [[ ${#out} == 32 ]] && printf '%s\n' "$out"
}

# doc_ip_in_cidr IP CIDR: 0 if IP (IPv4 or IPv6) lies in CIDR.
doc_ip_in_cidr() {
  local ip="$1" net="${2%/*}" bits="${2#*/}" a b o1 o2 o3 o4 i
  [[ $2 == */* && $bits =~ ^[0-9]+$ ]] || return 1
  if [[ $ip =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    a=$(((BASH_REMATCH[1] << 24) | (BASH_REMATCH[2] << 16) | (BASH_REMATCH[3] << 8) | BASH_REMATCH[4]))
    IFS=. read -r o1 o2 o3 o4 <<<"$net"
    [[ $o4 =~ ^[0-9]+$ ]] || return 1
    b=$(((o1 << 24) | (o2 << 16) | (o3 << 8) | o4))
    ((bits <= 32)) || return 1
    (((a >> (32 - bits)) == (b >> (32 - bits)) || bits == 0))
    return
  fi
  a="$(doc_ip6_hex "$ip")"
  b="$(doc_ip6_hex "$net")"
  [[ -n $a && -n $b ]] && ((bits <= 128)) || return 1
  # whole hex digits first, then the remaining bits of the next digit
  i=$((bits / 4))
  [[ ${a:0:i} == "${b:0:i}" ]] || return 1
  ((bits % 4 == 0)) && return 0
  ((((0x${a:i:1} ^ 0x${b:i:1}) >> (4 - bits % 4)) == 0))
}

# doc_egress_deny prints the egress deny ranges of the agent, one per line: container_egress_deny
# from agent.toml (a TOML array of strings, also over several lines), else the defaults.
doc_egress_deny() {
  local f="$ROOT$ETC_PANEL/agent.toml" v
  if [[ -r $f ]] && doc_toml_has_key "$f" container_egress_deny; then
    v="$(awk '/^[[:space:]]*\[/ && !inarr { exit }
      !inarr && /^[[:space:]]*container_egress_deny[[:space:]]*=/ { inarr = 1; sub(/^[^=]*=/, "") }
      inarr { line = $0; sub(/#.*/, "", line); out = out " " line; if (line ~ /\]/) { print out; exit } }' "$f")"
    grep -oE '"[^"]+"' <<<"$v" | tr -d '"'
    return 0
  fi
  # shellcheck disable=SC2086 # the default list is blank-separated on purpose
  printf '%s\n' $DOC_EGRESS_DENY_DEFAULT
}

# doc_is_root: 0 when doctor runs as root.
doc_is_root() { [[ $(doc_probe_euid) == 0 ]]; }

# doc_need_root ID...: 0 as root; otherwise records "skipped" warnings for ID... and returns 1.
doc_need_root() {
  local id
  doc_is_root && return 0
  for id in "$@"; do
    doc_result warn "$id" "$id: skipped (needs root)" "sudo bash install.sh doctor"
  done
  return 1
}

# doc_toml_has_key FILE KEY: 0 when KEY is set on the top level of FILE (before any [section]).
doc_toml_has_key() {
  awk -v k="$2" '
    /^[[:space:]]*\[/ { exit }
    { line = $0; sub(/^[[:space:]]+/, "", line)
      if (index(line, k) == 1) { r = substr(line, length(k) + 1); sub(/^[[:space:]]+/, "", r)
        if (substr(r, 1, 1) == "=") { found = 1; exit } } }
    END { exit !found }' "$1" 2>/dev/null
}

# doc_toml_string FILE KEY: the value of a top-level string KEY (between the first two quotes).
doc_toml_string() {
  awk -v k="$2" '
    /^[[:space:]]*\[/ { exit }
    { line = $0; sub(/^[[:space:]]+/, "", line)
      if (index(line, k) == 1) { r = substr(line, length(k) + 1); sub(/^[[:space:]]+/, "", r)
        if (substr(r, 1, 1) == "=" && split(r, p, "\"") >= 3) { print p[2]; exit } } }' "$1" 2>/dev/null
}

# doc_domain: host of public_url in panel.toml (without scheme, port and path); nothing if
# unreadable.
doc_domain() {
  local url
  url="$(doc_toml_string "$ROOT$ETC_PANEL/panel.toml" public_url)" || url=""
  url="${url#*://}"
  url="${url%%/*}"
  url="${url%%:*}"
  printf '%s\n' "$url"
}

# doc_json_str S: S as a JSON string (backslash, quote and every control character U+0001 to
# U+001F escaped; Bash strings cannot hold U+0000).
doc_json_str() {
  local s="$1" i c e
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  for ((i = 1; i < 32; i++)); do
    printf -v c '%b' "\\$(printf '%03o' "$i")"
    printf -v e '\\u%04x' "$i"
    s="${s//"$c"/"$e"}"
  done
  printf '"%s"' "$s"
}

# doc_expect_stat ENTRY...: each ENTRY is "path|owner:group mode"; prints the mismatches, one
# per line.
doc_expect_stat() {
  local e path exp got
  for e in "$@"; do
    path="${e%%|*}"
    exp="${e#*|}"
    got="$(doc_probe_stat "$path")" || got=missing
    [[ $got == "$exp" ]] || printf '%s is %s (expected %s)\n' "$path" "$got" "$exp"
  done
}

# --- checks --------------------------------------------------------------------------------

doc_chk_services() {
  local u bad=()
  for u in "${DOC_UNITS[@]}"; do
    doc_probe_unit_active "$u" || bad+=("$u")
  done
  if ((${#bad[@]} == 0)); then
    doc_result ok services "all mc-panel services are running"
  else
    doc_result fail services "not running: ${bad[*]}" "systemctl status ${bad[*]}; journalctl -u ${bad[0]}"
  fi
}

doc_chk_sockets() {
  local bad want=("/run/mc-panel-files/files.sock|mcpanel-mc:mcpanel 660" "/run/mc-panel-web/panel.sock|mcpanel:caddy 660")
  # /run/mc-panel (RuntimeDirectory of the agent) is root:mcpanel 0750: others cannot see the socket
  if doc_is_root; then want=("/run/mc-panel/agent.sock|root:mcpanel 660" "${want[@]}"); fi
  bad="$(doc_expect_stat "${want[@]}")"
  if [[ -z $bad ]]; then
    doc_result ok sockets "sockets have the expected owner and mode"
  else
    doc_result fail sockets "${bad//$'\n'/; }" "systemctl restart mcpanel-agent.service mcpanel-files.socket mcpanel-panel.socket (systemd and the agent set owner and mode)"
  fi
}

doc_chk_files_process() {
  doc_need_root files_process || return 0
  local got
  got="$(doc_probe_files_creds)" || got="unknown"
  if [[ $got == ok ]]; then
    doc_result ok files_process "files service runs as mcpanel-mc without capabilities, with no_new_privs"
  else
    doc_result fail files_process "files service $got" "install.sh update --force (rewrites the unit), then systemctl restart mcpanel-files.service"
  fi
}

doc_chk_versions() {
  local p a f latest rc=0
  read -r p a f <<<"$(upd_binary_versions)"
  if [[ $p == "$a" && $a == "$f" && $p != "?" ]]; then
    doc_result ok versions "panel, agent and files are $p"
  else
    doc_result fail versions "versions differ: panel $p, agent $a, files $f" "install.sh update --force"
  fi
  if ! latest="$(upd_latest_tag 2>/dev/null)" || [[ -z $latest ]]; then
    doc_result warn update_available "could not check for a newer release (no network?)" "curl -fsS https://api.github.com"
    return 0
  fi
  version_ge "$p" "$latest" || rc=$?
  case "$rc" in
  0) doc_result ok update_available "no newer release (latest ${latest#v})" ;;
  1) doc_result warn update_available "release ${latest#v} is available (installed $p)" "install.sh update" ;;
  *) doc_result warn update_available "installed version $p cannot be compared with release ${latest#v}" "install.sh update --version ${latest#v}" ;;
  esac
}

doc_chk_update_state() {
  doc_need_root update_state || return 0
  local d names=()
  while IFS= read -r d; do
    [[ -n $d ]] && names+=("${d##*/}")
  done < <(upd_unfinished_dirs)
  if ((${#names[@]} == 0)); then
    doc_result ok update_state "no unfinished update"
  else
    doc_result fail update_state "unfinished update or failed rollback in $ROLLBACK_DIR: ${names[*]}" \
      "install.sh update (rolls an unfinished update back first); details in $LOG_FILE"
  fi
}

doc_chk_config() {
  doc_need_root config || return 0
  local e file keys k absent=()
  for e in "$ETC_PANEL/panel.toml|$DOC_REQ_PANEL" "$ETC_PANEL/agent.toml|$DOC_REQ_AGENT" "$ETC_FILES/files.toml|$DOC_REQ_FILES"; do
    file="${e%%|*}"
    keys="${e#*|}"
    if [[ ! -f $ROOT$file || -L $ROOT$file ]]; then
      absent+=("$file (file)")
      continue
    fi
    for k in $keys; do
      doc_toml_has_key "$ROOT$file" "$k" || absent+=("$file: $k")
    done
  done
  if ((${#absent[@]} == 0)); then
    doc_result ok config "configuration complete"
  else
    local joined
    joined="$(printf '%s; ' "${absent[@]}")"
    doc_result fail config "absent: ${joined%; }" "install.sh install with your --domain/--email (adds absent keys, keeps existing values)"
  fi
}

doc_chk_permissions() {
  doc_need_root permissions || return 0
  local bad
  bad="$(doc_expect_stat \
    "$ETC_PANEL|root:mcpanel 750" \
    "$ETC_FILES|root:mcpanel-mc 750" \
    "$ETC_PANEL/panel.toml|root:mcpanel 640" \
    "$ETC_PANEL/agent.toml|root:root 600" \
    "$ETC_FILES/files.toml|root:mcpanel-mc 640" \
    "$DATA_DIR|mcpanel:mcpanel 700" \
    "$INSTANCES_DIR|root:root 755" \
    "$INSTANCES_DIR/instances|root:mcpanel-mc 750" \
    "$INSTANCES_DIR/backups|mcpanel-mc:mcpanel-mc 700")"
  if [[ -z $bad ]]; then
    doc_result ok permissions "owners and modes of configuration and data as expected"
  else
    doc_result fail permissions "${bad//$'\n'/; }" "install.sh install with your --domain/--email (resets owners and modes)"
  fi
}

doc_chk_caddy() {
  if doc_need_root caddy_config; then
    if doc_probe_caddy_validate; then
      doc_result ok caddy_config "Caddy configuration is valid"
    else
      doc_result fail caddy_config "Caddy configuration is invalid" "caddy validate --config $CADDY_DIR/Caddyfile --adapter caddyfile"
    fi
  fi
  if [[ -e $ROOT$CADDY_DIR/Caddyfile && ! -r $ROOT$CADDY_DIR/Caddyfile ]] && ! doc_is_root; then
    doc_result warn caddy_import "caddy_import: skipped ($CADDY_DIR/Caddyfile is not readable)" "sudo bash install.sh doctor"
  elif grep -qxF -- "$DOC_IMPORT_LINE" "$ROOT$CADDY_DIR/Caddyfile" 2>/dev/null && [[ -f $ROOT$CADDY_DIR/mc-panel.caddy ]]; then
    doc_result ok caddy_import "Caddy serves the panel site ($CADDY_DIR/mc-panel.caddy)"
  else
    doc_result fail caddy_import "the panel site is not imported in $CADDY_DIR/Caddyfile" "install.sh install with your --domain/--email"
  fi
  doc_chk_caddy_package
  if [[ -e $ROOT$CADDY_DIR/Caddyfile && ! -r $ROOT$CADDY_DIR/Caddyfile ]] && ! doc_is_root; then
    doc_result warn caddy_admin "caddy_admin: skipped ($CADDY_DIR/Caddyfile is not readable)" "sudo bash install.sh doctor"
  elif [[ $(cad_admin_status "$ROOT$CADDY_DIR/Caddyfile") == socket ]]; then
    doc_result ok caddy_admin "Caddy's admin API is off TCP (only caddy and root can use it)"
  else
    doc_result warn caddy_admin "Caddy's admin API listens on TCP (127.0.0.1:2019 by default): any local user, the panel included, can reconfigure Caddy" \
      "add \"$CAD_ADMIN\" to the global options in $CADDY_DIR/Caddyfile, then systemctl restart caddy (install.sh creates /run/caddy via $CAD_DROPIN)"
  fi
  doc_need_root certificate dns || return 0
  local domain days
  domain="$(doc_domain)" || domain=""
  if [[ -z $domain ]]; then
    doc_result fail certificate "no public_url in $ETC_PANEL/panel.toml" "install.sh install with your --domain/--email"
    doc_result warn dns "no domain to check" "install.sh install with your --domain/--email"
    return 0
  fi
  if grep -qE '^[[:space:]]*tls[[:space:]]+internal([[:space:]]|$)' "$ROOT$CADDY_DIR/mc-panel.caddy" 2>/dev/null; then
    doc_result ok certificate "local certificate for $domain (tls internal)"
  else
    days="$(doc_probe_cert_days "$domain")" || days=""
    if ! [[ $days =~ ^-?[0-9]+$ ]]; then
      doc_result fail certificate "no certificate for $domain yet" "check DNS and port 80/443, then journalctl -u caddy"
    elif ((days < 0)); then
      doc_result fail certificate "the certificate for $domain has expired" "journalctl -u caddy (renewal failed?)"
    elif ((days < DOC_CERT_WARN_DAYS)); then
      doc_result warn certificate "the certificate for $domain expires in $days days" "journalctl -u caddy (Caddy renews 30 days before expiry)"
    else
      doc_result ok certificate "certificate for $domain valid for $days days"
    fi
  fi
  local addrs locals a match=0
  addrs="$(doc_probe_resolve "$domain")" || addrs=""
  locals="$(doc_probe_local_addrs)" || locals=""
  for a in $addrs; do
    if grep -qxF -- "$a" <<<"$locals"; then match=1; fi
  done
  if [[ $match == 1 ]]; then
    doc_result ok dns "$domain points to this host"
  else
    doc_result warn dns "$domain resolves to ${addrs:-nothing}, not to an address of this host" "set the A/AAAA record of $domain to this server (behind NAT this warning is expected)"
  fi
}

# doc_chk_caddy_package: Caddy from its own repository and not older than PKG_CADDY_MIN (SEC-01:
# Ubuntu's universe package gets no security updates).
doc_chk_caddy_package() {
  local v hint="install.sh update --force (switches Caddy to its repository)"
  v="$(doc_probe_caddy_version)" || v=""
  if [[ -z $v ]]; then
    doc_result warn caddy_package "cannot determine the Caddy version" "caddy version"
  elif ! version_ge "$v" "$PKG_CADDY_MIN"; then
    doc_result warn caddy_package "Caddy $v is older than ${PKG_CADDY_MIN%.0} (known vulnerabilities in its HTTP/2 and TLS stack)" "$hint"
  elif ! doc_probe_caddy_from_repo; then
    doc_result warn caddy_package "Caddy $v does not come from the Caddy repository (no automatic security updates)" "$hint"
  else
    doc_result ok caddy_package "Caddy $v from the Caddy repository"
  fi
}

# doc_chk_docker_dns: the egress filter (section A) drops container connections to private
# ranges, so container DNS breaks when Docker forwards to a resolver there: /etc/resolv.conf
# without the systemd-resolved stub (127.0.0.53, which Docker uses from the host's namespace)
# naming such a nameserver, or "dns" in /etc/docker/daemon.json.
doc_chk_docker_dns() {
  local deny ns stub=0 bad=() src=() d n
  deny="$(doc_egress_deny)"
  if [[ -z $deny ]]; then
    doc_result ok docker_dns "container DNS: the egress filter is off (container_egress_deny = [])"
    return 0
  fi
  ns="$(awk '$1 == "nameserver" { print $2 }' "$ROOT/etc/resolv.conf" 2>/dev/null)" || ns=""
  if grep -qx '127.0.0.53' <<<"$ns"; then stub=1; fi
  if [[ $stub == 0 ]]; then
    for n in $ns; do src+=("$n|/etc/resolv.conf"); done
  fi
  for n in $(grep -oE '"dns"[[:space:]]*:[[:space:]]*\[[^]]*\]' "$ROOT/etc/docker/daemon.json" 2>/dev/null | grep -oE '"[0-9A-Fa-f.:]+"' | tr -d '"'); do
    src+=("$n|/etc/docker/daemon.json")
  done
  for n in "${src[@]}"; do
    for d in $deny; do
      if doc_ip_in_cidr "${n%%|*}" "$d"; then
        bad+=("${n%%|*} (${n#*|}, in $d)")
        break
      fi
    done
  done
  if ((${#bad[@]} == 0)); then
    doc_result ok docker_dns "container DNS is not blocked by the egress filter"
  else
    doc_result warn docker_dns "container DNS would be blocked by the egress filter: ${bad[*]}" \
      "use the systemd-resolved stub (nameserver 127.0.0.53 in /etc/resolv.conf, no \"dns\" in /etc/docker/daemon.json) or remove the range from container_egress_deny in $ETC_PANEL/agent.toml"
  fi
}

# doc_age SECONDS: a duration as "3 h" or "5 days".
doc_age() {
  if (($1 < 7200)); then
    echo "$(($1 / 60)) min"
  elif (($1 < 172800)); then
    echo "$(($1 / 3600)) h"
  else
    echo "$(($1 / 86400)) days"
  fi
}

doc_chk_firewall() {
  doc_need_root firewall || return 0
  local table ports p blocked=()
  table="$(doc_probe_nft_table)" || table=""
  if [[ -z $table ]]; then
    doc_result warn firewall "host firewall (table inet mcpanel_fw) is not loaded" "install.sh install without --no-firewall"
    return 0
  fi
  ports="$(doc_probe_ssh_port)" || ports=""
  ports="${ports:-22}"
  for p in $ports; do
    grep -E 'tcp dport' <<<"$table" | grep -Eq "(^|[^0-9])$p([^0-9]|$)" || blocked+=("$p")
  done
  if ((${#blocked[@]} == 0)); then
    doc_result ok firewall "host firewall active, SSH port ${ports//$'\n'/, } allowed"
  else
    doc_result fail firewall "the host firewall does not allow the SSH port ${blocked[*]}" "install.sh install --ssh-port ${blocked[0]} with your --domain/--email"
  fi
}

doc_chk_system() {
  if doc_need_root docker docker_version docker_live_restore docker_isolation; then
    if doc_probe_docker; then
      doc_result ok docker "Docker is reachable"
    else
      doc_result fail docker "Docker is not reachable" "systemctl status docker"
    fi
    local dv lr
    dv="$(doc_probe_docker_version)" || dv=""
    if [[ -z $dv ]]; then
      doc_result warn docker_version "cannot determine the Docker version" "docker version"
    elif version_ge "$dv" "$DOC_DOCKER_MIN"; then
      doc_result ok docker_version "Docker $dv"
    else
      doc_result warn docker_version "Docker $dv is older than $DOC_DOCKER_MIN (Ubuntu 26.04's security update)" "apt-get install --only-upgrade docker.io (or docker-ce)"
    fi
    lr="$(doc_probe_docker_live_restore)" || lr=""
    if [[ $lr == true ]]; then
      doc_result ok docker_live_restore "Docker live-restore is active (a Docker restart keeps the servers running)"
    elif grep -qE '"live-restore"[[:space:]]*:[[:space:]]*true' -- "$ROOT/etc/docker/daemon.json" 2>/dev/null; then
      doc_result warn docker_live_restore "live-restore is set in /etc/docker/daemon.json but not active: the next Docker restart (a package update) stops every server" \
        "systemctl reload docker (dockerd reloads live-restore; the servers keep running)"
    else
      doc_result ok docker_live_restore "Docker live-restore is not configured (/etc/docker/daemon.json)"
    fi
    # SEC-03: every server has its own network; one of an older version leaves the shared
    # mcpanel-mc only at its next stop.
    local shared=() n
    while IFS= read -r n; do
      if [[ $n =~ ^mcpanel-mc-[0-9]+$ ]]; then shared+=("$n"); fi
    done < <(doc_probe_shared_servers || true)
    if ((${#shared[@]} == 0)); then
      doc_result ok docker_isolation "every Minecraft server is on its own network"
    else
      doc_result warn docker_isolation "${#shared[@]} Minecraft server(s) still share the network mcpanel-mc and can reach each other: ${shared[*]}" \
        "restart these servers once in the panel (they move to their own network at the next stop)"
    fi
  fi
  doc_chk_docker_dns
  local rv
  rv="$(doc_probe_restic_version)" || rv=""
  if [[ -n $rv ]] && version_ge "$rv" 0.17.0; then
    doc_result ok restic "restic $rv"
  else
    doc_result fail restic "restic ${rv:-is missing} (backups need 0.17 or newer)" "apt-get install restic"
  fi
  if doc_probe_pkg tzdata; then
    doc_result ok tzdata "tzdata is installed"
  else
    doc_result fail tzdata "tzdata is missing (backup schedules would fall back to UTC)" "apt-get install tzdata"
  fi
  if [[ $(doc_probe_ntp) == yes ]]; then
    doc_result ok time_sync "the system clock is synchronized"
  else
    doc_result warn time_sync "the system clock is not synchronized (two-factor codes and schedules need it)" "timedatectl set-ntp true"
  fi
  if doc_probe_unit_enabled unattended-upgrades.service; then
    doc_result ok upgrades "security updates are installed automatically"
  else
    doc_result warn upgrades "unattended-upgrades is not enabled" "install.sh install with your --domain/--email"
  fi
  local rr="$ROOT/run/reboot-required" t now pkgs
  if [[ -e $rr ]]; then
    t="$(doc_probe_mtime "$rr")" || t=""
    now="$(doc_probe_now)"
    pkgs="$(sort -u -- "$rr.pkgs" 2>/dev/null | head -n 5 | paste -sd' ' -)" || pkgs=""
    doc_result warn reboot_required "a reboot is pending${t:+ since $(doc_age $((now - t)))}${pkgs:+ ($pkgs)}: kernel and library fixes are not active yet" \
      "plan a reboot (the Minecraft servers stop meanwhile); unattended-upgrades never reboots on its own"
  else
    doc_result ok reboot_required "no reboot pending"
  fi
}

# doc_chk_storage: a full $INSTANCES_DIR must not take the panel's database or Docker (container
# layers, logs) down with it (SEC-04): it needs a file system of its own.
doc_chk_storage() {
  local mp free docker_root shared=() d
  mp="$(doc_probe_mountpoint "$INSTANCES_DIR")" || mp=""
  docker_root=""
  if doc_is_root; then
    docker_root="$(doc_probe_docker_root)" || docker_root=""
  fi
  for d in "$DATA_DIR" "${docker_root:-$DOC_DOCKER_ROOT}"; do
    if [[ -z $mp || "$(doc_probe_mountpoint "$d")" == "$mp" ]]; then
      shared+=("$d")
    fi
  done
  if ((${#shared[@]} == 0)); then
    doc_result ok storage_fs "$INSTANCES_DIR is on its own file system ($mp)"
  else
    doc_result warn storage_fs "$INSTANCES_DIR shares a file system (${mp:-?}) with ${shared[*]}" \
      "a server that fills $INSTANCES_DIR then also stops the panel's database or Docker; give $INSTANCES_DIR its own file system (docs/install.md)"
  fi
  free="$(doc_probe_free_percent)" || free=""
  if ! [[ $free =~ ^[0-9]+$ ]]; then
    doc_result warn storage_free "cannot determine the free space on $INSTANCES_DIR" "df -h $INSTANCES_DIR"
  elif ((free < 10)); then
    doc_result warn storage_free "only $free % free on $INSTANCES_DIR" "remove old backups or worlds"
  else
    doc_result ok storage_free "$free % free on $INSTANCES_DIR"
  fi
  doc_need_root backup_keys || return 0
  local keys="$ROOT$INSTANCES_DIR/backups/keys"
  if [[ -d $keys && ! -L $keys ]]; then
    doc_result ok backup_keys "backup keys present: include $INSTANCES_DIR/backups/keys in your own host backups"
  else
    doc_result ok backup_keys "no backup keys yet (they appear with the first backup)"
  fi
}

doc_chk_ssh() {
  doc_need_root ssh_password ssh_root ssh_kbdinteractive || return 0
  local cfg
  cfg="$(doc_probe_sshd)" || cfg=""
  if grep -qiE '^passwordauthentication[[:space:]]+yes' <<<"$cfg"; then
    doc_result warn ssh_password "SSH allows password logins (PasswordAuthentication yes)" "use SSH keys and set PasswordAuthentication no"
  else
    doc_result ok ssh_password "SSH password logins are off"
  fi
  if grep -qiE '^permitrootlogin[[:space:]]+yes' <<<"$cfg"; then
    doc_result warn ssh_root "SSH allows root logins with a password (PermitRootLogin yes)" "set PermitRootLogin prohibit-password or no"
  else
    doc_result ok ssh_root "SSH root login with a password is off"
  fi
  # keyboard-interactive through PAM asks for the password, too (ChallengeResponse before OpenSSH 8.7)
  if grep -qiE '^(kbdinteractive|challengeresponse)authentication[[:space:]]+yes' <<<"$cfg" &&
    grep -qiE '^usepam[[:space:]]+yes' <<<"$cfg"; then
    doc_result warn ssh_kbdinteractive "SSH allows password logins through PAM (KbdInteractiveAuthentication yes with UsePAM yes)" "set KbdInteractiveAuthentication no"
  else
    doc_result ok ssh_kbdinteractive "SSH keyboard-interactive (PAM password) logins are off"
  fi
}

# --- output --------------------------------------------------------------------------------

# doc_print prints the results (text or JSON) and returns 0, 1 or 2.
doc_print() {
  local i n=${#DOC_IDS[@]} worst=ok fails=0 warns=0 sym
  for ((i = 0; i < n; i++)); do
    case "${DOC_STATUS[i]}" in
    fail)
      fails=$((fails + 1))
      worst=fail
      ;;
    warn)
      warns=$((warns + 1))
      if [[ $worst != fail ]]; then worst=warn; fi
      ;;
    esac
  done
  if [[ $JSON == 1 ]]; then
    printf '{"status":"%s","checks":[' "$worst"
    for ((i = 0; i < n; i++)); do
      if ((i > 0)); then printf ','; fi
      printf '{"id":%s,"status":%s,"message":%s,"hint":%s}' \
        "$(doc_json_str "${DOC_IDS[i]}")" "$(doc_json_str "${DOC_STATUS[i]}")" \
        "$(doc_json_str "${DOC_MSG[i]}")" "$(doc_json_str "${DOC_HINT[i]}")"
    done
    printf ']}\n'
  else
    for ((i = 0; i < n; i++)); do
      if [[ $DOC_SUMMARY == 1 && ${DOC_STATUS[i]} == ok ]]; then
        continue
      fi
      case "${DOC_STATUS[i]}" in
      ok) sym="✓" ;;
      warn) sym="!" ;;
      *) sym="✗" ;;
      esac
      printf '%s %s\n' "$sym" "${DOC_MSG[i]}"
      if [[ -n ${DOC_HINT[i]} && ${DOC_STATUS[i]} != ok ]]; then
        printf '    -> %s\n' "${DOC_HINT[i]}"
      fi
    done
    printf '%d checks: %d failed, %d warnings\n' "$n" "$fails" "$warns"
    _log "doctor: $n checks, $fails failed, $warns warnings"
  fi
  case "$worst" in
  ok) return 0 ;;
  warn) return 1 ;;
  *) return 2 ;;
  esac
}

cmd_doctor() {
  DOC_IDS=()
  DOC_STATUS=()
  DOC_MSG=()
  DOC_HINT=()
  if [[ $JSON != 1 ]]; then
    log_step "doctor"
  fi
  doc_chk_services
  doc_chk_sockets
  doc_chk_files_process
  doc_chk_versions
  doc_chk_update_state
  doc_chk_config
  doc_chk_permissions
  doc_chk_caddy
  doc_chk_firewall
  doc_chk_system
  doc_chk_storage
  doc_chk_ssh
  local rc=0
  doc_print || rc=$?
  return "$rc"
}
