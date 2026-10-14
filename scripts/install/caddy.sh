# shellcheck shell=bash
# caddy.sh - installer spec §4.7: the panel's site lives in /etc/caddy/mc-panel.caddy; the
# Caddyfile only gets one marked import line. Foreign sites and global options stay untouched;
# only an unmodified package default (Ubuntu's :80 welcome site) is replaced by a file of our
# own. caddy validate before the reload; on failure the previous files are put back.
# Needs the KEY_* of cfg_keys.

# The exact line of the contract (overview V3); uninstall removes exactly this line.
CAD_IMPORT='import mc-panel.caddy # managed by mc-panel'
CAD_IMPORT_RE='^[[:space:]]*import[[:space:]]+mc-panel[.]caddy([[:space:]]|$)' # also unmarked or indented
CAD_WAIT=120 # seconds to wait for https://<domain>/api/health

# cad_is_package_default FILE: 0 if FILE is Ubuntu's unmodified Caddyfile (md5 of the conffile
# recorded by dpkg).
cad_is_package_default() {
  local want got
  want="$(dpkg-query -W -f='${Conffiles}\n' caddy 2>/dev/null | awk -v f="$CADDY_DIR/Caddyfile" '$1 == f { print $2; exit }')"
  [[ -n $want ]] || return 1
  got="$(md5sum -- "$1" | cut -d' ' -f1)"
  [[ $got == "$want" ]]
}

# cad_own_caddyfile: the Caddyfile when the installer owns it. skip_install_trust is a global
# option, so it can only go here (overview V3): with tls internal Caddy would otherwise try to
# add its root CA to the system trust store.
cad_own_caddyfile() {
  printf '# Created by mc-panel install.sh; add your own sites below, the import line must stay.\n'
  if [[ $TLS_MODE == internal ]]; then
    printf '{\n\tskip_install_trust\n}\n\n'
  fi
  printf '%s\n' "$CAD_IMPORT"
}

# cad_import_ok FILE: 0 if FILE has exactly one import line of ours and it is the marked one.
cad_import_ok() {
  [[ $(grep -cE -- "$CAD_IMPORT_RE" "$1") == 1 ]] && grep -qxF -- "$CAD_IMPORT" "$1"
}

# cad_new_caddyfile FILE prints the Caddyfile with the import line: the first import line of
# ours (unmarked, indented or marked) becomes the marked line, further ones are dropped
# (duplicates); without one the line is appended. FILE is left alone.
cad_new_caddyfile() {
  if grep -qE "$CAD_IMPORT_RE" -- "$1"; then
    awk -v re="$CAD_IMPORT_RE" -v line="$CAD_IMPORT" \
      '$0 ~ re { if (!done) print line; done = 1; next } { print }' "$1"
  else
    cat -- "$1"
    if [[ -n "$(tail -c 1 -- "$1")" ]]; then # no newline at the end
      printf '\n'
    fi
    printf '\n%s\n' "$CAD_IMPORT"
  fi
}

# cad_wait polls the panel through Caddy for up to CAD_WAIT seconds; only a warning on failure
# (DNS or a blocked port 80 are the usual causes for Let's Encrypt).
cad_wait() {
  local i opts=(-fsS -o /dev/null --max-time 5)
  if [[ $TLS_MODE == internal ]]; then
    opts+=(-k --resolve "$DOMAIN:443:127.0.0.1")
  fi
  for ((i = 0; i < CAD_WAIT; i += 5)); do
    if curl "${opts[@]}" "https://$DOMAIN/api/health" 2>/dev/null; then
      log_ok "https://$DOMAIN/ answers"
      return 0
    fi
    sleep 5
  done
  warn "https://$DOMAIN/api/health does not answer after ${CAD_WAIT}s (DNS, port 80/443 or Let's Encrypt?); last Caddy log lines:"
  journalctl -u caddy.service -n 20 --no-pager 2>/dev/null | sed 's/^/  /' || true
}

# cad_restore: puts back the Caddyfile and mc-panel.caddy saved by cad_ensure.
cad_restore() {
  local dir="$ROOT$CADDY_DIR" name
  for name in Caddyfile mc-panel.caddy; do
    if [[ -e $WORK/caddy.before/$name ]]; then
      cp -p -- "$WORK/caddy.before/$name" "$dir/$name"
    else
      rm -f -- "$dir/$name"
    fi
  done
}

cad_ensure() {
  log_step "caddy"
  cfg_keys || return 1
  local dir="$ROOT$CADDY_DIR" file="$ROOT$CADDY_DIR/Caddyfile" name site_r file_r=unchanged
  if [[ ! -d $dir ]]; then
    site_r="$(ensure_dir "$CADDY_DIR" 0755 root root)" || return 1
  fi
  for name in Caddyfile mc-panel.caddy; do
    if [[ -L $dir/$name || (-e $dir/$name && ! -f $dir/$name) ]]; then
      warn "$CADDY_DIR/$name is not a regular file; refusing to change it"
      return 1
    fi
  done
  rm -rf -- "$WORK/caddy.before"
  mkdir -p -- "$WORK/caddy.before"
  for name in Caddyfile mc-panel.caddy; do
    if [[ -e $dir/$name ]]; then
      cp -p -- "$dir/$name" "$WORK/caddy.before/$name"
    fi
  done
  site_r="$(write_template caddy/mc-panel.caddy "$CADDY_DIR/mc-panel.caddy" 0644 root root)" || return 1
  # An existing Caddyfile keeps its owner, group and mode (e.g. root:caddy 0640).
  local mode=0644 og=root:root
  if [[ -e $file ]]; then
    mode="0$(stat -c '%a' -- "$file")"
    og="$(_owner_get "$CADDY_DIR/Caddyfile")"
  fi
  if [[ ! -e $file ]] || cad_is_package_default "$file"; then
    file_r="$(cad_own_caddyfile | write_file "$CADDY_DIR/Caddyfile" "$mode" "${og%%:*}" "${og#*:}")" ||
      { cad_restore; return 1; }
  elif ! cad_import_ok "$file"; then
    cad_new_caddyfile "$file" >"$WORK/Caddyfile.new"
    file_r="$(write_file "$CADDY_DIR/Caddyfile" "$mode" "${og%%:*}" "${og#*:}" <"$WORK/Caddyfile.new")" ||
      { cad_restore; return 1; }
  fi
  sys_systemctl enable --now caddy.service </dev/null || true
  if [[ $site_r == unchanged && $file_r == unchanged ]]; then
    log_skip "site $DOMAIN unchanged"
    return 0
  fi
  # Validated as the user the service runs as: a file caddy cannot read fails here, not at
  # the reload. HOME is caddy's own (validate provisions the TLS storage below it).
  if ! run_as caddy env HOME=/var/lib/caddy caddy validate --config "$file" --adapter caddyfile </dev/null; then
    cad_restore
    warn "caddy rejected the configuration; the previous Caddyfile and mc-panel.caddy are back in place"
    return 1
  fi
  if ! sys_systemctl reload-or-restart caddy.service </dev/null; then
    cad_restore
    sys_systemctl reload-or-restart caddy.service </dev/null || true
    warn "caddy did not reload (journalctl -u caddy); the previous Caddyfile and mc-panel.caddy are back in place"
    return 1
  fi
  log_changed "site $DOMAIN ($TLS_MODE): mc-panel.caddy $site_r, Caddyfile $file_r"
  cad_wait
}
