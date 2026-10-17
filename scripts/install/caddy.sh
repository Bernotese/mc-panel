# shellcheck shell=bash
# caddy.sh - installer spec §4.7: the panel's site lives in /etc/caddy/mc-panel.caddy; the
# Caddyfile only gets one marked import line. Foreign sites and global options stay untouched;
# only an unmodified package default (Ubuntu's :80 welcome site) is replaced by a file of our
# own. caddy validate before the reload; on failure the previous files are put back.
# A Caddyfile of the installer moves Caddy's admin API from 127.0.0.1:2019 (any local user, the
# panel included, could reconfigure Caddy) to a socket only caddy and root can open (SEC-14);
# the drop-in caddy.service.d/mc-panel.conf creates its directory /run/caddy and /var/log/caddy
# for the panel's access log. Needs the KEY_* of cfg_keys.
# The web interfaces of the servers (web ports spec §5) are a catch-all site in
# /etc/caddy/mc-panel-web.caddy with certificates on demand; Caddy asks mcpanel-web
# (on_demand_tls ask, a global option) before it requests one. The installer adds the ask and the
# import only to a Caddyfile it owns: without the ask, any name pointing at the host would get a
# certificate. A foreign Caddyfile gets a hint with both snippets.

# The exact line of the contract (overview V3); uninstall removes exactly this line.
CAD_IMPORT='import mc-panel.caddy # managed by mc-panel'
CAD_IMPORT_RE='^[[:space:]]*import[[:space:]]+mc-panel[.]caddy([[:space:]]|$)' # also unmarked or indented
CAD_WEB_IMPORT='import mc-panel-web.caddy # managed by mc-panel'
CAD_WEB_IMPORT_RE='^[[:space:]]*import[[:space:]]+mc-panel-web[.]caddy([[:space:]]|$)'
# The first line of a Caddyfile the installer created; the global options below it are the
# installer's, the sites the operator adds after the import lines are not.
CAD_HEAD='# Created by mc-panel install.sh; add your own sites below, the import line must stay.'
CAD_WAIT=120 # seconds to wait for https://<domain>/api/health
CAD_ADMIN='admin unix//run/caddy/admin.sock'
CAD_DROPIN=/etc/systemd/system/caddy.service.d/mc-panel.conf
# The operator's additions to the panel site (basic_auth, an IP allowlist, …): mc-panel.caddy
# imports $CAD_SITE_D/*.caddy inside its site block. install and update only create the directory
# when it is missing and never touch it or its files afterwards; uninstall keeps it.
CAD_SITE_D=/etc/caddy/mc-panel.d
CAD_WEB_SITE_D=/etc/caddy/mc-panel-web.d # the same for the web interfaces site (mc-panel-web.caddy)

# cad_is_package_default FILE: 0 if FILE is Ubuntu's unmodified Caddyfile (md5 of the conffile
# recorded by dpkg).
cad_is_package_default() {
  local want got
  want="$(dpkg-query -W -f='${Conffiles}\n' caddy 2>/dev/null | awk -v f="$CADDY_DIR/Caddyfile" '$1 == f { print $2; exit }')"
  [[ -n $want ]] || return 1
  got="$(md5sum -- "$1" | cut -d' ' -f1)"
  [[ $got == "$want" ]]
}

# cad_on_demand_block: the global option that lets Caddy request a certificate on demand only
# for a name mcpanel-web allows (its ask server answers 200 for published names only).
cad_on_demand_block() {
  printf '\ton_demand_tls {\n\t\task http://127.0.0.1:%s/\n\t}\n' "$KEY_WEB_ASK_PORT"
}

# cad_own_caddyfile: the Caddyfile when the installer owns it. Global options can only go here
# (overview V3): the admin socket, with tls internal skip_install_trust (Caddy would otherwise
# try to add its root CA to the system trust store), and the on-demand ask of the web interfaces.
cad_own_caddyfile() {
  printf '%s\n' "$CAD_HEAD"
  printf '{\n\t%s\n' "$CAD_ADMIN"
  if [[ $TLS_MODE == internal ]]; then
    printf '\tskip_install_trust\n'
  fi
  cad_on_demand_block
  printf '}\n\n%s\n%s\n' "$CAD_IMPORT" "$CAD_WEB_IMPORT"
}

# cad_is_old_own FILE: 0 if FILE is exactly a Caddyfile that an older install.sh created (before
# the admin socket or before the web interfaces, either TLS mode); such a file is still the
# installer's and gets the new one.
cad_is_old_own() {
  local f admin=$'\n{\n\t'"$CAD_ADMIN"
  f="$(cat -- "$1")" || return 1
  [[ $f == "$CAD_HEAD"$'\n'"$CAD_IMPORT" || $f == "$CAD_HEAD"$'\n{\n\tskip_install_trust\n}\n\n'"$CAD_IMPORT" ||
    $f == "$CAD_HEAD$admin"$'\n}\n\n'"$CAD_IMPORT" || $f == "$CAD_HEAD$admin"$'\n\tskip_install_trust\n}\n\n'"$CAD_IMPORT" ]]
}

