# shellcheck shell=bash
# update.sh - install.sh update (installer spec §5): target version, hand-over to the target's
# install.sh, backup of DB, binaries and units, swap, health check, automatic rollback.
# cmd_update returns 0 (up to date / updated), 1 (error before any change), 3 (rolled back)
# or 4 (rollback failed).
#
# Every rollback dir $ROLLBACK_DIR/<old version>-<UTC time>/ holds bin/, units/, panel.db and
# a state file (phase=backed_up|applying|verified|rolled_back|rollback_failed, from=, to=). A
# run that stopped in phase applying (power loss, kill -9) is rolled back first by the next
# update; doctor reports it.

UPD_KEEP=3
UPD_HEALTH_SECS=90
UPD_API_LATEST="https://api.github.com/repos/Bernotese/mc-panel/releases/latest"
UPD_RELEASE_BASE="$RELEASE_BASE/download"
UPD_UNITS=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket
  mcpanel-panel.service mcpanel-fw.service)
UPD_BINARIES=(panel agent files)
# Restart order of spec §5 step 4: agent, files, panel (sockets before their services).
UPD_RESTART=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket
  mcpanel-panel.service)
UPD_STOP=(mcpanel-panel.service mcpanel-panel.socket mcpanel-files.service mcpanel-files.socket
  mcpanel-agent.service)
UPD_DIR=""     # rollback dir of this run (with $ROOT)
UPD_FROM=""    # installed version before the swap
UPD_SIGNALED=0 # INT/TERM/HUP arrived during the swap or the rollback
UPD_SWAP_RC=0  # exit code of the swap subshell
UPD_APPLY_OK=0 # 1 when the target version is installed and healthy

# --- probes (tests redefine these) ---------------------------------------------------------

# upd_fetch_latest_json DEST: the latest-release JSON of the GitHub API. Short timeouts and no
# die: doctor calls this offline, too.
upd_fetch_latest_json() {
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL --connect-timeout 5 --max-time 15 \
    -H 'Accept: application/vnd.github+json' -o "$1" -- "$UPD_API_LATEST" 2>/dev/null
}

# upd_probe_health: 0 when panel, agent and files answer on their sockets (the agent and the
# files service accept root).
upd_probe_health() {
  curl -fsS --max-time 5 --unix-socket /run/mc-panel-web/panel.sock http://panel/api/health >/dev/null 2>&1 &&
    curl -fsS --max-time 5 --unix-socket /run/mc-panel/agent.sock http://agent/v1/health 2>/dev/null | grep -q '"status":"ok"' &&
    curl -fsS --max-time 5 --unix-socket /run/mc-panel-files/files.sock http://files/v1/health 2>/dev/null | grep -q '"status":"ok"'
}

# upd_db_backup DEST: consistent copy of the running panel's DB (sqlite online backup) into DEST
# in the root-only rollback dir. Everything below DATA_DIR runs as mcpanel (the copy, reading it,
# removing it): root never opens a path mcpanel controls, so a compromised panel cannot plant a
# symlink or a file that root installs later (overview, Global Constraints).
upd_db_backup() {
  local tmp st
  tmp="$DATA_DIR/.rollback-$(date -u +%Y%m%dT%H%M%SZ)-$$.db"
  run_as mcpanel sqlite3 -bail -cmd '.timeout 10000' "$DATA_DIR/panel.db" ".backup '$tmp'" || return 1
  # lstat only (stat without -L): a regular file of mcpanel, no symlink (spec §5 step 3)
  st="$(stat -c '%F %U' -- "$ROOT$tmp" 2>/dev/null)" || st=""
  if [[ $st != "regular file mcpanel" ]]; then
    run_as mcpanel rm -f -- "$tmp" || true
    return 1
  fi
  if ! run_as mcpanel cat -- "$tmp" >"$1"; then
    run_as mcpanel rm -f -- "$tmp" || true
    return 1
  fi
  run_as mcpanel rm -f -- "$tmp" || true
  [[ -s $1 ]]
}

# upd_db_restore SRC: puts SRC (in the rollback dir) back as the panel DB; mcpanel writes it, so
# root follows no symlink below DATA_DIR. The WAL of the failed version goes first.
upd_db_restore() {
  run_as mcpanel rm -f -- "$DATA_DIR/panel.db-wal" "$DATA_DIR/panel.db-shm" || return 1
  # shellcheck disable=SC2016 # $1 is expanded by the inner sh
  run_as mcpanel sh -c 'umask 077 && cat >"$1.restore" && sync -- "$1.restore" && mv -f -- "$1.restore" "$1"' \
    sh "$DATA_DIR/panel.db" <"$1"
}

