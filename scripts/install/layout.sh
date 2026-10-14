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

LAY_RESULT=""

# lay_uid NAME prints the uid of user NAME; 1 if there is no such user (a query; tests stub it,
# config.sh and doctor read the panel uid through it).
lay_uid() {
  id -u -- "$1" 2>/dev/null
}

# lay_passwd NAME prints the passwd entry of NAME ("" if none); lay_group_exists NAME: 0 if
# the group exists. Queries; tests stub them.
lay_passwd() {
  getent passwd "$1" 2>/dev/null || true
}
lay_group_exists() {
  getent group "$1" >/dev/null 2>&1
}

# lay_user NAME HOME: creates the system user NAME (no login, no home) unless it exists; a
# group NAME that exists already becomes its primary group. An existing user that looks like a
# login account (UID >= 1000 or a login shell) is kept, with a warning. Sets LAY_RESULT to
# changed/unchanged; dies when useradd fails (nothing else has changed yet).
lay_user() {
  local entry uid shell
  entry="$(lay_passwd "$1")"
  if [[ -n $entry ]]; then
    IFS=: read -r _ _ uid _ _ _ shell <<<"$entry"
    if [[ ! $uid =~ ^[0-9]+$ ]] || ((uid >= 1000)); then
      warn "user $1 exists with UID $uid, not a system user; mc-panel uses it anyway"
    fi
    case "$shell" in
    */nologin | */false) ;;
    *) warn "user $1 has the login shell ${shell:-<none>}; consider: usermod -s /usr/sbin/nologin $1" ;;
    esac
    LAY_RESULT=unchanged
    return 0
  fi
  local -a group=(--user-group)
  if lay_group_exists "$1"; then
    group=(-g "$1")
  fi
  sys_useradd --system "${group[@]}" --home-dir "$2" --no-create-home --shell /usr/sbin/nologin "$1" </dev/null ||
    die "useradd $1 failed"
  LAY_RESULT=changed
}

lay_ensure() {
  log_step "users and directories"
  local changed=() spec path mode owner group r
  lay_user mcpanel "$DATA_DIR"
  [[ $LAY_RESULT == unchanged ]] || changed+=("user mcpanel")
  lay_user mcpanel-mc /nonexistent
  [[ $LAY_RESULT == unchanged ]] || changed+=("user mcpanel-mc")
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
