# shellcheck shell=bash
# lib.sh - shared helpers of install.sh (installer plan V2): constants, globals with defaults,
# logging, templates, file writes, downloads, checksums and the sys_* wrappers that tests
# (MCPANEL_INSTALL_DRYRUN=1) only record. Sourced first; defines no side effects besides
# variables. Every system path below is unprefixed; file operations prefix it with $ROOT.

MCPANEL_VERSION=dev

# shellcheck disable=SC2034 # constants of all steps; the step files read them
{
  BIN_DIR=/usr/local/bin
  ETC_PANEL=/etc/mc-panel
  ETC_FILES=/etc/mc-panel-files
  DATA_DIR=/var/lib/mc-panel
  INSTANCES_DIR=/srv/mc-panel
  UNIT_DIR=/etc/systemd/system
  CADDY_DIR=/etc/caddy
  LOG_FILE=/var/log/mc-panel-install.log
  ROLLBACK_DIR=/var/lib/mc-panel-rollback # root:root 0700, outside mcpanel's data dir (overview Global Constraints)
  RELEASE_BASE=https://github.com/Bernotese/mc-panel/releases
  TEMPLATE_DELIM=__MCPANEL_TEMPLATE_EOF__
}

# reset_globals sets every global the command line can change to its default (main.sh's
# parse_args fills them; tests start from these values).
# shellcheck disable=SC2034 # globals of all steps; the step files read them
reset_globals() {
  CMD=""
  DOMAIN=""
  EMAIL=""
  TLS_MODE=letsencrypt
  TARGET_VERSION=""
  FROM_DIR=""
  SSH_PORT=""
  NO_FIREWALL=0
  ADMIN_USER=""
  ADMIN_PASSWORD_FILE=""
  DOC_SUMMARY=0
  YES=0
  PURGE=0
  CHECK_ONLY=0
  FORCE=0
  JSON=0
  ARCH=""
  BIN_SOURCE_DIR="" # extracted release tarball (binaries.sh); its deploy/ wins in render_template
  WORK="${WORK:-}"
}
reset_globals

# Only tests set these; an operator's ROOT variable is never read.
ROOT="${MCPANEL_INSTALL_ROOT:-}"
DRYRUN="${MCPANEL_INSTALL_DRYRUN:-0}"
TTY="${MCPANEL_INSTALL_TTY:-/dev/tty}"
TEMPLATE_DIR="${TEMPLATE_DIR:-}"

# --- logging -------------------------------------------------------------------------------

# _log TEXT appends to the install log when it exists (best effort, never fails).
_log() {
  local f="$ROOT$LOG_FILE"
  if [[ -w $f && ! -L $f ]]; then
    printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" >>"$f" 2>/dev/null || true
  fi
  return 0
}

log_step() {
  printf '\n==> %s\n' "$1"
  _log "== $1"
}
# Step output on stdout, one prefix per result (the CI and doctor tests check these prefixes).
log_ok() {
  printf '[ok] %s\n' "$1"
  _log "[ok] $1"
}
log_changed() {
  printf '[changed] %s\n' "$1"
  _log "[changed] $1"
}
log_skip() {
  printf '[skip] %s\n' "$1"
  _log "[skip] $1"
}
warn() {
  printf '[warn] %s\n' "$1"
  _log "[warn] $1"
}

# die MSG [CODE]: message, pointer to the log, exit CODE (default 1). Inside $(...) only the
# subshell ends; callers rely on set -e (main.sh) to stop.
die() {
  printf 'install.sh: error: %s\n' "$1" >&2
  if [[ -e $ROOT$LOG_FILE ]]; then
    printf '  details: %s\n' "$LOG_FILE" >&2
  fi
  _log "error $1"
  exit "${2:-1}"
}

# confirm PROMPT: 0 = yes. YES=1 answers yes without asking; without a terminal the answer
# is no. curl | bash has no stdin and stdout may be a log, so prompt and answer both go
# through $TTY (/dev/tty; read -p would print the prompt only when stdin is a terminal).
confirm() {
  if [[ $YES == 1 ]]; then
    return 0
  fi
  local ans=""
  if ! { [[ -r $TTY && -w $TTY ]] && printf '%s [y/N] ' "$1" >>"$TTY" && read -r ans <"$TTY"; } 2>/dev/null; then
    return 1
  fi
  [[ $ans =~ ^[yYjJ] ]]
}

# --- versions ------------------------------------------------------------------------------

