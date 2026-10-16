# shellcheck shell=bash
# uninstall.sh - install.sh uninstall [--purge] (installer spec §7): removes mc-panel's units,
# binaries, Caddy site, nftables tables, containers and sockets - only mc-panel objects, never
# packages. Worlds, backups, the database, the rollback copies, the configuration and the users
# stay unless --purge, which needs the panel domain typed in on the terminal (also with --yes).
# cmd_uninstall returns 0 (removed) or 1 (aborted).

UNI_IMPORT_LINE='import mc-panel.caddy # managed by mc-panel'
# Stop order: the panel first, the agent last (it must not recreate containers meanwhile); a
# socket before its service, or socket activation would start the service again.
UNI_UNITS=(mcpanel-panel.socket mcpanel-panel.service mcpanel-files.socket mcpanel-files.service
  mcpanel-agent.service mcpanel-fw.service)
UNI_IMPORT_RE='^[[:space:]]*import[[:space:]]+mc-panel\.caddy([[:space:]]|$)'
UNI_FAILED=() # what --purge could not delete
UNI_TABLES=(mcpanel_fw mcpanel)
UNI_SOCKETS=(/run/mc-panel-web/panel.sock /run/mc-panel-files/files.sock /run/mc-panel/agent.sock)

# --- probes (tests redefine these) ---------------------------------------------------------

uni_probe_containers() { docker ps -aq --filter label=mcpanel.instance 2>/dev/null </dev/null; }
uni_probe_running_containers() { docker ps -q --filter label=mcpanel.instance 2>/dev/null </dev/null; }
# The router and the network only count as ours with the labels the agent sets (template.go).
uni_probe_has_router() {
  [[ $(docker container inspect -f '{{index .Config.Labels "mcpanel.router"}}' mcpanel-router 2>/dev/null </dev/null) == 1 ]]
}
uni_probe_has_network() {
  [[ $(docker network inspect -f '{{index .Labels "mcpanel.network"}}' mcpanel-mc 2>/dev/null </dev/null) == 1 ]]
}
# The per-server networks mcpanel-net-<id> (one per Minecraft server) carry the same label.
uni_probe_instance_networks() { docker network ls --filter label=mcpanel.network --format '{{.Name}}' 2>/dev/null </dev/null; }
uni_probe_has_table() { nft list table inet "$1" >/dev/null 2>&1 </dev/null; }
uni_probe_user_exists() { getent passwd "$1" >/dev/null 2>&1; }
uni_probe_mountpoint() { mountpoint -q -- "$1" 2>/dev/null; }
uni_userdel() { sys_userdel "$1"; }

# uni_read_tty PROMPT: prints PROMPT on the terminal and the line typed there; 1 without a
# terminal. Like confirm (lib.sh) only through $TTY: stdin is the script with curl | bash.
uni_read_tty() {
  local line=""
  if ! { [[ -r $TTY && -w $TTY ]] && printf '%s' "$1" >>"$TTY" && IFS= read -r line <"$TTY"; } 2>/dev/null; then
    return 1
  fi
  printf '%s\n' "$line"
}

# --- steps ---------------------------------------------------------------------------------

# uni_purge_word: what --purge wants typed - the panel domain (host of public_url), "mc-panel"
# when there is none.
uni_purge_word() {
  local d
  d="$(doc_domain)" || d=""
  printf '%s\n' "${d:-mc-panel}"
}

# uni_confirm: a plain uninstall asks once (YES=1 answers); --purge always needs the word typed.
uni_confirm() {
  if [[ $PURGE == 1 ]]; then
    local word typed
    word="$(uni_purge_word)"
    warn "--purge deletes all worlds, backups, backup keys, the panel database, the rollback copies, the configuration and the users mcpanel and mcpanel-mc."
    if ! typed="$(uni_read_tty "Type \"$word\" to delete everything: ")"; then
      warn "no terminal to confirm --purge (--yes does not count); nothing was removed"
      return 1
    fi
    if [[ $typed != "$word" ]]; then
      warn "the confirmation did not match \"$word\"; nothing was removed"
      return 1
    fi
    return 0
  fi
  if ! confirm "Remove mc-panel? Worlds, backups, database and configuration stay."; then
    warn "aborted; nothing was removed (--yes answers yes)"
    return 1
  fi
}

