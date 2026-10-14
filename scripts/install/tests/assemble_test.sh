# shellcheck shell=bash
# Tests of assemble.sh (installer plan V5).

_assemble() { # _assemble VERSION -> path of the assembled script
  local repo
  repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
  "$repo/scripts/install/assemble.sh" "$1" "$WORK/install.sh" >/dev/null
  printf '%s' "$WORK/install.sh"
}

test_assemble_embeds_version_and_templates() {
  local f
  f="$(_assemble v1.2.3)"
  bash -n "$f"
  assert_file_contains "$f" "MCPANEL_VERSION=1.2.3"
  assert_file_contains "$f" "tpl_systemd_mcpanel_panel_socket() {"
  assert_file_contains "$f" "tpl_config_panel_toml() {"
  [[ $(head -n1 "$f") == '#!/usr/bin/env bash' ]] || fail "shebang"
  [[ $(grep -c '^#!' "$f") == 1 ]] || fail "more than one shebang"
}

test_assembled_script_renders_embedded_templates() {
  local f out
  f="$(_assemble 1.2.3)"
  out="$(MCPANEL_INSTALL_NO_MAIN=1 bash -c '
    . "$1"
    KEY_VERSION=1.2.3 KEY_INSTANCES_DIR=/srv/mc-panel
    render_template systemd/mcpanel-files.service' _ "$f")"
  assert_contains "$out" "ReadWritePaths=/srv/mc-panel/instances /srv/mc-panel/backups"
  assert_contains "$out" "install.sh 1.2.3"
}

test_assembled_script_shows_usage() {
  local f
  f="$(_assemble 1.2.3)"
  # test.sh exports MCPANEL_INSTALL_NO_MAIN=1; the assembled script must run main here.
  assert_exit 0 env -u MCPANEL_INSTALL_NO_MAIN bash "$f" --help
  assert_contains "$(cat "$WORK/out")" "usage: install.sh"
  assert_exit 1 env -u MCPANEL_INSTALL_NO_MAIN bash "$f" frobnicate
  assert_contains "$(cat "$WORK/out")" "unknown command: frobnicate"
}

test_assemble_partial_runs_nothing() {
  local f
  f="$(_assemble 1.2.3)"
  head -n -1 "$f" >"$WORK/partial.sh"
  # Without NO_MAIN the full script would run main (and print the usage for --help).
  assert_exit 0 env -u MCPANEL_INSTALL_NO_MAIN bash "$WORK/partial.sh" --help
  assert_eq "" "$(cat "$WORK/out")"
}

test_assemble_rejects_bad_version() {
  local repo
  repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
  assert_exit 2 "$repo/scripts/install/assemble.sh" '1.2;rm' "$WORK/x.sh"
  assert_exit 2 "$repo/scripts/install/assemble.sh" "" "$WORK/x.sh"
}