# upd_free_bytes DIR: free bytes on DIR's file system.
upd_free_bytes() {
  df -P -B1 --output=avail -- "$1" 2>/dev/null | tail -n 1 | tr -d ' '
}

upd_exec() { exec "$@"; }

# --- versions ------------------------------------------------------------------------------

upd_strip_v() { printf '%s\n' "${1#v}"; }

# upd_installed_version: version of the installed panel binary, "none" without one.
upd_installed_version() {
  local v=""
  if [[ -x $ROOT$BIN_DIR/panel ]]; then
    v="$("$ROOT$BIN_DIR/panel" version 2>/dev/null)" || v=""
  fi
  v="${v#v}"
  printf '%s\n' "${v:-none}"
}

# upd_binary_versions: versions of panel, agent and files, space-separated ("?" if one fails).
upd_binary_versions() {
  local p a f
  p="$("$ROOT$BIN_DIR/panel" version 2>/dev/null)" || p=""
  a="$("$ROOT$BIN_DIR/agent" -version 2>/dev/null)" || a=""
  f="$("$ROOT$BIN_DIR/files" -version 2>/dev/null)" || f=""
  p="${p#v}"
  a="${a#v}"
  f="${f#v}"
  printf '%s %s %s\n' "${p:-?}" "${a:-?}" "${f:-?}"
}

# upd_latest_tag: tag of the newest release (vX.Y.Z); returns 1 (never dies) when the API is
# unreachable or answers something else, so doctor can use it offline.
upd_latest_tag() {
  local json="$WORK/latest.json" tag
  upd_fetch_latest_json "$json" || return 1
  tag="$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$json" 2>/dev/null | head -n 1)" || tag=""
  [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  printf '%s\n' "$tag"
}

# --- rollback dirs -------------------------------------------------------------------------

# upd_state_get DIR KEY: value of KEY in DIR/state ("" if missing).
upd_state_get() {
  [[ -f $1/state ]] || return 0
  awk -F= -v k="$2" '$1 == k { print substr($0, length(k) + 2); exit }' "$1/state"
}

# upd_state_write DIR PHASE FROM TO: replaces DIR/state atomically and durably (a power loss
# leaves the old or the new state, never a torn one).
upd_state_write() {
  printf 'phase=%s\nfrom=%s\nto=%s\n' "$2" "$3" "$4" >"$1/state.tmp"
  sync -- "$1/state.tmp"
  mv -f -- "$1/state.tmp" "$1/state"
  sync -- "$1"
}

upd_state_set_phase() {
  upd_state_write "$1" "$2" "$(upd_state_get "$1" from)" "$(upd_state_get "$1" to)"
}

