# shellcheck shell=bash
# units.sh - installer spec §4.6: systemd units from deploy/systemd/, started agent -> files ->
# panel, each must become active within 60 s. mcpanel-fw.service is only written here; fw_ensure
# enables and starts it (never with --no-firewall). With CMD=update this step only writes and
# reloads: update restarts the services and checks their health itself (overview V4).
# Needs the KEY_* of cfg_keys (cfg_ensure runs before).

UNIT_FILES=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service
  mcpanel-panel.socket mcpanel-panel.service mcpanel-fw.service)
UNIT_START_ORDER=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service
  mcpanel-panel.socket mcpanel-panel.service)
UNIT_WAIT=60 # seconds per unit

unit_ensure() {
  log_step "systemd units"
  local u r changed=() restart
  if [[ ! -d $ROOT$UNIT_DIR ]]; then
    r="$(ensure_dir "$UNIT_DIR" 0755 root root)" || return 1
  fi
  for u in "${UNIT_FILES[@]}"; do
    # A failure returns 1, so update's subshell rolls back instead of dying halfway (V4).
    r="$(write_template "systemd/$u" "$UNIT_DIR/$u" 0644 root root)" || return 1
    [[ $r == unchanged ]] || changed+=("$u")
  done
  if ((${#changed[@]} > 0)); then
    sys_systemctl daemon-reload || return 1
  fi
  if [[ $CMD == update ]]; then
    if ((${#changed[@]} > 0)); then
      log_changed "${changed[*]} (update restarts the services)"
    else
      log_skip "units unchanged"
    fi
    return 0
  fi
  sys_systemctl enable "${UNIT_START_ORDER[@]}" || return 1
  for u in "${UNIT_START_ORDER[@]}"; do
    # A changed unit restarts; a service also restarts when its binary or configuration
    # changed (a second install with a new version). start on an active unit is a no-op.
    restart=0
    if [[ " ${changed[*]} " == *" $u "* ]]; then
      restart=1
    elif [[ $u == *.service ]] && ((BIN_CHANGED || CFG_CHANGED)); then
      restart=1
    fi
    if ((restart)); then
      sys_systemctl restart "$u" || true # the wait below reports the failure
    else
      sys_systemctl start "$u" || true
    fi
    if ! systemd_wait_active "$u" "$UNIT_WAIT"; then
      warn "$u did not become active within ${UNIT_WAIT}s (journalctl -u ${u%.*})"
      return 1
    fi
  done
  if ((${#changed[@]} > 0)); then
    log_changed "${changed[*]}; all active"
  else
    log_ok "units unchanged, all active"
  fi
}
