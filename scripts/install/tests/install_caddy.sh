# shellcheck shell=bash
# Tests of caddy.sh (installer spec §4.7, review focus 2). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329

cad_fixture() {
  DOMAIN=panel.example.com TLS_MODE=letsencrypt EMAIL=a@example.com TARGET_VERSION=1.2.3
  stub lay_uid 'echo 998'
  stub cad_wait ':'
  dpkg-query() { echo " /etc/caddy/Caddyfile 0123456789abcdef0123456789abcdef"; }
  mkdir -p "$ROOT$CADDY_DIR"
}

cad_foreign() {
  printf 'blog.example.com {\n\troot * /srv/blog\n\tfile_server\n}\n' >"$ROOT$CADDY_DIR/Caddyfile"
}

test_install_cad_ensure_creates_caddyfile_when_missing() {
  cad_fixture
  local out
  out="$(cad_ensure)"
  assert_contains "$out" "[changed] site panel.example.com"
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'import mc-panel.caddy # managed by mc-panel'
  assert_not_contains "$(cat "$ROOT$CADDY_DIR/Caddyfile")" 'skip_install_trust'
  assert_file_contains "$ROOT$CADDY_DIR/mc-panel.caddy" 'panel.example.com {'
  assert_file_contains "$ROOT$CADDY_DIR/mc-panel.caddy" 'tls a@example.com'
  assert_file_contains "$ROOT$CADDY_DIR/mc-panel.caddy" 'reverse_proxy unix//run/mc-panel-web/panel.sock'
  assert_eq 644 "$(stat -c '%a' "$ROOT$CADDY_DIR/mc-panel.caddy")"
  assert_called "runuser -u caddy -- env HOME=/var/lib/caddy caddy validate --config $ROOT$CADDY_DIR/Caddyfile --adapter caddyfile"
  assert_called 'systemctl reload-or-restart caddy.service'
}

test_install_cad_ensure_appends_import_once_to_foreign_caddyfile() {
  cad_fixture
  cad_foreign
  cp "$ROOT$CADDY_DIR/Caddyfile" "$WORK/before"
  cad_ensure >/dev/null
  cad_ensure >/dev/null
  assert_eq 1 "$(grep -c '^import mc-panel.caddy' "$ROOT$CADDY_DIR/Caddyfile")"
  assert_eq "$(cat "$WORK/before")"$'\n\n'"import mc-panel.caddy # managed by mc-panel" "$(cat "$ROOT$CADDY_DIR/Caddyfile")"
}

test_install_cad_ensure_appends_after_a_missing_final_newline() {
  cad_fixture
  printf 'blog.example.com {\n\tfile_server\n}' >"$ROOT$CADDY_DIR/Caddyfile"
  cad_ensure >/dev/null
  assert_eq '}' "$(sed -n 3p "$ROOT$CADDY_DIR/Caddyfile")"
  assert_eq 'import mc-panel.caddy # managed by mc-panel' "$(tail -n 1 "$ROOT$CADDY_DIR/Caddyfile")"
}

test_install_cad_ensure_marks_an_existing_unmarked_import() {
  # the test VM frame or an operator wrote the line without the marker: marked, not doubled
  cad_fixture
  printf 'blog.example.com {\n\tfile_server\n}\nimport mc-panel.caddy\nimport other.caddy\n' >"$ROOT$CADDY_DIR/Caddyfile"
  cad_ensure >/dev/null
  assert_eq 1 "$(grep -c '^import mc-panel.caddy' "$ROOT$CADDY_DIR/Caddyfile")"
  assert_eq 'import mc-panel.caddy # managed by mc-panel' "$(sed -n 4p "$ROOT$CADDY_DIR/Caddyfile")"
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'import other.caddy'
}

test_install_cad_ensure_replaces_untouched_package_default() {
  cad_fixture
  printf ':80 {\n\troot * /usr/share/caddy\n\tfile_server\n}\n' >"$ROOT$CADDY_DIR/Caddyfile"
  CAD_TEST_MD5="$(md5sum "$ROOT$CADDY_DIR/Caddyfile" | cut -d' ' -f1)"
  dpkg-query() { printf ' /etc/caddy/Caddyfile %s\n' "$CAD_TEST_MD5"; }
  cad_ensure >/dev/null
  assert_eq 0 "$(grep -c '/usr/share/caddy' "$ROOT$CADDY_DIR/Caddyfile")"
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'import mc-panel.caddy # managed by mc-panel'
}

test_install_cad_ensure_keeps_a_modified_package_default() {
  cad_fixture
  printf ':80 {\n\troot * /var/www\n\tfile_server\n}\n' >"$ROOT$CADDY_DIR/Caddyfile"
  cad_ensure >/dev/null
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'root * /var/www'
}

test_install_cad_ensure_internal_sets_skip_install_trust_only_in_own_file() {
  cad_fixture
  TLS_MODE=internal EMAIL=''
  cad_ensure >/dev/null
  assert_eq '{' "$(grep -v '^#' "$ROOT$CADDY_DIR/Caddyfile" | head -n 1)"
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'skip_install_trust'
  assert_file_contains "$ROOT$CADDY_DIR/mc-panel.caddy" 'tls internal'
  assert_not_contains "$(cat "$ROOT$CADDY_DIR/mc-panel.caddy")" 'skip_install_trust'
}