# upd_unfinished_dirs: rollback dirs whose update never finished (applying) or whose rollback
# failed, one per line; doctor reports both.
upd_unfinished_dirs() {
  local d
  [[ -d $ROOT$ROLLBACK_DIR && ! -L $ROOT$ROLLBACK_DIR ]] || return 0
  for d in "$ROOT$ROLLBACK_DIR"/*/; do
    [[ -d $d ]] || continue
    d="${d%/}"
    case "$(upd_state_get "$d" phase)" in
    applying | rollback_failed) printf '%s\n' "$d" ;;
    esac
  done
}

# upd_prune keeps the UPD_KEEP newest rollback dirs (by mtime); a dir in phase applying or
# rollback_failed is never removed: it may be the only copy of the old state.
upd_prune() {
  local d keep=0
  [[ -d $ROOT$ROLLBACK_DIR ]] || return 0
  while IFS= read -r d; do
    [[ -n $d ]] || continue
    case "$(upd_state_get "$d" phase)" in
    applying | rollback_failed) continue ;;
    esac
    keep=$((keep + 1))
    if ((keep > UPD_KEEP)); then
      rm -rf -- "$d"
      log_changed "removed old rollback copy ${d##*/}"
    fi
  done < <(
    # shellcheck disable=SC2012 # names are ours: <version>-<UTC time>, no blanks or newlines
    ls -1dt -- "$ROOT$ROLLBACK_DIR"/*/ 2>/dev/null | sed 's:/$::'
  )
}

# --- steps ---------------------------------------------------------------------------------

# upd_context fills DOMAIN, TLS_MODE and EMAIL from the installed configuration: cfg_ensure
# (B1) renders the KEY_* from them and must never add an empty public_url.
# shellcheck disable=SC2034 # DOMAIN, TLS_MODE and EMAIL are read by config.sh (B1)
upd_context() {
  if [[ -z $DOMAIN ]]; then
    DOMAIN="$(doc_domain)" || DOMAIN=""
  fi
  [[ -n $DOMAIN ]] || die "no public_url in $ETC_PANEL/panel.toml; run install.sh install first"
  local site="$ROOT$CADDY_DIR/mc-panel.caddy" tls=""
  if [[ -f $site ]]; then
    tls="$(awk '$1 == "tls" { print $2; exit }' "$site")" || tls=""
  fi
  if [[ $tls == internal ]]; then
    TLS_MODE=internal
  elif [[ -n $tls ]]; then
    TLS_MODE=letsencrypt
    EMAIL="${EMAIL:-$tls}"
  fi
}

# upd_backup copies DB, binaries and units into a new rollback dir (phase backed_up). Dies
# before any change when the free space or the DB copy is not enough.
upd_backup() {
  local db="$ROOT$DATA_DIR/panel.db" need free b u size=0 dir
  UPD_FROM="$(upd_installed_version)"
  for b in "${UPD_BINARIES[@]}"; do
    [[ -f $ROOT$BIN_DIR/$b ]] || die "$BIN_DIR/$b is missing; run install.sh install first"
  done
  # root:root 0700 outside DATA_DIR (overview, Global Constraints); ensure_dir refuses a symlink
  ensure_dir "$ROLLBACK_DIR" 0700 root root >/dev/null
  if [[ -f $db && ! -L $db ]]; then
    size="$(stat -c %s -- "$db")"
  fi
  # the DB twice (sqlite's copy below DATA_DIR and ours), binaries and units, plus a margin
  need=$((size * 22 / 10 + 200 * 1024 * 1024))
  for dir in "$ROOT$ROLLBACK_DIR" "$ROOT$DATA_DIR"; do
    [[ -d $dir ]] || continue
    free="$(upd_free_bytes "$dir")" || free=""
    [[ $free =~ ^[0-9]+$ ]] || die "cannot determine the free space in ${dir#"$ROOT"}; nothing was changed"
    ((free >= need)) || die "not enough free space for the backup in ${dir#"$ROOT"} ($need bytes needed, $free free); nothing was changed"
  done
  UPD_DIR="$ROOT$ROLLBACK_DIR/$UPD_FROM-$(date -u +%Y%m%dT%H%M%SZ)"
  [[ ! -e $UPD_DIR ]] || die "rollback dir ${UPD_DIR##*/} already exists; try again in a second"
  mkdir -m 0700 -- "$UPD_DIR" "$UPD_DIR/bin" "$UPD_DIR/units"
  if [[ -e $db || -L $db ]]; then
    if ! upd_db_backup "$UPD_DIR/panel.db"; then
      rm -rf -- "$UPD_DIR"
      die "database backup failed (disk full?); nothing was changed"
    fi
  fi
  for b in "${UPD_BINARIES[@]}"; do
    cp -p -- "$ROOT$BIN_DIR/$b" "$UPD_DIR/bin/$b"
  done
  for u in "${UPD_UNITS[@]}"; do
    if [[ -f $ROOT$UNIT_DIR/$u ]]; then
      cp -p -- "$ROOT$UNIT_DIR/$u" "$UPD_DIR/units/$u"
    fi
  done
  # The copies must be on disk before phase applying says they can be put back.
  sync
  upd_state_write "$UPD_DIR" backed_up "$UPD_FROM" "$(upd_strip_v "$TARGET_VERSION")"
  log_ok "backup of $UPD_FROM in $ROLLBACK_DIR/${UPD_DIR##*/}"
}

# upd_restart_all: daemon-reload, then agent -> files -> panel; 1 if one of them failed.
upd_restart_all() {
  local u rc=0
  sys_systemctl daemon-reload || rc=1
  for u in "${UPD_RESTART[@]}"; do
    sys_systemctl restart "$u" || rc=1
  done
  return "$rc"
}

upd_units_active() {
  local u
  for u in mcpanel-agent.service mcpanel-files.service mcpanel-panel.service; do
    systemd_wait_active "$u" 1 || return 1
  done
}

# upd_healthy VERSION: within UPD_HEALTH_SECS all three binaries report VERSION, the services
# are active and the health endpoints answer (spec §5 step 5).
upd_healthy() {
  local ver="$1" deadline
  deadline=$(($(date +%s) + UPD_HEALTH_SECS))
  while :; do
    if [[ $(upd_binary_versions) == "$ver $ver $ver" ]] && upd_units_active && upd_probe_health; then
      return 0
    fi
    (($(date +%s) < deadline)) || return 1
    sleep 2
  done
}