# _prerelease_ge A B: 0 if pre-release A >= B (SemVer 11.4: dot-separated identifiers,
# numeric ones compared as numbers and lower than alphanumeric ones, a shorter list of equal
# identifiers is lower).
_prerelease_ge() {
  local LC_ALL=C i x y
  local -a pa pb
  IFS=. read -r -a pa <<<"$1"
  IFS=. read -r -a pb <<<"$2"
  for ((i = 0; ; i++)); do
    if ((i >= ${#pb[@]})); then
      return 0
    fi
    if ((i >= ${#pa[@]})); then
      return 1
    fi
    x="${pa[i]}"
    y="${pb[i]}"
    if [[ $x =~ ^[0-9]+$ && $y =~ ^[0-9]+$ ]]; then
      if ((10#$x > 10#$y)); then
        return 0
      fi
      if ((10#$x < 10#$y)); then
        return 1
      fi
    elif [[ $x =~ ^[0-9]+$ ]]; then
      return 1
    elif [[ $y =~ ^[0-9]+$ ]]; then
      return 0
    elif [[ $x > $y ]]; then
      return 0
    elif [[ $x < $y ]]; then
      return 1
    fi
  done
}

# version_ge A B: 0 if version A >= B. MAJOR.MINOR.PATCH with optional leading v,
# -prerelease (older than the release, compared per SemVer) and +build (ignored); 2 for a
# malformed version.
version_ge() {
  local a="${1#v}" b="${2#v}" ap="" bp="" i x y
  a="${a%%+*}"
  b="${b%%+*}"
  if [[ $a == *-* ]]; then
    ap="${a#*-}"
    a="${a%%-*}"
  fi
  if [[ $b == *-* ]]; then
    bp="${b#*-}"
    b="${b%%-*}"
  fi
  local -a av bv
  IFS=. read -r -a av <<<"$a"
  IFS=. read -r -a bv <<<"$b"
  if [[ ${#av[@]} -ne 3 || ${#bv[@]} -ne 3 ]]; then
    return 2
  fi
  for i in 0 1 2; do
    x="${av[i]}"
    y="${bv[i]}"
    if ! [[ $x =~ ^[0-9]+$ && $y =~ ^[0-9]+$ ]]; then
      return 2
    fi
    if ((10#$x > 10#$y)); then
      return 0
    fi
    if ((10#$x < 10#$y)); then
      return 1
    fi
  done
  if [[ -z $ap ]]; then
    return 0
  fi
  if [[ -z $bp ]]; then
    return 1
  fi
  _prerelease_ge "$ap" "$bp"
}

# --- templates -----------------------------------------------------------------------------

# tpl_func_suffix PATH: the name part of the embedded template function for PATH below
# deploy/ ("systemd/mcpanel-agent.service" -> "systemd_mcpanel_agent_service"); must match
# assemble.sh.
tpl_func_suffix() {
  local s="${1//\//_}"
  s="${s//./_}"
  s="${s//-/_}"
  printf '%s' "$s"
}

# render_template NAME: template NAME (path below deploy/) on stdout with every {{KEY}}
# replaced by the value of KEY_<KEY>. Source (installer plan V5): the deploy/ of the release
# being installed wins - ${BIN_SOURCE_DIR:-$FROM_DIR}/deploy, i.e. the extracted release
# tarball (binaries.sh sets BIN_SOURCE_DIR) or the local build of --from-dir -, else the
# function embedded by assemble.sh, else $TEMPLATE_DIR (source tree). A placeholder without
# value or an unresolved "{{" ends the run.
render_template() {
  local name="$1" dir="${BIN_SOURCE_DIR:-$FROM_DIR}" fn src out="" rest key var
  fn="tpl_$(tpl_func_suffix "$name")"
  if [[ -n $dir ]]; then
    [[ -f $dir/deploy/$name ]] || die "template deploy/$name is missing in $dir"
    src="$(cat -- "$dir/deploy/$name")"
  elif declare -F "$fn" >/dev/null; then
    src="$("$fn")"
  elif [[ -n $TEMPLATE_DIR && -f $TEMPLATE_DIR/$name ]]; then
    src="$(cat -- "$TEMPLATE_DIR/$name")"
  else
    die "unknown template $name"
  fi
  rest="$src"
  while [[ $rest =~ \{\{([A-Z0-9_]+)\}\} ]]; do
    key="${BASH_REMATCH[1]}"
    var="KEY_$key"
    if [[ -z ${!var+x} ]]; then
      die "template $name: no value for {{$key}} ($var)"
    fi
    out+="${rest%%"{{$key}}"*}${!var}"
    rest="${rest#*"{{$key}}"}"
  done
  out+="$rest"
  if [[ $out == *'{{'* ]]; then
    die "template $name: unresolved placeholder"
  fi
  printf '%s\n' "$out"
}

# write_template NAME DEST MODE OWNER GROUP: renders template NAME completely first and
# writes it with write_file only if that succeeded, so a missing KEY_* never leaves DEST
# empty or half-written. Prints changed/unchanged.
write_template() {
  local content
  content="$(render_template "$1")" || return "$?"
  printf '%s\n' "$content" | write_file "$2" "$3" "$4" "$5"
}

# --- files ---------------------------------------------------------------------------------

# _owner_get PATH: "owner:group" of $ROOT$PATH. In dry-run mode (tests run as a normal user)
# owners are simulated in $WORK/owners, lines "path owner group mode", the last line per path
# wins; a path without line reports the real owner.
_owner_get() {
  local line=""
  if [[ $DRYRUN == 1 && -f $WORK/owners ]]; then
    line="$(awk -v p="$1" '$1 == p { l = $2 ":" $3 } END { print l }' "$WORK/owners")"
  fi
  if [[ -n $line ]]; then
    printf '%s' "$line"
  else
    stat -c '%U:%G' -- "$ROOT$1"
  fi
}

# _root_uid: the uid that must own the directories root writes into: 0, in dry-run mode the
# test user.
_root_uid() {
  if [[ $DRYRUN == 1 ]]; then
    id -u
  else
    echo 0
  fi
}

# _dir_safe DIR SHOWN: DIR (with $ROOT) may receive files from root: no symlink, owned by root,
# not writable by group or others - otherwise another user could swap what root writes there.
_dir_safe() {
  local st
  [[ ! -L $1 ]] || die "$2 is a symlink; refusing to write below it"
  st="$(stat -c '%u %a' -- "$1")" || die "cannot inspect $2"
  [[ ${st%% *} == "$(_root_uid)" ]] || die "$2 is not owned by root; refusing to write below it"
  (((8#${st#* } & 8#022) == 0)) || die "$2 is writable by group or others; refusing to write below it"
}

# _owner_set PATH OWNER GROUP MODE: records the simulated owner (dry-run only).
_owner_set() {
  if [[ $DRYRUN == 1 ]]; then
    printf '%s %s %s %s\n' "$1" "$2" "$3" "$4" >>"$WORK/owners"
  fi
}

# write_file [--allow-empty] DEST MODE OWNER GROUP < content: writes stdin to $ROOT$DEST
# atomically (temp file in the same directory, fsync, then rename) unless content, mode and
# owner already match; prints "changed" or "unchanged". The directory must exist, belong to
# root and not be writable by group or others. Empty input is refused unless --allow-empty
# (a failed render must never truncate a file).
write_file() {
  local allow_empty=0
  if [[ ${1:-} == --allow-empty ]]; then
    allow_empty=1
    shift
  fi
  local dest="$ROOT$1" mode="$2" owner="$3" group="$4" tmp
  _dir_safe "${dest%/*}" "${1%/*}"
  tmp="$(mktemp "${dest%/*}/.mcpanel-install.XXXXXX")" || die "cannot write to ${1%/*}"
  cat >"$tmp"
  if [[ $allow_empty == 0 && ! -s $tmp ]]; then
    rm -f -- "$tmp"
    die "refusing to write an empty $1"
  fi
  if [[ -f $dest && ! -L $dest ]] && cmp -s -- "$tmp" "$dest" && [[ $(stat -c '%a' -- "$dest") == "${mode#0}" ]] &&
    [[ $(_owner_get "$1") == "$owner:$group" ]]; then
    rm -f -- "$tmp"
    echo unchanged
    return 0
  fi
  chmod "$mode" -- "$tmp"
  sys_chown -h "$owner:$group" "$tmp"
  sync -- "$tmp"
  mv -fT -- "$tmp" "$dest"
  _owner_set "$1" "$owner" "$group" "$mode"
  echo changed
}

# ensure_dir PATH MODE OWNER GROUP: creates $ROOT$PATH (with parents) or corrects mode and
# owner of an existing directory; never follows a symlink at PATH. The nearest existing parent
# must belong to root and not be writable by group or others. Prints changed/unchanged.
ensure_dir() {
  local dir="$ROOT$1" mode="$2" owner="$3" group="$4" changed=0 parent="${1%/*}"
  if [[ -L $dir ]]; then
    die "$1 is a symlink; refusing to use it"
  fi
  while [[ -n $parent && ! -e $ROOT$parent && ! -L $ROOT$parent ]]; do
    parent="${parent%/*}"
  done
  _dir_safe "$ROOT${parent:-/}" "${parent:-/}"
  if [[ ! -d $dir ]]; then
    [[ ! -e $dir ]] || die "$1 exists but is not a directory"
    mkdir -p -- "$dir"
    changed=1
  fi
  if [[ $(stat -c '%a' -- "$dir") != "${mode#0}" ]]; then
    chmod "$mode" -- "$dir"
    changed=1
  fi
  if [[ $changed == 1 || $(_owner_get "$1") != "$owner:$group" ]]; then
    sys_chown -h "$owner:$group" "$dir"
    _owner_set "$1" "$owner" "$group" "$mode"
    changed=1
  fi
  if [[ $changed == 1 ]]; then echo changed; else echo unchanged; fi
}

# toml_ensure_key FILE KEY VALUE: adds "KEY = VALUE" (VALUE is a TOML literal, e.g. "\"x\"" or
# 5) when KEY is missing on the top level of FILE (before the first [section]); inserts it
# before that section with a comment; never changes an existing value. Prints changed/unchanged.
toml_ensure_key() {
  local file="$ROOT$1" key="$2" value="$3" mode og
  [[ ! -L $file ]] || die "$1 is a symlink; refusing to use it"
  [[ -f $file ]] || die "$1 is missing"
  if awk -v k="$key" '
      /^[[:space:]]*\[/ { exit }
      { line = $0; sub(/^[[:space:]]+/, "", line)
        if (index(line, k) == 1) { r = substr(line, length(k) + 1); sub(/^[[:space:]]+/, "", r)
          if (substr(r, 1, 1) == "=") { found = 1; exit } } }
      END { exit !found }' "$file"; then
    echo unchanged
    return 0
  fi
  mode="$(stat -c '%a' -- "$file")"
  og="$(_owner_get "$1")"
  awk -v line="$key = $value" -v note="# added by install.sh $MCPANEL_VERSION" '
      !done && /^[[:space:]]*\[/ { print note; print line; print ""; done = 1 }
      { print }
      END { if (!done) { print note; print line } }' "$file" | write_file "$1" "0$mode" "${og%%:*}" "${og#*:}"
}

# --- downloads -----------------------------------------------------------------------------

# fetch URL DEST: HTTPS only (also for redirects), TLS >= 1.2, 3 retries; gives up after 10
# minutes or when the transfer stays below 1 KiB/s for a minute.
fetch() {
  case "$1" in
  https://*) ;;
  *) die "refusing a non-HTTPS download: $1" ;;
  esac
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL --retry 3 --retry-delay 2 \
    --connect-timeout 15 --max-time 600 --speed-limit 1024 --speed-time 60 \
    -o "$2" -- "$1" || die "download failed: $1"
}

# sha256_verify FILE CHECKSUMS NAME: the single line for NAME in CHECKSUMS (sha256sum format,
# as goreleaser writes checksums.txt) must match FILE.
sha256_verify() {
  local n want got
  [[ -f $2 ]] || die "checksums file ${2##*/} is missing"
  n="$(awk -v n="$3" '$2 == n || $2 == "*" n { c++ } END { print c + 0 }' "$2")"
  [[ $n == 1 ]] || die "checksums: expected one entry for $3, found $n"
  want="$(awk -v n="$3" '$2 == n || $2 == "*" n { print $1 }' "$2")"
  [[ $want =~ ^[0-9a-f]{64}$ ]] || die "checksums: malformed entry for $3"
  got="$(sha256sum -- "$1" | awk '{ print $1 }')"
  [[ $got == "$want" ]] || die "checksum mismatch for $3 (expected $want, got $got)"
}

# --- system wrappers -----------------------------------------------------------------------

# _sys CMD...: runs CMD; in dry-run mode only appends it to $WORK/calls.log and succeeds.
# calls.log format: one line per call, the command and its arguments exactly as passed, joined
# by single spaces ("$*"), e.g. "systemctl daemon-reload",
# "env DEBIAN_FRONTEND=noninteractive apt-get -y -q install caddy", "chown -h root:root /x",
# "runuser -u mcpanel -- /usr/local/bin/panel admin list". Tests match these lines as fixed
# strings (assert_called).
_sys() {
  if [[ $DRYRUN == 1 ]]; then
    printf '%s\n' "$*" >>"$WORK/calls.log"
    return 0
  fi
  "$@"
}
sys_apt() { _sys env DEBIAN_FRONTEND=noninteractive apt-get -y -q "$@"; }
sys_apt_mark() { _sys apt-mark "$@"; }
sys_systemctl() { _sys systemctl "$@"; }
sys_useradd() { _sys useradd "$@"; }
sys_userdel() { _sys userdel "$@"; }
sys_nft() { _sys nft "$@"; }
sys_docker() { _sys docker "$@"; }
sys_caddy() { _sys caddy "$@"; }
sys_chown() { _sys chown "$@"; }
sys_install() { _sys install "$@"; }
sys_runuser() {
  local user="$1"
  shift
  _sys runuser -u "$user" -- "$@"
}

# run_as USER CMD...: runs CMD as USER.
run_as() { sys_runuser "$@"; }

# systemd_wait_active UNIT SECS: 0 once UNIT is active, 1 after SECS seconds.
systemd_wait_active() {
  local i
  for ((i = 0; i <= $2; i++)); do
    if sys_systemctl is-active --quiet "$1"; then
      return 0
    fi
    if ((i < $2)); then
      sleep 1
    fi
  done
  return 1
}
