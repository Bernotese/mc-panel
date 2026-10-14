# shellcheck shell=bash
# upgrades.sh - installer spec §4.9: security updates daily, never an automatic reboot (a reboot
# would stop the Minecraft servers). The allowed origins stay those of Ubuntu's
# 50unattended-upgrades (the release and its -security pocket, ESM security; -updates is off):
# apt appends list entries, so a file of ours could only widen them, never narrow them.

UPG_PERIODIC=/etc/apt/apt.conf.d/21mc-panel-auto-upgrades # after the package's 20auto-upgrades
UPG_OPTIONS=/etc/apt/apt.conf.d/52mc-panel-unattended-upgrades

upg_periodic() {
  cat <<'EOF'
// Managed by mc-panel install.sh: update the package lists and run unattended-upgrades daily.
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
EOF
}

upg_options() {
  cat <<'EOF'
// Managed by mc-panel install.sh: never reboot on its own; Minecraft servers would stop.
Unattended-Upgrade::Automatic-Reboot "false";
EOF
}

upg_ensure() {
  log_step "security updates"
  local a b
  if [[ ! -d $ROOT${UPG_PERIODIC%/*} ]]; then
    ensure_dir "${UPG_PERIODIC%/*}" 0755 root root >/dev/null
  fi
  a="$(upg_periodic | write_file "$UPG_PERIODIC" 0644 root root)" || return 1
  b="$(upg_options | write_file "$UPG_OPTIONS" 0644 root root)" || return 1
  sys_systemctl enable --now unattended-upgrades.service </dev/null || {
    warn "unattended-upgrades.service does not start"
    return 1
  }
  if [[ $a == changed || $b == changed ]]; then
    log_changed "unattended-upgrades: security updates daily, no automatic reboot"
  else
    log_ok "unattended-upgrades unchanged"
  fi
}
