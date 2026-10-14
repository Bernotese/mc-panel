# shellcheck shell=bash
# doctor.sh - install.sh doctor (installer spec §6): one result per check (✓ ok, ! warn,
# ✗ fail) with a one-line hint. cmd_doctor returns 0 (all ok), 1 (warnings) or 2 (failures)
# instead of exiting (main uses it as exit code); --json (JSON=1) prints
# {"status":"ok|warn|fail","checks":[{"id","status","message","hint"}]} (contract for the CI);
# DOC_SUMMARY=1 (install's summary) prints only what is not ok. Without root, the checks that
# need root report "warn: skipped".
#
# Check ids (stable): services sockets files_process versions update_available update_state
# config permissions caddy_config caddy_import certificate dns firewall docker restic tzdata
# time_sync upgrades storage_fs storage_free backup_keys ssh_password ssh_root

DOC_IDS=()
DOC_STATUS=()
DOC_MSG=()
DOC_HINT=()
DOC_UNITS=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket
  mcpanel-panel.service caddy.service)
DOC_IMPORT_LINE='import mc-panel.caddy # managed by mc-panel'
DOC_CERT_WARN_DAYS=14

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
doc_probe_restic_version() { restic version 2>/dev/null | awk '{ print $2; exit }'; }
doc_probe_pkg() { dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q 'install ok installed'; }
doc_probe_ntp() { timedatectl show -p NTPSynchronized --value 2>/dev/null; }
doc_probe_mountpoint() { findmnt -n -o TARGET --target "$INSTANCES_DIR" 2>/dev/null; }
# doc_probe_free_percent: free space on INSTANCES_DIR in percent (df reports the used share).
doc_probe_free_percent() {
  local used
  used="$(df --output=pcent -- "$INSTANCES_DIR" 2>/dev/null | tail -n 1 | tr -dc '0-9')" || used=""
  [[ -n $used ]] || return 0
  echo $((100 - used))
}
doc_probe_sshd() { sshd -T 2>/dev/null; }

# --- helpers -------------------------------------------------------------------------------

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
  if doc_need_root docker; then
    if doc_probe_docker; then
      doc_result ok docker "Docker is reachable"
    else
      doc_result fail docker "Docker is not reachable" "systemctl status docker"
    fi
  fi
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
}

doc_chk_storage() {
  local mp free
  mp="$(doc_probe_mountpoint)" || mp=""
  if [[ $mp == "$INSTANCES_DIR" ]]; then
    doc_result ok storage_fs "$INSTANCES_DIR is its own file system"
  else
    doc_result warn storage_fs "$INSTANCES_DIR is not its own file system (shared with ${mp:-?})" "a full Minecraft server can then fill the disk of the panel; see the instances spec §3.3"
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
  doc_need_root ssh_password ssh_root || return 0
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