# upd_rollback DIR puts the binaries, units and DB of DIR back and checks the old version:
# 0 = rolled back, 1 = rollback failed (the services stay stopped). Callers call it in a
# conditional context on purpose: every step is best effort, the health check decides.
upd_rollback() {
  local dir="$1" from b u
  from="$(upd_state_get "$dir" from)"
  warn "rolling back to $from"
  for u in "${UPD_STOP[@]}"; do
    sys_systemctl stop "$u" || true
  done
  for b in "${UPD_BINARIES[@]}"; do
    if [[ -f $dir/bin/$b ]]; then
      install -m 0755 -- "$dir/bin/$b" "$ROOT$BIN_DIR/.$b.rollback" &&
        mv -f -- "$ROOT$BIN_DIR/.$b.rollback" "$ROOT$BIN_DIR/$b"
    fi
  done
  for u in "${UPD_UNITS[@]}"; do
    if [[ -f $dir/units/$u ]]; then
      cp -p -- "$dir/units/$u" "$ROOT$UNIT_DIR/.$u.rollback" &&
        mv -f -- "$ROOT$UNIT_DIR/.$u.rollback" "$ROOT$UNIT_DIR/$u"
    fi
  done
  if [[ -f $dir/panel.db ]]; then
    upd_db_restore "$dir/panel.db" || warn "could not put the database back"
  fi
  if upd_restart_all && upd_healthy "$from"; then
    upd_state_set_phase "$dir" rolled_back
    return 0
  fi
  for u in "${UPD_STOP[@]}"; do
    sys_systemctl stop "$u" || true
  done
  upd_state_set_phase "$dir" rollback_failed
  return 1
}

upd_on_signal() { UPD_SIGNALED=1; }

# upd_on_hup: the terminal is gone (SSH dropped); keep going and write to the log only, so a
# failing write to the dead terminal cannot stop the rollback.
upd_on_hup() {
  UPD_SIGNALED=1
  if [[ -w $ROOT$LOG_FILE && ! -L $ROOT$LOG_FILE ]]; then
    exec >>"$ROOT$LOG_FILE" 2>&1
  else
    exec >/dev/null 2>&1
  fi
}

upd_trap_signals() {
  trap upd_on_signal INT TERM
  trap upd_on_hup HUP
}

# upd_swap runs the B1 steps bin -> cfg -> unit (cfg_ensure sets the KEY_* the unit templates
# need) in a subshell with errexit on, outside any conditional context, so any failing command
# stops it (overview V4). The result goes to UPD_SWAP_RC; the function itself always returns 0,
# so the caller's errexit never ends the run before the rollback.
upd_swap() {
  local e=0
  if [[ $- == *e* ]]; then e=1; fi
  set +e
  (
    set -e
    bin_install
    cfg_ensure
    unit_ensure
  )
  UPD_SWAP_RC=$?
  if [[ $e == 1 ]]; then set -e; fi
  return 0
}

# upd_apply TARGET swaps in the target version; UPD_APPLY_OK=1 when it is healthy. Callers must
# not call it in a conditional context (that would switch errexit off in upd_swap's subshell).
upd_apply() {
  local target="$1"
  UPD_APPLY_OK=0
  upd_state_set_phase "$UPD_DIR" applying
  upd_swap
  if [[ $UPD_SWAP_RC != 0 ]]; then
    warn "installing $target failed (exit $UPD_SWAP_RC)"
  elif [[ $UPD_SIGNALED == 0 ]]; then
    if ! upd_restart_all; then
      warn "restarting the services failed"
    elif [[ $UPD_SIGNALED == 0 ]] && upd_healthy "$target"; then
      UPD_APPLY_OK=1
    elif [[ $UPD_SIGNALED == 0 ]]; then
      warn "$target is not healthy after ${UPD_HEALTH_SECS}s (versions: $(upd_binary_versions))"
    fi
  fi
  if [[ $UPD_SIGNALED == 1 ]]; then
    warn "update interrupted"
    UPD_APPLY_OK=0
  fi
  return 0
}

