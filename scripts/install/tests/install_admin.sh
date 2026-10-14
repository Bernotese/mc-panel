# shellcheck shell=bash
# Tests of admin.sh (installer spec §4.10, overview V4). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329

# adm_fixture: run_as records the call; "admin list" prints $ADM_EXISTING, "admin create"
# stores the password it got on stdin in $WORK/created.pw.
adm_fixture() {
  ADM_EXISTING=""
  ADMIN_USER="" ADMIN_PASSWORD_FILE="" ADM_STDIN_READ=0 ADM_STDIN_PW=""
  run_as() {
    printf 'run_as %s\n' "$*" >>"$WORK/calls.log"
    case "$*" in
    *"admin list"*) printf '%s' "$ADM_EXISTING" ;;
    *"admin create"*) cat >"$WORK/created.pw" ;;
    esac
  }
}

test_install_adm_ensure_skips_when_admin_exists() {
  adm_fixture
  ADM_EXISTING=$'jannes\nzoe\n'
  assert_contains "$(adm_ensure)" "[skip] admin exists: jannes, zoe"
  assert_called 'run_as mcpanel /usr/local/bin/panel admin list -config /etc/mc-panel/panel.toml'
  assert_not_called 'admin create'
}

test_install_adm_ensure_takes_password_file() {
  adm_fixture
  ADMIN_USER="admin"
  printf 'file-secret-1234\n' >"$WORK/pw"
  chmod 0600 "$WORK/pw"
  ADMIN_PASSWORD_FILE="$WORK/pw"
  assert_contains "$(adm_ensure </dev/null)" "[changed] admin admin created"
  assert_called 'run_as mcpanel /usr/local/bin/panel admin create -config /etc/mc-panel/panel.toml -username admin'
  assert_file_eq "$WORK/created.pw" 'file-secret-1234'
}

test_install_adm_ensure_empty_password_file_warns() {
  adm_fixture
  ADMIN_USER="admin"
  : >"$WORK/pw"
  ADMIN_PASSWORD_FILE="$WORK/pw"
  assert_contains "$(adm_ensure </dev/null)" "[warn]"
  assert_not_called 'admin create'
}

test_install_adm_ensure_creates_with_user_and_stdin_password() {
  adm_fixture
  ADMIN_USER="admin"
  stub adm_script_is_file 'return 0'
  adm_ensure <<<'s3cret-pass-1234' >/dev/null
  assert_file_eq "$WORK/created.pw" 's3cret-pass-1234'
}

test_install_adm_ensure_never_reads_stdin_of_piped_script() {
  adm_fixture
  ADMIN_USER="admin"
  stub adm_script_is_file 'return 1'
  stub adm_has_tty 'return 0'
  stub adm_read_secret 'echo from-tty-1234567'
  adm_ensure <<<'#!/usr/bin/env bash' >/dev/null
  assert_file_eq "$WORK/created.pw" 'from-tty-1234567'
}

test_install_adm_ensure_prompts_when_interactive() {
  adm_fixture
  stub adm_has_tty 'return 0'
  stub adm_read_line 'echo admin2'
  stub adm_read_secret 'echo tty-secret-98765'
  adm_ensure >/dev/null
  assert_called '-username admin2'
  assert_file_eq "$WORK/created.pw" 'tty-secret-98765'
}

test_install_adm_ensure_asks_when_the_terminal_opens() {
  # the real adm_has_tty against a file standing in for the terminal (the reads are stubbed:
  # every read reopens $TTY, so a plain file would answer each prompt with its first line)
  adm_fixture
  TTY="$WORK/tty"
  : >"$TTY"
  adm_read_line() { printf 'admin4\n'; }
  adm_read_secret() { printf 'pw-from-tty-4444\n'; }
  adm_ensure >/dev/null
  assert_called '-username admin4'
  assert_file_eq "$WORK/created.pw" 'pw-from-tty-4444'
}