# cad_is_own FILE: 0 if the installer created FILE (its first line), also when the operator added
# sites to it.
cad_is_own() {
  [[ $(head -n 1 -- "$1" 2>/dev/null) == "$CAD_HEAD" ]]
}

# cad_web_caddyfile FILE prints an own Caddyfile (cad_is_own) with the web interfaces: the
# on_demand_tls block in the first global options block (one is added after the leading comments
# when there is none; an existing on_demand_tls block stays, only an ask on 127.0.0.1 follows
# KEY_WEB_ASK_PORT) and the marked import of mc-panel-web.caddy right after the panel's import
# (an unmarked one becomes that line, duplicates are dropped). Everything else stays as it is.
cad_web_caddyfile() {
  awk -v ask="ask http://127.0.0.1:$KEY_WEB_ASK_PORT/" -v imp="$CAD_WEB_IMPORT" -v re="$CAD_WEB_IMPORT_RE" \
    -v panel_re="$CAD_IMPORT_RE" '
    function block() { print "\ton_demand_tls {"; print "\t\t" ask; print "\t}" }
    state == 0 && /^[[:space:]]*(#.*)?$/ { print; next }
    state == 0 && $0 == "{" { state = 1; depth = 1; print; next }
    state == 0 { print "{"; block(); print "}"; print ""; state = 2 }
    state == 1 {
      code = $0; sub(/#.*/, "", code)
      if (code ~ /^[[:space:]]*on_demand_tls([[:space:]]|$)/) { odt = depth + 1; seen = 1 }
      if (odt && code ~ /^[[:space:]]*ask[[:space:]]+http:\/\/127[.]0[.]0[.]1:[0-9]+\/[[:space:]]*$/) {
        match($0, /^[[:space:]]*/); $0 = substr($0, 1, RLENGTH) ask
      }
      depth += gsub(/[{]/, "{", code) - gsub(/[}]/, "}", code)
      if (odt && depth < odt) odt = 0
      if (depth == 0) { if (!seen) block(); state = 2 }
      print; next
    }
    $0 ~ re { if (!done) print imp; done = 1; next }
    { print }
    $0 ~ panel_re && !done { print imp; done = 1 }
    END { if (!done) { print ""; print imp } }' "$1"
}

# cad_web_hint: what the operator of a foreign Caddyfile adds for the web interfaces.
cad_web_hint() {
  warn "$CADDY_DIR/Caddyfile is not the installer's, so the web interfaces of the servers are not served. Add to its global options
$(cad_on_demand_block)
and the line
$CAD_WEB_IMPORT
then: systemctl reload caddy (docs/install.md, \"Web interfaces\")"
}

# cad_dropin_ensure writes the drop-in for caddy.service (daemon-reload when it changed) and
# prints changed/unchanged. Uninstall keeps it: the Caddyfile may still name the admin socket.
cad_dropin_ensure() {
  local r
  if [[ ! -d $ROOT${CAD_DROPIN%/*} ]]; then
    ensure_dir "${CAD_DROPIN%/*}" 0755 root root >/dev/null || return 1
  fi
  # caddy validate (as caddy) already opens the access log, before LogsDirectory= of a restart
  # could create its directory. Not ensure_dir: /var/log is group-writable (root:syslog 0775).
  if [[ ! -d $ROOT/var/log/caddy ]]; then
    sys_install -d -o caddy -g caddy -m 0750 -- "$ROOT/var/log/caddy" || return 1
  fi
  r="$(write_template caddy/caddy-service-mc-panel.conf "$CAD_DROPIN" 0644 root root)" || return 1
  if [[ $r == changed ]]; then
    sys_systemctl daemon-reload </dev/null || return 1
  fi
  echo "$r"
}

# cad_import_files FILE prints the files FILE imports (one level; a relative path or glob is
# relative to FILE's directory, an absolute one is prefixed with ROOT), one per line. Named
# snippets (import name) match no file and are skipped.
cad_import_files() {
  local dir="${1%/*}" p f
  while read -r p; do
    if [[ $p == /* ]]; then p="$ROOT$p"; else p="$dir/$p"; fi
    # shellcheck disable=SC2086 # the import path is a glob on purpose
    for f in $p; do
      [[ -f $f ]] && printf '%s\n' "$f"
    done
  done < <(awk '$1 == "import" && NF >= 2 { print $2 }' "$1" 2>/dev/null)
}

# cad_admin_status FILE: "socket" if FILE or a file it imports moves the admin API off TCP
# (admin unix//… or admin off), else "tcp". doctor reports the TCP case of a foreign Caddyfile.
cad_admin_status() {
  local f
  # shellcheck disable=SC2046 # one path per line, no blanks in Caddy's import paths
  for f in "$1" $(cad_import_files "$1"); do
    if grep -qE '^[[:space:]]*admin[[:space:]]+(unix//|off([[:space:]]|$))' -- "$f" 2>/dev/null; then
      echo socket
      return 0
    fi
  done
  echo tcp
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

# cad_restore: puts back the Caddyfile, mc-panel.caddy and mc-panel-web.caddy saved by cad_ensure.
cad_restore() {
  local dir="$ROOT$CADDY_DIR" name
  for name in Caddyfile mc-panel.caddy mc-panel-web.caddy; do
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
  local dir="$ROOT$CADDY_DIR" file="$ROOT$CADDY_DIR/Caddyfile" name site_r web_r file_r=unchanged drop_r
  if [[ ! -d $dir ]]; then
    site_r="$(ensure_dir "$CADDY_DIR" 0755 root root)" || return 1
  fi
  for name in Caddyfile mc-panel.caddy mc-panel-web.caddy; do
    if [[ -L $dir/$name || (-e $dir/$name && ! -f $dir/$name) ]]; then
      warn "$CADDY_DIR/$name is not a regular file; refusing to change it"
      return 1
    fi
  done
  rm -rf -- "$WORK/caddy.before"
  mkdir -p -- "$WORK/caddy.before"
  for name in Caddyfile mc-panel.caddy mc-panel-web.caddy; do
    if [[ -e $dir/$name ]]; then
      cp -p -- "$dir/$name" "$WORK/caddy.before/$name"
    fi
  done
  drop_r="$(cad_dropin_ensure)" || return 1
  for name in "$CAD_SITE_D" "$CAD_WEB_SITE_D"; do
    if [[ ! -e $ROOT$name && ! -L $ROOT$name ]]; then
      ensure_dir "$name" 0755 root root >/dev/null || return 1
    fi
  done
  site_r="$(write_template caddy/mc-panel.caddy "$CADDY_DIR/mc-panel.caddy" 0644 root root)" || return 1
  # Also next to a foreign Caddyfile: inert until imported, then the hint's two snippets suffice.
  web_r="$(write_template caddy/mc-panel-web.caddy "$CADDY_DIR/mc-panel-web.caddy" 0644 root root)" ||
    { cad_restore; return 1; }
  # An existing Caddyfile keeps its owner, group and mode (e.g. root:caddy 0640).
  local mode=0644 og=root:root
  if [[ -e $file ]]; then
    mode="0$(stat -c '%a' -- "$file")"
    og="$(_owner_get "$CADDY_DIR/Caddyfile")"
  fi
  if [[ ! -e $file ]] || cad_is_package_default "$file" || cad_is_old_own "$file"; then
    file_r="$(cad_own_caddyfile | write_file "$CADDY_DIR/Caddyfile" "$mode" "${og%%:*}" "${og#*:}")" ||
      { cad_restore; return 1; }
  elif cad_is_own "$file"; then
    if cad_import_ok "$file"; then cp -- "$file" "$WORK/Caddyfile.own"; else cad_new_caddyfile "$file" >"$WORK/Caddyfile.own"; fi
    cad_web_caddyfile "$WORK/Caddyfile.own" >"$WORK/Caddyfile.new"
    file_r="$(write_file "$CADDY_DIR/Caddyfile" "$mode" "${og%%:*}" "${og#*:}" <"$WORK/Caddyfile.new")" ||
      { cad_restore; return 1; }
  elif ! cad_import_ok "$file"; then
    cad_new_caddyfile "$file" >"$WORK/Caddyfile.new"
    file_r="$(write_file "$CADDY_DIR/Caddyfile" "$mode" "${og%%:*}" "${og#*:}" <"$WORK/Caddyfile.new")" ||
      { cad_restore; return 1; }
  fi
  if ! cad_is_own "$file" && ! grep -qE -- "$CAD_WEB_IMPORT_RE" "$file"; then
    cad_web_hint
  fi
  sys_systemctl enable --now caddy.service </dev/null || true
  if [[ $site_r == unchanged && $web_r == unchanged && $file_r == unchanged && $drop_r == unchanged ]]; then
    log_skip "site $DOMAIN unchanged"
    return 0
  fi
  # Validated as the user the service runs as: a file caddy cannot read fails here, not at
  # the reload. HOME is caddy's own (validate provisions the TLS storage below it).
  if ! run_as caddy env HOME=/var/lib/caddy caddy validate --config "$file" --adapter caddyfile </dev/null; then
    cad_restore
    warn "caddy rejected the configuration; the previous Caddyfile, mc-panel.caddy and mc-panel-web.caddy are back in place"
    return 1
  fi
  # caddy reload sends the new configuration to the admin address named in that configuration:
  # when the Caddyfile (its admin line) or the drop-in (/run/caddy) changed, only a restart works.
  local how=reload-or-restart
  if [[ $file_r == changed || $drop_r == changed ]]; then
    how=restart
  fi
  if ! sys_systemctl "$how" caddy.service </dev/null; then
    cad_restore
    sys_systemctl restart caddy.service </dev/null || true
    warn "caddy did not $how (journalctl -u caddy); the previous Caddyfile, mc-panel.caddy and mc-panel-web.caddy are back in place"
    return 1
  fi
  log_changed "site $DOMAIN ($TLS_MODE): mc-panel.caddy $site_r, mc-panel-web.caddy $web_r, Caddyfile $file_r, caddy.service drop-in $drop_r"
  cad_wait
}