test_install_cad_ensure_internal_leaves_foreign_global_options() {
  cad_fixture
  TLS_MODE=internal EMAIL=''
  cad_foreign
  cad_ensure >/dev/null
  assert_not_contains "$(cat "$ROOT$CADDY_DIR/Caddyfile")" 'skip_install_trust'
}

test_install_cad_ensure_restores_caddyfile_when_validate_fails() {
  cad_fixture
  cad_foreign
  cp "$ROOT$CADDY_DIR/Caddyfile" "$WORK/before"
  run_as() { return 1; } # caddy validate fails
  assert_fails cad_ensure
  assert_file_eq "$ROOT$CADDY_DIR/Caddyfile" "$(cat "$WORK/before")"
  assert_no_file "$ROOT$CADDY_DIR/mc-panel.caddy"
  assert_not_called 'reload-or-restart caddy'
}

test_install_cad_ensure_restores_previous_site_when_validate_fails() {
  cad_fixture
  cad_ensure >/dev/null
  cp "$ROOT$CADDY_DIR/mc-panel.caddy" "$WORK/site.before"
  DOMAIN=new.example.com
  run_as() { return 1; } # caddy validate fails
  assert_fails cad_ensure
  assert_file_eq "$ROOT$CADDY_DIR/mc-panel.caddy" "$(cat "$WORK/site.before")"
}

test_install_cad_ensure_refuses_symlinked_caddyfile() {
  cad_fixture
  cad_foreign
  mv "$ROOT$CADDY_DIR/Caddyfile" "$WORK/real"
  ln -s "$WORK/real" "$ROOT$CADDY_DIR/Caddyfile"
  assert_fails cad_ensure
  assert_not_contains "$(cat "$WORK/real")" 'import mc-panel.caddy'
}

test_install_cad_ensure_second_run_does_not_reload() {
  cad_fixture
  cad_ensure >/dev/null
  : >"$WORK/calls.log"
  assert_contains "$(cad_ensure)" "[skip]"
  assert_not_called 'reload-or-restart caddy'
  assert_not_called 'caddy validate'
}

test_install_cad_ensure_keeps_owner_and_mode_of_the_caddyfile() {
  cad_fixture
  cad_foreign
  chmod 0640 "$ROOT$CADDY_DIR/Caddyfile"
  _owner_set "$CADDY_DIR/Caddyfile" root caddy 0640
  cad_ensure >/dev/null
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'import mc-panel.caddy # managed by mc-panel'
  assert_eq 640 "$(stat -c '%a' "$ROOT$CADDY_DIR/Caddyfile")"
  assert_eq root:caddy "$(_owner_get "$CADDY_DIR/Caddyfile")"
  assert_called "chown -h root:caddy $ROOT$CADDY_DIR/.mcpanel-install."
}

test_install_cad_ensure_restores_when_the_reload_fails() {
  cad_fixture
  cad_foreign
  cp "$ROOT$CADDY_DIR/Caddyfile" "$WORK/before"
  sys_systemctl() {
    printf 'systemctl %s\n' "$*" >>"$WORK/calls.log"
    [[ $1 != reload-or-restart ]]
  }
  assert_fails cad_ensure
  assert_contains "$(cat "$WORK/out")" "did not reload"
  assert_file_eq "$ROOT$CADDY_DIR/Caddyfile" "$(cat "$WORK/before")"
  assert_no_file "$ROOT$CADDY_DIR/mc-panel.caddy"
  assert_eq 2 "$(grep -c 'reload-or-restart caddy' "$WORK/calls.log")" # again with the old files
}

test_install_cad_ensure_merges_indented_and_duplicate_imports() {
  cad_fixture
  printf 'blog.example.com {\n\tfile_server\n}\n  import mc-panel.caddy\nimport mc-panel.caddy # managed by mc-panel\nimport  mc-panel.caddy\n' >"$ROOT$CADDY_DIR/Caddyfile"
  cad_ensure >/dev/null
  assert_eq 1 "$(grep -c 'mc-panel.caddy' "$ROOT$CADDY_DIR/Caddyfile")"
  assert_eq 'import mc-panel.caddy # managed by mc-panel' "$(sed -n 4p "$ROOT$CADDY_DIR/Caddyfile")"
  : >"$WORK/calls.log"
  cad_ensure >/dev/null
  assert_not_called 'caddy validate'
}

test_install_cad_ensure_ignores_other_imports() {
  cad_fixture
  printf 'import mc-panel.caddy.bak\nimport other.caddy\n' >"$ROOT$CADDY_DIR/Caddyfile"
  cad_ensure >/dev/null
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'import mc-panel.caddy.bak'
  assert_eq 'import mc-panel.caddy # managed by mc-panel' "$(tail -n 1 "$ROOT$CADDY_DIR/Caddyfile")"
}