test_install_adm_read_secret_prompts_on_the_terminal() {
  TTY="$WORK/tty"
  printf 'hidden-1234\n' >"$TTY"
  assert_eq hidden-1234 "$(adm_read_secret 'password: ' 2>/dev/null)"
  assert_file_contains "$TTY" 'password: '
}

test_install_adm_ensure_retries_mismatched_passwords() {
  adm_fixture
  stub adm_has_tty 'return 0'
  stub adm_read_line 'echo admin3'
  # each call runs in a subshell: count in a file, not in a variable
  echo 0 >"$WORK/n"
  adm_read_secret() {
    local n
    n=$(($(cat "$WORK/n") + 1))
    echo "$n" >"$WORK/n"
    case "$n" in 1) echo aaaaaaaaaaaa ;; 2) echo bbbbbbbbbbbb ;; *) echo cccccccccccc ;; esac
  }
  adm_ensure >/dev/null
  assert_file_eq "$WORK/created.pw" cccccccccccc
}

test_install_adm_ensure_gives_up_after_three_mismatches() {
  adm_fixture
  stub adm_has_tty 'return 0'
  stub adm_read_line 'echo admin3'
  echo 0 >"$WORK/n"
  adm_read_secret() {
    local n
    n=$(($(cat "$WORK/n") + 1))
    echo "$n" >"$WORK/n"
    echo "pw-$n-xxxxxxxx"
  }
  assert_contains "$(adm_ensure)" "[warn] no admin was created"
  assert_not_called 'admin create'
}

test_install_adm_ensure_warns_and_continues_without_tty_and_user() {
  # CI and other non-interactive runs must neither hang nor fail (overview V4); the harness's
  # TTY does not exist.
  adm_fixture
  local out
  out="$(adm_ensure </dev/null)"
  assert_contains "$out" "[warn]"
  assert_contains "$out" "admin create -config /etc/mc-panel/panel.toml"
  assert_not_called 'admin create'
}

test_install_adm_ensure_warns_and_continues_when_create_fails() {
  adm_fixture
  ADMIN_USER="admin"
  printf 'short\n' >"$WORK/pw"
  ADMIN_PASSWORD_FILE="$WORK/pw"
  run_as() {
    printf 'run_as %s\n' "$*" >>"$WORK/calls.log"
    case "$*" in
    *"admin create"*) cat >/dev/null; return 1 ;;
    esac
  }
  local out
  out="$(adm_ensure)"
  assert_contains "$out" "[warn] panel admin create failed"
  assert_contains "$out" "[warn] no admin was created; create one later with: sudo runuser -u mcpanel -- /usr/local/bin/panel admin create -config /etc/mc-panel/panel.toml -username <name>"
  assert_not_contains "$out" "<password>"
  assert_eq 1 "$(grep -c 'admin create' "$WORK/calls.log")" # a file password is tried once
}

test_install_adm_ensure_asks_again_at_the_terminal_when_create_fails() {
  adm_fixture
  stub adm_has_tty 'return 0'
  echo 0 >"$WORK/n"
  adm_read_line() {
    local n
    n=$(($(cat "$WORK/n") + 1))
    echo "$n" >"$WORK/n"
    echo "admin$n"
  }
  stub adm_read_secret 'echo long-enough-secret-1'
  run_as() {
    printf 'run_as %s\n' "$*" >>"$WORK/calls.log"
    case "$*" in
    *"-username admin1"*) cat >/dev/null; return 1 ;; # rejected (policy)
    *"admin create"*) cat >"$WORK/created.pw" ;;
    esac
  }
  assert_contains "$(adm_ensure)" "[changed] admin admin2 created"
  assert_file_eq "$WORK/created.pw" long-enough-secret-1
}

test_install_adm_ensure_continues_when_list_fails() {
  adm_fixture
  run_as() { return 1; }
  assert_contains "$(adm_ensure)" "[warn] panel admin list failed"
}
