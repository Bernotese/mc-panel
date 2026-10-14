# shellcheck shell=bash
# admin.sh - installer spec §4.10: the first admin, unless `panel admin list` already names one.
# With `curl | bash` stdin is the script itself, so the password comes from
# --admin-password-file (main.sh checked owner and mode), from stdin only with --admin-user when
# install.sh runs from a file (read early by adm_preread from pre_check), or from the terminal
# ($TTY). Without any of them, or when admin create keeps failing, the step warns and install
# continues; install_summary (main.sh) prints the command to create one later.

# The command to create an admin later: panel asks for the password twice on the terminal, so
# no password ever appears in a command line or the shell history.
ADM_CREATE_HINT="sudo runuser -u mcpanel -- $BIN_DIR/panel admin create -config $ETC_PANEL/panel.toml -username <name>"
ADM_STDIN_READ=0 # adm_preread ran
ADM_STDIN_PW=""  # the password from stdin (only with --admin-user and install.sh from a file)

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

# adm_preread reads the stdin password once, early (pre_check), before any other command could
# consume stdin: only with --admin-user and no --admin-password-file, when install.sh runs from
# a file (with curl | bash stdin is the script) and stdin is no terminal.
adm_preread() {
  if ((ADM_STDIN_READ)); then
    return 0
  fi
  ADM_STDIN_READ=1
  if [[ -n $ADMIN_USER && -z $ADMIN_PASSWORD_FILE && ! -t 0 ]] && adm_script_is_file; then
    IFS= read -r ADM_STDIN_PW || true
  fi
  return 0
}

# adm_password_source prints where the password comes from: file, stdin, tty or none.
adm_password_source() {
  if [[ -n $ADMIN_PASSWORD_FILE ]]; then
    echo file
  elif [[ -n $ADM_STDIN_PW ]]; then
    echo stdin
  elif adm_has_tty; then
    echo tty
  else
    echo none
  fi
}

# adm_password USER SOURCE prints the password of the new admin; 1 if there is none.
adm_password() {
  local a="" b="" i
  case "$2" in
  file)
    IFS= read -r a <"$ADMIN_PASSWORD_FILE" || true
    if [[ -z $a ]]; then
      warn "--admin-password-file is empty"
      return 1
    fi
    printf '%s\n' "$a"
    return 0
    ;;
  stdin)
    printf '%s\n' "$ADM_STDIN_PW"
    return 0
    ;;
  tty) ;;
  *) return 1 ;;
  esac
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
  local existing user pw src attempt
  if ! existing="$(run_as mcpanel "$BIN_DIR/panel" admin list -config "$ETC_PANEL/panel.toml" </dev/null)"; then
    warn "panel admin list failed; create the admin later with: $ADM_CREATE_HINT"
    return 0
  fi
  if [[ -n $existing ]]; then
    log_skip "admin exists: ${existing//$'\n'/, }"
    return 0
  fi
  adm_preread
  src="$(adm_password_source)"
  if [[ -z $ADMIN_USER && $src != tty ]]; then
    warn "no admin yet and no terminal (pass --admin-user); create one later with: $ADM_CREATE_HINT"
    return 0
  fi
  # At the terminal a rejected name or password (policy) is asked again; a password from a file
  # or stdin is tried once.
  for ((attempt = 0; attempt < 3; attempt++)); do
    user="$ADMIN_USER"
    if [[ -z $user ]]; then
      user="$(adm_read_line "admin user name: ")"
    fi
    if [[ -z $user ]] || ! pw="$(adm_password "$user" "$src")"; then
      break
    fi
    if printf '%s\n' "$pw" | run_as mcpanel "$BIN_DIR/panel" admin create -config "$ETC_PANEL/panel.toml" -username "$user"; then
      log_changed "admin $user created"
      return 0
    fi
    warn "panel admin create failed (user name or password policy, see above)"
    if [[ $src != tty ]]; then
      break
    fi
  done
  warn "no admin was created; create one later with: $ADM_CREATE_HINT"
  return 0
}