# upd_handover downloads and verifies the target's install.sh and execs it (spec §5 step 2).
upd_handover() {
  local tag script="$WORK/install.sh" sums="$WORK/checksums.txt"
  tag="$(release_tag "$TARGET_VERSION")"
  fetch "$UPD_RELEASE_BASE/$tag/checksums.txt" "$sums"
  fetch "$UPD_RELEASE_BASE/$tag/install.sh" "$script"
  sha256_verify "$script" "$sums" install.sh
  local args=(update --version "$tag")
  if [[ $FORCE == 1 ]]; then args+=(--force); fi
  if [[ $YES == 1 ]]; then args+=(--yes); fi
  log_ok "handing over to install.sh $tag"
  # setup_work in the child takes WORK over and removes it (overview V4)
  export MCPANEL_UPDATE_HANDOFF=1 MCPANEL_HANDOFF_WORK="$WORK"
  upd_exec bash "$script" "${args[@]}"
}

# upd_finish_unfinished rolls back every run that stopped in phase applying; 1 if that failed.
upd_finish_unfinished() {
  local d
  while IFS= read -r d; do
    [[ -n $d ]] || continue
    case "$(upd_state_get "$d" phase)" in
    applying)
      warn "a previous update did not finish (${d##*/}); rolling it back first"
      if ! upd_rollback "$d"; then
        warn "the rollback of the unfinished update failed; the old state is in $ROLLBACK_DIR/${d##*/}"
        return 1
      fi
      log_ok "unfinished update ${d##*/} rolled back"
      ;;
    rollback_failed)
      warn "an earlier rollback failed ($ROLLBACK_DIR/${d##*/}); its copy is kept"
      ;;
    esac
  done < <(upd_unfinished_dirs)
  return 0
}

upd_failed_rollback_help() {
  warn "update to $1 failed AND the rollback to $UPD_FROM failed."
  warn "All mc-panel services are stopped. The old binaries, units and database are in $ROLLBACK_DIR/${UPD_DIR##*/}:"
  warn "  install -m 0755 bin/* $BIN_DIR/ && cp units/* $UNIT_DIR/"
  warn "  rm -f $DATA_DIR/panel.db-wal $DATA_DIR/panel.db-shm && runuser -u mcpanel -- sh -c 'cat >$DATA_DIR/panel.db' <panel.db"
  warn "  systemctl daemon-reload && systemctl start mcpanel-agent.service mcpanel-files.socket mcpanel-panel.socket"
  warn "Details: $LOG_FILE"
}

cmd_update() {
  log_step "update"
  local installed target rc=0
  UPD_SIGNALED=0
  upd_trap_signals
  upd_finish_unfinished || rc=4
  trap - INT TERM HUP
  if [[ $rc != 0 ]]; then
    return "$rc"
  fi

  # With --from-dir, validate_args already took the build's version (no network).
  if [[ -z $TARGET_VERSION ]]; then
    TARGET_VERSION="$(upd_latest_tag)" || die "cannot reach the GitHub API to find the latest release; pass --version"
  fi
  target="$(upd_strip_v "$TARGET_VERSION")"
  TARGET_VERSION="$target"
  installed="$(upd_installed_version)"
  if [[ $CHECK_ONLY == 1 ]]; then
    if [[ $installed == "$target" ]]; then
      log_ok "up to date ($installed)"
    else
      log_ok "installed $installed, available $target (install.sh update)"
    fi
    return 0
  fi
  [[ $installed != none ]] || die "mc-panel is not installed ($BIN_DIR/panel is missing); use install.sh install"
  if [[ $installed == "$target" && $FORCE != 1 ]]; then
    log_skip "already at $installed (--force reinstalls it)"
    return 0
  fi
  if [[ -z $FROM_DIR && ${MCPANEL_UPDATE_HANDOFF:-0} != 1 && $target != "${MCPANEL_VERSION#v}" ]]; then
    upd_handover
    return 0 # only reached when upd_exec is a test double
  fi
  unset MCPANEL_UPDATE_HANDOFF

  upd_context
  upd_backup
  UPD_SIGNALED=0
  upd_trap_signals
  upd_apply "$target"
  if [[ $UPD_APPLY_OK == 1 ]]; then
    trap - INT TERM HUP
    upd_state_set_phase "$UPD_DIR" verified
    log_ok "updated $UPD_FROM -> $target"
    upd_prune
    return 0
  fi
  # The signals stay caught: a second Ctrl-C must not stop the rollback halfway.
  if upd_rollback "$UPD_DIR"; then
    trap - INT TERM HUP
    warn "update to $target failed and was rolled back to $UPD_FROM; details: $LOG_FILE"
    return 3
  fi
  trap - INT TERM HUP
  upd_failed_rollback_help "$target"
  return 4
}
