#!/usr/bin/env bash
# shellcheck shell=bash
# main.sh - entry point of install.sh (installer plan V4): arguments, subcommands, logging.
# Usage: install.sh <install|update|doctor|uninstall> [options]; see usage below.
#
# Only the very last line of the assembled script calls main, so a download that breaks off
# (curl | bash) never runs a partial script.

if ! declare -F log_step >/dev/null; then # running from the source tree (tests, make check)
  _src_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  # shellcheck source=lib.sh
  . "$_src_dir/lib.sh"
  for _f in "$_src_dir"/*.sh; do
    case "${_f##*/}" in
    lib.sh | main.sh | assemble.sh | test.sh) continue ;;
    esac
    # shellcheck disable=SC1090 # step files are found at run time; shellcheck checks each file itself
    . "$_f"
  done
  TEMPLATE_DIR="${TEMPLATE_DIR:-$_src_dir/../../deploy}"
  _SIGNERS_FILE="$_src_dir/release-signers"
  # assemble.sh embeds release-signers as this function
  release_signers() { cat -- "$_SIGNERS_FILE"; }
  unset _f _src_dir
fi

usage() {
  cat <<'EOF'
usage: install.sh <command> [options]

  install    --domain FQDN --email ADDRESS [--version vX.Y.Z | --from-dir DIR]
             [--tls letsencrypt|internal] [--ssh-port N] [--no-firewall]
             [--allow-port PORT[/tcp|/udp]]...
             [--admin-user NAME [--admin-password-file FILE]] [--allow-unsigned] [--yes]
  update     [--version vX.Y.Z | --from-dir DIR] [--check] [--force] [--allow-unsigned]
             [--yes]

Releases from v0.5.0 on are signed; install and update refuse one whose signature does not
verify. --allow-unsigned only admits the unsigned releases before v0.5.0.
  doctor     [--json]
  uninstall  [--purge] [--yes]

Example:
  curl -fsSL https://github.com/Bernotese/mc-panel/releases/latest/download/install.sh \
    | sudo bash -s -- install --domain panel.example.com --email you@example.com
EOF
}

# need_value OPT ARGS...: the option OPT needs a value (the first of ARGS).
need_value() {
  if [[ $# -lt 2 || -z $2 || $2 == --* ]]; then
    die "$1 needs a value"
  fi
}

# shellcheck disable=SC2034 # the options are globals that the step files read
parse_args() {
  if [[ $# -eq 0 ]]; then
    usage >&2
    exit 1
  fi
  CMD="$1"
  shift
  case "$CMD" in
  install | update | doctor | uninstall) ;;
  -h | --help | help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    die "unknown command: $CMD"
    ;;
  esac
  local opt
  while [[ $# -gt 0 ]]; do
    opt="$1"
    shift
    if [[ $opt == --*=* ]]; then
      set -- "${opt#*=}" "$@"
      opt="${opt%%=*}"
    fi
    case "$CMD:$opt" in
    *:-h | *:--help)
      usage
      exit 0
      ;;
    install:--domain)
      need_value "$opt" "$@"
      DOMAIN="$1"
      shift
      ;;
    install:--email)
      need_value "$opt" "$@"
      EMAIL="$1"
      shift
      ;;
    install:--version | update:--version)
      need_value "$opt" "$@"
      TARGET_VERSION="$1"
      shift
      ;;
    install:--from-dir | update:--from-dir)
      need_value "$opt" "$@"
      FROM_DIR="$1"
      shift
      ;;
    install:--tls)
      need_value "$opt" "$@"
      TLS_MODE="$1"
      shift
      ;;
    install:--ssh-port)
      need_value "$opt" "$@"
      SSH_PORT="$1"
      shift
      ;;
    install:--admin-user)
      need_value "$opt" "$@"
      ADMIN_USER="$1"
      shift
      ;;
    install:--admin-password-file)
      need_value "$opt" "$@"
      ADMIN_PASSWORD_FILE="$1"
      shift
      ;;
    install:--allow-port)
      need_value "$opt" "$@"
      FW_ALLOW_PORTS+=("$1") # firewall.sh
      shift
      ;;
    install:--no-firewall) NO_FIREWALL=1 ;;
    install:--allow-unsigned | update:--allow-unsigned) ALLOW_UNSIGNED=1 ;;
    update:--check) CHECK_ONLY=1 ;;
    update:--force) FORCE=1 ;;
    doctor:--json) JSON=1 ;;
    uninstall:--purge) PURGE=1 ;;
    install:--yes | update:--yes | uninstall:--yes) YES=1 ;;
    *)
      usage >&2
      die "unknown option for $CMD: $opt"
      ;;
    esac
  done
}

