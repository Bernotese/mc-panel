# shellcheck shell=bash
# layout.sh - installer spec §4.3: the system users and the directories the services need.
# instances/ and backups/ are created here because mcpanel-files.service names them in
# ReadWritePaths (systemd refuses to start it without them, 226/NAMESPACE); the agent creates
# the other subdirectories of /srv/mc-panel.

# Directories: path mode owner group, created in this order (parents first).
LAY_DIRS=(
  "$INSTANCES_DIR 0755 root root"
  "$INSTANCES_DIR/instances 0750 root mcpanel-mc"
  "$INSTANCES_DIR/backups 0700 mcpanel-mc mcpanel-mc"
  "$ETC_PANEL 0750 root mcpanel"
  "$ETC_FILES 0750 root mcpanel-mc"
  "$DATA_DIR 0700 mcpanel mcpanel"
)

# lay_uid NAME prints the uid of user NAME; 1 if there is no such user (a query; tests stub it,
# config.sh and doctor read the panel uid through it).
lay_uid() {
  id -u -- "$1" 2>/dev/null
}

# lay_user NAME HOME: creates the system user NAME (own group, no login) unless it exists;
# prints changed/unchanged.
lay_user() {
  if lay_uid "$1" >/dev/null; then
    echo unchanged
    return 0
  fi
  sys_useradd --system --user-group --home-dir "$2" --no-create-home --shell /usr/sbin/nologin "$1" || return 1
  echo changed
}

lay_ensure() {
  log_step "users and directories"
  local changed=() spec path mode owner group r
  r="$(lay_user mcpanel "$DATA_DIR")" || die "useradd mcpanel failed"
  [[ $r == unchanged ]] || changed+=("user mcpanel")
  r="$(lay_user mcpanel-mc /nonexistent)" || die "useradd mcpanel-mc failed"
  [[ $r == unchanged ]] || changed+=("user mcpanel-mc")
  for spec in "${LAY_DIRS[@]}"; do
    read -r path mode owner group <<<"$spec"
    # ensure_dir dies on a symlink or an unsafe parent; in $(...) only the subshell ends.
    r="$(ensure_dir "$path" "$mode" "$owner" "$group")" || return 1
    [[ $r == unchanged ]] || changed+=("$path")
  done
  if ((${#changed[@]} > 0)); then
    log_changed "${changed[*]}"
  else
    log_skip "users and directories unchanged"
  fi
}