uni_units() {
  local u removed=0
  for u in "${UNI_UNITS[@]}"; do
    [[ -f $ROOT$UNIT_DIR/$u || -L $ROOT$UNIT_DIR/$u ]] || continue
    sys_systemctl stop "$u" </dev/null || true
    sys_systemctl disable "$u" </dev/null || true
    rm -f -- "$ROOT$UNIT_DIR/$u"
    log_changed "removed $u"
    removed=1
  done
  if [[ $removed == 1 ]]; then
    sys_systemctl daemon-reload || true
    sys_systemctl reset-failed "${UNI_UNITS[@]}" || true
  else
    log_skip "no mc-panel units"
  fi
}

uni_binaries() {
  local b removed=()
  for b in "${UPD_BINARIES[@]}"; do
    [[ -e $ROOT$BIN_DIR/$b || -L $ROOT$BIN_DIR/$b ]] || continue
    rm -f -- "$ROOT$BIN_DIR/$b"
    removed+=("$b")
  done
  if ((${#removed[@]} > 0)); then
    log_changed "removed ${removed[*]} from $BIN_DIR"
  fi
}

# uni_caddy removes exactly the marked import line (other sites stay) and mc-panel.caddy. An
# import line of the operator (unmarked) stays; mc-panel.caddy then becomes a comment-only file,
# so Caddy still loads. The configuration is validated as caddy before the reload. Like the
# Caddy package, its repository and the Caddyfile, the drop-in caddy.service.d/mc-panel.conf
# stays: a Caddyfile of the installer keeps its admin socket in /run/caddy. So does mc-panel.d/ with
# the operator's additions to the site.
uni_caddy() {
  local file="$CADDY_DIR/Caddyfile" site="$CADDY_DIR/mc-panel.caddy" touched=0 mode og content foreign=0
  if [[ -f $ROOT$file && ! -L $ROOT$file ]] && grep -qxF -- "$UNI_IMPORT_LINE" "$ROOT$file"; then
    mode="$(stat -c '%a' -- "$ROOT$file")"
    og="$(_owner_get "$file")"
    content="$(grep -vxF -- "$UNI_IMPORT_LINE" "$ROOT$file")" || content=""
    if [[ -n $content ]]; then
      printf '%s\n' "$content" | write_file "$file" "0$mode" "${og%%:*}" "${og#*:}" >/dev/null
    else
      write_file --allow-empty "$file" "0$mode" "${og%%:*}" "${og#*:}" </dev/null >/dev/null
    fi
    touched=1
  fi
  if [[ -f $ROOT$file && ! -L $ROOT$file ]] && grep -vxF -- "$UNI_IMPORT_LINE" "$ROOT$file" | grep -qE "$UNI_IMPORT_RE"; then
    foreign=1
  fi
  if [[ $foreign == 1 ]]; then
    if [[ -e $ROOT$site || -L $ROOT$site ]] && ! grep -q '^# mc-panel was uninstalled' "$ROOT$site" 2>/dev/null; then
      printf '%s\n' "# mc-panel was uninstalled; remove \"import mc-panel.caddy\" from $file and then this file." |
        write_file "$site" 0644 root root >/dev/null
      touched=1
    fi
    warn "$file still imports mc-panel.caddy (a line not written by install.sh); $site is now empty - remove that line and the file yourself"
  elif [[ -e $ROOT$site || -L $ROOT$site ]]; then
    rm -f -- "$ROOT$site"
    touched=1
  fi
  [[ $touched == 1 ]] || return 0
  log_changed "removed the panel site from Caddy"
  # as caddy with its own HOME, like install (caddy.sh)
  if ! run_as caddy env HOME=/var/lib/caddy caddy validate --config "$file" --adapter caddyfile </dev/null >/dev/null 2>&1; then
    warn "caddy rejects $file now; Caddy was not reloaded - fix the file, then: systemctl reload caddy"
    return 0
  fi
  sys_systemctl reload caddy.service </dev/null || warn "reloading Caddy failed (systemctl status caddy)"
}

uni_firewall() {
  local t
  for t in "${UNI_TABLES[@]}"; do
    uni_probe_has_table "$t" || continue
    sys_nft delete table inet "$t" || warn "could not delete the nftables table inet $t"
    log_changed "removed the nftables table inet $t"
  done
}

# uni_docker stops the Minecraft containers (label mcpanel.instance) with time to save the
# worlds and removes them, then the router and the network (only with the agent's labels)
# directly: the agent is stopped by now. Other containers are never touched.
uni_docker() {
  local ids=() running=() nets=() id
  while IFS= read -r id; do
    if [[ $id =~ ^[0-9a-f]{12,64}$ ]]; then running+=("$id"); fi
  done < <(uni_probe_running_containers || true)
  while IFS= read -r id; do
    if [[ $id =~ ^[0-9a-f]{12,64}$ ]]; then ids+=("$id"); fi
  done < <(uni_probe_containers || true)
  if ((${#running[@]} > 0)); then
    # up to 60 s to save the worlds, as the agent gives them
    if sys_docker stop -t 60 "${running[@]}" </dev/null; then
      log_changed "stopped ${#running[@]} Minecraft server(s)"
    else
      warn "could not stop all Minecraft servers; they are removed anyway"
    fi
  fi
  if ((${#ids[@]} > 0)); then
    sys_docker rm -f "${ids[@]}" </dev/null || warn "could not remove all Minecraft containers"
    log_changed "removed ${#ids[@]} Minecraft container(s); their data stays in $INSTANCES_DIR/instances"
  fi
  if uni_probe_has_router; then
    sys_docker rm -f mcpanel-router </dev/null || warn "could not remove mcpanel-router"
    log_changed "removed the container mcpanel-router"
  fi
  while IFS= read -r id; do
    if [[ $id =~ ^mcpanel-net-[0-9]+$ ]]; then nets+=("$id"); fi
  done < <(uni_probe_instance_networks || true)
  if ((${#nets[@]} > 0)); then
    sys_docker network rm "${nets[@]}" </dev/null || warn "could not remove all server networks"
    log_changed "removed ${#nets[@]} server network(s) mcpanel-net-<id>"
  fi
  if uni_probe_has_network; then
    sys_docker network rm mcpanel-mc </dev/null || warn "could not remove the network mcpanel-mc"
    log_changed "removed the network mcpanel-mc"
  fi
}

# uni_sockets removes leftover socket files and their (then empty) directories.
uni_sockets() {
  local s
  for s in "${UNI_SOCKETS[@]}"; do
    if [[ -S $ROOT$s || -L $ROOT$s ]]; then
      rm -f -- "$ROOT$s"
    fi
    if [[ -d $ROOT${s%/*} && ! -L $ROOT${s%/*} ]]; then
      rmdir -- "$ROOT${s%/*}" 2>/dev/null || true
    fi
  done
}

# uni_rm_tree PATH removes one of mc-panel's own trees (PATH without $ROOT) and nothing else; it
# does not cross into other file systems below it. A mount point itself stays (its content
# goes). A failure is recorded in UNI_FAILED.
uni_rm_tree() {
  case "$1" in
  "$INSTANCES_DIR" | "$DATA_DIR" | "$ROLLBACK_DIR" | "$ETC_PANEL" | "$ETC_FILES") ;;
  *) die "refusing to delete $1" ;;
  esac
  local p="$ROOT$1"
  [[ -e $p || -L $p ]] || return 0
  if [[ -d $p && ! -L $p ]] && uni_probe_mountpoint "$p"; then
    if find "$p" -mindepth 1 -maxdepth 1 -exec rm -rf --one-file-system -- {} + && [[ -z $(find "$p" -mindepth 1 -maxdepth 1 -print -quit) ]]; then
      log_changed "deleted the content of $1 (a mount point; it stays)"
    else
      warn "could not delete everything in $1"
      UNI_FAILED+=("$1")
    fi
    return 0
  fi
  if rm -rf --one-file-system -- "$p" && [[ ! -e $p && ! -L $p ]]; then
    log_changed "deleted $1"
  else
    warn "could not delete $1 completely"
    UNI_FAILED+=("$1")
  fi
}

uni_purge() {
  local d u
  UNI_FAILED=()
  for d in "$INSTANCES_DIR" "$DATA_DIR" "$ROLLBACK_DIR" "$ETC_PANEL" "$ETC_FILES"; do
    uni_rm_tree "$d"
  done
  for u in mcpanel-mc mcpanel; do
    uni_probe_user_exists "$u" || continue
    if uni_userdel "$u" </dev/null; then
      log_changed "deleted the user $u"
    else
      warn "could not delete the user $u"
      UNI_FAILED+=("user $u")
    fi
  done
}

cmd_uninstall() {
  log_step "uninstall"
  uni_confirm || return 1
  uni_units
  uni_binaries
  uni_caddy
  uni_firewall
  uni_docker
  uni_sockets
  if [[ $PURGE == 1 ]]; then
    uni_purge
    if ((${#UNI_FAILED[@]} > 0)); then
      warn "mc-panel was removed, but --purge could not delete: ${UNI_FAILED[*]} (see the messages above)"
      return 1
    fi
    log_ok "mc-panel and all its data were removed (packages stay installed)"
  else
    log_ok "mc-panel was removed; kept: $INSTANCES_DIR (worlds, backups, keys), $DATA_DIR (database), $ROLLBACK_DIR, $ETC_PANEL, $ETC_FILES and the users mcpanel, mcpanel-mc"
  fi
  return 0
}