validate_args() {
  local fqdn='^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$'
  if [[ -n $TARGET_VERSION ]]; then
    [[ $TARGET_VERSION =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$ ]] ||
      die "--version: $TARGET_VERSION is not a version (vX.Y.Z)"
    TARGET_VERSION="${TARGET_VERSION#v}"
  fi
  if [[ -n $TARGET_VERSION && -n $FROM_DIR ]]; then
    die "--version and --from-dir exclude each other"
  fi
  if [[ -n $FROM_DIR ]]; then
    [[ -d $FROM_DIR ]] || die "--from-dir: $FROM_DIR is not a directory"
    FROM_DIR="$(cd "$FROM_DIR" && pwd)"
    # The build in FROM_DIR is the target of install and update: its own version, no network
    # (overview V4).
    [[ -x $FROM_DIR/panel ]] || die "--from-dir: $FROM_DIR/panel is missing"
    TARGET_VERSION="$("$FROM_DIR/panel" version)" || die "--from-dir: $FROM_DIR/panel version failed"
    TARGET_VERSION="${TARGET_VERSION#v}"
    [[ $TARGET_VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$ ]] ||
      die "--from-dir: $FROM_DIR/panel reports no release version ($TARGET_VERSION)"
  fi
  [[ -z $SSH_PORT || ($SSH_PORT =~ ^[0-9]+$ && $SSH_PORT -ge 1 && $SSH_PORT -le 65535) ]] ||
    die "--ssh-port: $SSH_PORT is not a port"
  local ap
  for ap in "${FW_ALLOW_PORTS[@]}"; do
    if ! [[ $ap =~ ^([0-9]{1,5})(/(tcp|udp))?$ ]] || ((10#${BASH_REMATCH[1]} < 1 || 10#${BASH_REMATCH[1]} > 65535)); then
      die "--allow-port: $ap is not PORT, PORT/tcp or PORT/udp"
    fi
  done
  [[ -z $ADMIN_USER || $ADMIN_USER =~ ^[A-Za-z0-9._-]{1,64}$ ]] || die "--admin-user: invalid name"
  if [[ -n $ADMIN_PASSWORD_FILE ]]; then
    # curl | bash: stdin is the script itself, so a password comes from a file (spec §4.10).
    [[ -n $ADMIN_USER ]] || die "--admin-password-file needs --admin-user"
    [[ -f $ADMIN_PASSWORD_FILE && ! -L $ADMIN_PASSWORD_FILE ]] || die "--admin-password-file: not a regular file"
    local pw_st
    pw_st="$(stat -c '%u %a' -- "$ADMIN_PASSWORD_FILE")"
    [[ ${pw_st%% *} == "$(_root_uid)" ]] || die "--admin-password-file must belong to root"
    [[ ${pw_st#* } == 600 || ${pw_st#* } == 400 ]] || die "--admin-password-file must have mode 0600 or 0400"
  fi
  if [[ $CMD != install ]]; then
    return 0
  fi
  case "$TLS_MODE" in
  letsencrypt)
    [[ -n $DOMAIN ]] || die "--domain is required (the panel's DNS name)"
    [[ -n $EMAIL ]] || die "--email is required for Let's Encrypt"
    ;;
  internal)
    if [[ -z $DOMAIN ]]; then
      DOMAIN=localhost
      warn "no --domain: using localhost with Caddy's internal certificate"
    fi
    ;;
  *) die "--tls must be letsencrypt or internal" ;;
  esac
  [[ $DOMAIN == localhost || $DOMAIN =~ $fqdn ]] || die "--domain: $DOMAIN is not a lowercase DNS name"
  [[ -z $EMAIL || $EMAIL =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]] || die "--email: invalid address"
  if [[ -z $TARGET_VERSION ]]; then
    [[ $MCPANEL_VERSION != dev ]] || die "this is a development build of install.sh: pass --version or --from-dir"
    TARGET_VERSION="${MCPANEL_VERSION#v}"
  fi
}

# release_tag VERSION: the git tag of a release (always with a leading v).
release_tag() {
  printf 'v%s\n' "${1#v}"
}

require_root() {
  [[ $(id -u) -eq 0 ]] || die "install.sh must run as root (sudo)"
}

detect_arch() {
  case "$(uname -m)" in
  x86_64 | amd64) echo amd64 ;;
  aarch64 | arm64) echo arm64 ;;
  *) die "unsupported architecture: $(uname -m) (amd64 and arm64 only)" ;;
  esac
}

