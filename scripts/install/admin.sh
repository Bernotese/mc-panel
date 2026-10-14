# shellcheck shell=bash
# admin.sh - installer spec §4.10: the first admin, unless `panel admin list` already names one.
# With `curl | bash` stdin is the script itself, so the password comes from
# --admin-password-file (main.sh checked owner and mode), from stdin only with --admin-user when
# install.sh runs from a file, or from the terminal ($TTY). Without any of them the step warns
# and install continues; install_summary (main.sh) prints the command to create one later.

ADM_CREATE_HINT="printf '%s\\n' '<password>' | sudo runuser -u mcpanel -- $BIN_DIR/panel admin create -config $ETC_PANEL/panel.toml -username <name>"

# adm_script_is_file: 0 if install.sh was started from a file (not piped into bash).
adm_script_is_file() {
  [[ -f ${BASH_SOURCE[-1]:-} ]]
}

# adm_has_tty: 0 if the terminal $TTY can be opened for reading and writing.
adm_has_tty() {
  [[ -r $TTY && -w $TTY ]] && { : <"$TTY" && : >>"$TTY"; } 2>/dev/null
}

# adm_read_line PROMPT prints one line read from the terminal.
adm_read_line() {
  local v=""
  printf '%s' "$1" >>"$TTY"
  IFS= read -r v <"$TTY" || true
  printf '%s\n' "$v"
}

# adm_read_secret PROMPT prints one line read from the terminal without echo.
adm_read_secret() {
  local v=""
  printf '%s' "$1" >>"$TTY"
  IFS= read -rs v <"$TTY" || true
  printf '\n' >>"$TTY"
  printf '%s\n' "$v"
}

# adm_password USER prints the password of the new admin; 1 if there is none.
adm_password() {
  local a="" b="" i
  if [[ -n $ADMIN_PASSWORD_FILE ]]; then
    IFS= read -r a <"$ADMIN_PASSWORD_FILE" || true
    if [[ -z $a ]]; then
      warn "--admin-password-file is empty"
      return 1
    fi
    printf '%s\n' "$a"
    return 0
  fi
  if [[ -n $ADMIN_USER && ! -t 0 ]] && adm_script_is_file; then
    IFS= read -r a || true
    if [[ -n $a ]]; then
      printf '%s\n' "$a"
      return 0
    fi
  fi
  adm_has_tty || return 1
  for ((i = 0; i < 3; i++)); do
    a="$(adm_read_secret "password for $1: ")"
    b="$(adm_read_secret "repeat the password: ")"
    if [[ -n $a && $a == "$b" ]]; then
      printf '%s\n' "$a"
      return 0
    fi
    printf 'the passwords are empty or do not match, try again\n' >>"$TTY"
  done
  warn "no matching password after 3 attempts"
  return 1
}

adm_ensure() {
  log_step "first admin"
  local existing user pw
  if ! existing="$(run_as mcpanel "$BIN_DIR/panel" admin list -config "$ETC_PANEL/panel.toml")"; then
    warn "panel admin list failed; create the admin later with: $ADM_CREATE_HINT"
    return 0
  fi
  if [[ -n $existing ]]; then
    log_skip "admin exists: ${existing//$'\n'/, }"
    return 0
  fi
  user="$ADMIN_USER"
  if [[ -z $user ]]; then
    if ! adm_has_tty; then
      warn "no admin yet and no terminal (pass --admin-user); create one later with: $ADM_CREATE_HINT"
      return 0
    fi
    user="$(adm_read_line "admin user name: ")"
  fi
  if [[ -z $user ]] || ! pw="$(adm_password "$user")"; then
    warn "no admin was created; create one later with: $ADM_CREATE_HINT"
    return 0
  fi
  if ! printf '%s\n' "$pw" | run_as mcpanel "$BIN_DIR/panel" admin create -config "$ETC_PANEL/panel.toml" -username "$user"; then
    warn "panel admin create failed (user name or password policy, see above); retry with: $ADM_CREATE_HINT"
    return 1
  fi
  log_changed "admin $user created"
}
