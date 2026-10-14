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
UNIT_WAIT=60  # seconds per unit
UNIT_SETTLE=3 # seconds a unit must stay active without an automatic restart
PANEL_SOCKET=/run/mc-panel-web/panel.sock

# unit_settled UNIT: 0 if UNIT, UNIT_SETTLE seconds after it became active, is still active and
# systemd did not restart it in between (a service that crashes right after the start looks
# active for a moment). A query; tests stub it.
unit_settled() {
  local props state restarts
  sleep "$UNIT_SETTLE"
  props="$(systemctl show -p ActiveState -p NRestarts "$1" 2>/dev/null)" || return 1
  state="$(sed -n 's/^ActiveState=//p' <<<"$props")"
  restarts="$(sed -n 's/^NRestarts=//p' <<<"$props")"
  [[ $state == active && ${restarts:-0} == 0 ]]
}

# unit_panel_health: 0 once the panel answers /api/health on its socket (up to UNIT_WAIT
# seconds). A query; tests stub it.
unit_panel_health() {
  local i
  for ((i = 0; i < UNIT_WAIT; i++)); do
    if curl -fsS -o /dev/null --max-time 5 --unix-socket "$PANEL_SOCKET" http://localhost/api/health 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

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
    sys_systemctl daemon-reload </dev/null || return 1
  fi
  if [[ $CMD == update ]]; then
    if ((${#changed[@]} > 0)); then
      log_changed "${changed[*]} (update restarts the services)"
    else
      log_skip "units unchanged"
    fi
    return 0
  fi
  sys_systemctl enable "${UNIT_START_ORDER[@]}" </dev/null || return 1
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
      sys_systemctl restart "$u" </dev/null || true # the checks below report the failure
    else
      sys_systemctl start "$u" </dev/null || true
    fi
    if ! systemd_wait_active "$u" "$UNIT_WAIT"; then
      warn "$u did not become active within ${UNIT_WAIT}s (journalctl -u ${u%.*})"
      return 1
    fi
    if ! unit_settled "$u"; then
      warn "$u does not stay active (it was restarted or stopped; journalctl -u ${u%.*})"
      return 1
    fi
  done
  if ! unit_panel_health; then
    warn "the panel does not answer on $PANEL_SOCKET (journalctl -u mcpanel-panel)"
    return 1
  fi
  if ((${#changed[@]} > 0)); then
    log_changed "${changed[*]}; all active"
  else
    log_ok "units unchanged, all active"
  fi
}