setup_work() {
  # update hands over to the install.sh of the target version with exec (B2): the child takes
  # the parent's WORK (downloads are already there) and removes it at exit. Only a directory
  # that setup_work itself would have created qualifies: exactly
  # ${TMPDIR:-/tmp}/mc-panel-install.<alnum>, no symlink, owned by this user (root), mode 0700.
  local handoff="${MCPANEL_HANDOFF_WORK:-}" base="${TMPDIR:-/tmp}" suffix st
  unset MCPANEL_HANDOFF_WORK
  base="${base%/}"
  WORK=""
  if [[ -n $handoff && $handoff == "$base"/mc-panel-install.* ]]; then
    suffix="${handoff#"$base"/mc-panel-install.}"
    if [[ $suffix =~ ^[A-Za-z0-9]+$ && -d $handoff && ! -L $handoff ]]; then
      st="$(stat -c '%u %a' -- "$handoff")"
      if [[ $st == "$(id -u) 700" ]]; then
        WORK="$handoff"
      fi
    fi
  fi
  if [[ -z $WORK ]]; then
    WORK="$(mktemp -d "$base/mc-panel-install.XXXXXX")"
  fi
  # shellcheck disable=SC2064 # expand WORK now: the trap must remove exactly this directory
  trap "rm -rf -- $(printf '%q' "$WORK")" EXIT
}

setup_log() {
  local f="$ROOT$LOG_FILE"
  if [[ $(id -u) -ne 0 ]]; then
    return 0 # doctor without root: no log
  fi
  if [[ -L $f ]]; then
    die "$LOG_FILE is a symlink; refusing to log there"
  fi
  if [[ ! -e $f ]]; then
    (umask 077 && set -o noclobber && : >"$f") || die "cannot create $LOG_FILE"
  fi
  [[ -f $f && ! -L $f ]] || die "$LOG_FILE is not a regular file"
  chmod 0600 -- "$f"
}

cmd_install() {
  pre_check
  pkg_install
  lay_ensure
  bin_install
  cfg_ensure
  unit_ensure
  cad_ensure
  if [[ $NO_FIREWALL == 1 ]]; then
    log_step "firewall"
    log_skip "--no-firewall given"
  else
    fw_ensure
  fi
  upg_ensure
  adm_ensure
  install_summary
}

# shellcheck disable=SC2034 # DOC_SUMMARY is read by cmd_doctor (doctor.sh)
install_summary() {
  local rc=0 admins=""
  DOC_SUMMARY=1
  cmd_doctor || rc=$?
  DOC_SUMMARY=0
  admins="$(run_as mcpanel "$BIN_DIR/panel" admin list -config "$ETC_PANEL/panel.toml" 2>/dev/null)" || admins=""
  printf '\nmc-panel is installed: https://%s/\n' "$DOMAIN"
  if [[ -n $admins ]]; then
    printf 'Admin: %s\n' "${admins//$'\n'/, }"
  else
    printf 'No admin yet. Create one with:\n  sudo runuser -u mcpanel -- %s/panel admin create -config %s/panel.toml -username NAME\n' \
      "$BIN_DIR" "$ETC_PANEL"
  fi
  if [[ $rc -ne 0 ]]; then
    printf 'doctor reported %s; run: sudo bash install.sh doctor\n' "$([[ $rc -eq 1 ]] && echo warnings || echo errors)"
  fi
  printf '%s\n' \
    "Next steps:" \
    "  1. Log in and set up two-factor authentication." \
    "  2. Settings: enter the base domain for Minecraft servers (wildcard DNS *.<domain> -> this host)." \
    "  3. Settings: add a notification channel (Discord or ntfy)." \
    "  4. Back up $INSTANCES_DIR/backups/keys/ - without it the backups cannot be read."
}

# shellcheck disable=SC2034 # ARCH is read by the step files (binaries.sh)
main() {
  set -euo pipefail
  umask 022
  parse_args "$@"
  validate_args
  if [[ $CMD != doctor ]]; then
    require_root
  fi
  ARCH="$(detect_arch)"
  setup_work
  setup_log
  _log "install.sh $MCPANEL_VERSION: $CMD $*"
  # Each command runs in a subshell with errexit on and outside any conditional context:
  # `cmd || rc=$?` would switch errexit off for the whole call tree (overview V4).
  local rc
  if [[ $CMD == update ]]; then
    # Ctrl-C, kill or a dropped SSH session must not end this shell while update rolls back in
    # the subshell: caught here (not ignored, so the subshell can trap them itself), this shell
    # waits for the subshell and exits with its code (update.sh).
    trap : INT TERM HUP
  fi
  set +e
  (
    set -e
    "cmd_$CMD"
  )
  rc=$?
  set -e
  exit "$rc"
}

if [[ ${MCPANEL_INSTALL_NO_MAIN:-0} != 1 ]]; then main "$@"; fi
