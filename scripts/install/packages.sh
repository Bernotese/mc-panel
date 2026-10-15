# shellcheck shell=bash
# packages.sh - installer spec §4.2: packages from the Ubuntu archive (only missing ones),
# restic >= 0.17, a Docker daemon that answers, and Docker's daemon.json only when it is missing.
# Caddy comes from the official Caddy repository (SEC-01: universe has no security updates for
# it): its signing key is verified against the pinned fingerprint before apt trusts it.

PKG_LIST=(caddy nftables restic tzdata sqlite3 unattended-upgrades curl ca-certificates bzip2)
PKG_DOCKER=(docker.io)
PKG_RESTIC_MIN=0.17.0
PKG_RESTIC_BIN=""    # set by pkg_install: path of a restic >= PKG_RESTIC_MIN (cfg_keys reads it)
PKG_DOCKER_WAIT=30   # seconds to wait for the Docker daemon to answer
PKG_APT_LOCK_WAIT=300 # seconds apt waits for the dpkg lock (unattended-upgrades on a fresh host)
PKG_CADDY_KEY_URL=https://dl.cloudsmith.io/public/caddy/stable/gpg.key
# "Caddy Web Server <contact@caddyserver.com>", the same at keyserver.ubuntu.com.
PKG_CADDY_KEY_FPR=65760C51EDEA2017CEA2CA15155B6D79CA56EA34
PKG_CADDY_KEYRING=/usr/share/keyrings/caddy-stable-archive-keyring.gpg # the paths of Caddy's
PKG_CADDY_SOURCES=/etc/apt/sources.list.d/caddy-stable.list            # own install guide
PKG_CADDY_REPO=https://dl.cloudsmith.io/public/caddy/stable/deb/debian
# shellcheck disable=SC2034 # read by doctor.sh
PKG_CADDY_MIN=2.10.0 # doctor warns below this version

# pkg_missing NAME... prints the given packages that are not installed, one per line.
pkg_missing() {
  local p
  for p in "$@"; do
    if [[ "$(dpkg-query -W -f='${Status}' "$p" 2>/dev/null)" != "install ok installed" ]]; then
      echo "$p"
    fi
  done
}

# pkg_docker_answers: 0 when a Docker daemon answers (also a preinstalled Docker CE, whose
# containerd.io conflicts with docker.io). A query; tests stub it.
pkg_docker_answers() {
  docker info >/dev/null 2>&1
}

# pkg_running_containers prints the IDs of all running containers - a Docker restart without
# live-restore stops every one of them, not only mc-panel's (a query; tests stub it).
pkg_running_containers() {
  docker ps -q 2>/dev/null || true
}

# pkg_swarm_active: 0 if this Docker is a swarm node (live-restore is incompatible with swarm
# mode; dockerd would refuse to start). A query; tests stub it.
pkg_swarm_active() {
  [[ "$(docker info --format '{{.Swarm.LocalNodeState}}' 2>/dev/null)" == active ]]
}

# pkg_daemon_json_valid FILE: 0 if dockerd accepts FILE (a query; tests stub it).
pkg_daemon_json_valid() {
  dockerd --validate --config-file "$1" >/dev/null 2>&1
}

# pkg_restic_path prints the path of restic from PATH ("" if there is none).
pkg_restic_path() {
  command -v restic 2>/dev/null || true
}

# pkg_restic_version BIN prints the version of that restic ("" if it does not run).
pkg_restic_version() {
  "$1" version 2>/dev/null | awk '$1 == "restic" { print $2; exit }'
}

# pkg_gpg_home: an empty gpg home in WORK, so root's own keyring stays untouched.
pkg_gpg_home() {
  if [[ ! -d $WORK/gnupg ]]; then
    mkdir -m 0700 -- "$WORK/gnupg"
  fi
}

# pkg_key_fprs FILE prints the fingerprints of the primary keys in the OpenPGP key FILE (armored
# or binary), one per line. A query; tests stub it.
pkg_key_fprs() {
  pkg_gpg_home
  gpg --batch --no-options --homedir "$WORK/gnupg" --with-colons --show-keys -- "$1" 2>/dev/null |
    awk -F: '$1 == "pub" { want = 1; next } $1 == "fpr" && want { print $10; want = 0 }'
}

# pkg_dearmor IN OUT: the binary keyring apt's signed-by expects (a query; tests stub it).
pkg_dearmor() {
  pkg_gpg_home
  gpg --batch --no-options --homedir "$WORK/gnupg" --yes --dearmor -o "$2" -- "$1" 2>/dev/null
}

# pkg_apt_policy NAME: apt-cache policy NAME (a query; tests stub it).
pkg_apt_policy() {
  apt-cache policy -- "$1" 2>/dev/null
}

# pkg_caddy_version prints the version of the installed caddy ("2.11.7"; "" without one).
pkg_caddy_version() {
  caddy version 2>/dev/null </dev/null | awk 'NR == 1 { sub(/^v/, "", $1); print $1 }'
}

# pkg_installed_sources: reads apt-cache policy on stdin and prints the sources of the installed
# version (the lines below "***"), one per line.
pkg_installed_sources() {
  # a version line starts with " *** " (installed) or five blanks, a source line with eight
  # (written out: mawk has no interval expressions)
  awk '/^ [*][*][*] / { inst = 1; next }
    /^     [^ ]/ { inst = 0 }
    inst && /^        [^ ]/ { sub(/^ +[0-9]+ +/, ""); print }'
}

# pkg_caddy_from_repo: 0 if the installed caddy comes from the Caddy repository.
pkg_caddy_from_repo() {
  pkg_apt_policy caddy | pkg_installed_sources | grep -qF -- "$PKG_CADDY_REPO "
}

pkg_caddy_sources() {
  printf '%s\n' \
    "# Managed by mc-panel install.sh: Caddy from its official repository (security updates)." \
    "deb [signed-by=$PKG_CADDY_KEYRING] $PKG_CADDY_REPO any-version main"
}

# pkg_caddy_repo_ok: 0 when the source list is ours and the keyring holds exactly the pinned key.
pkg_caddy_repo_ok() {
  [[ -f $ROOT$PKG_CADDY_SOURCES && ! -L $ROOT$PKG_CADDY_SOURCES && -f $ROOT$PKG_CADDY_KEYRING && ! -L $ROOT$PKG_CADDY_KEYRING ]] || return 1
  [[ "$(cat -- "$ROOT$PKG_CADDY_SOURCES")" == "$(pkg_caddy_sources)" ]] || return 1
  [[ "$(pkg_key_fprs "$ROOT$PKG_CADDY_KEYRING")" == "$PKG_CADDY_KEY_FPR" ]]
}

# pkg_caddy_repo: adds the Caddy repository unless it is in place: gpg from the archive if
# missing, the key downloaded, converted and trusted only if it is exactly the pinned key, then
# the source list (the file names of Caddy's guide, so a list of the operator is replaced, not
# duplicated). Prints changed/unchanged; dies on a wrong key.
pkg_caddy_repo() {
  if pkg_caddy_repo_ok; then
    echo unchanged
    return 0
  fi
  if [[ -n "$(pkg_missing gpg)" ]]; then
    sys_apt -o "DPkg::Lock::Timeout=$PKG_APT_LOCK_WAIT" update </dev/null >&2 || die "apt-get update failed"
    sys_apt -o "DPkg::Lock::Timeout=$PKG_APT_LOCK_WAIT" install --no-install-recommends gpg </dev/null >&2 ||
      die "apt-get install gpg failed"
  fi
  local fprs
  fetch "$PKG_CADDY_KEY_URL" "$WORK/caddy.key"
  pkg_dearmor "$WORK/caddy.key" "$WORK/caddy.gpg" || die "cannot read the Caddy signing key"
  fprs="$(pkg_key_fprs "$WORK/caddy.gpg")"
  [[ $fprs == "$PKG_CADDY_KEY_FPR" ]] ||
    die "the Caddy signing key from $PKG_CADDY_KEY_URL is not the expected key $PKG_CADDY_KEY_FPR (got: ${fprs//$'\n'/ }); nothing was trusted"
  if [[ ! -d $ROOT${PKG_CADDY_KEYRING%/*} ]]; then
    ensure_dir "${PKG_CADDY_KEYRING%/*}" 0755 root root >/dev/null
  fi
  if [[ ! -d $ROOT${PKG_CADDY_SOURCES%/*} ]]; then
    ensure_dir "${PKG_CADDY_SOURCES%/*}" 0755 root root >/dev/null
  fi
  write_file "$PKG_CADDY_KEYRING" 0644 root root <"$WORK/caddy.gpg" >/dev/null
  pkg_caddy_sources | write_file "$PKG_CADDY_SOURCES" 0644 root root >/dev/null
  echo changed
}

# pkg_installed_version NAME prints the installed version of package NAME ("" if none).
pkg_installed_version() {
  dpkg-query -W -f='${Version}' -- "$1" 2>/dev/null || true
}

# pkg_caddy_works: 0 if the installed caddy accepts the current Caddyfile (validated as caddy,
# like cad_ensure) and caddy.service runs and stays up after a restart (a query; tests stub it).
pkg_caddy_works() {
  run_as caddy env HOME=/var/lib/caddy caddy validate --config "$CADDY_DIR/Caddyfile" --adapter caddyfile \
    </dev/null >/dev/null 2>&1 || return 1
  sys_systemctl restart caddy.service </dev/null || return 1
  unit_settled caddy.service
}

# pkg_caddy_upgrade: installs caddy from the Caddy repository when the installed one comes from
# elsewhere (Ubuntu's universe). The Caddyfile is a conffile: --force-confold keeps it. A newer
# Caddy may reject a Caddyfile the old one took: then the previous version goes back and is held
# (unattended-upgrades would break Caddy again), with a warning; the next install or update
# tries again (--allow-change-held-packages) and releases the hold once Caddy works. Dies only
# when even the way back fails.
pkg_caddy_upgrade() {
  if pkg_caddy_from_repo; then
    return 0
  fi
  local prev apt_opts=(-o "DPkg::Lock::Timeout=$PKG_APT_LOCK_WAIT" -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold)
  prev="$(pkg_installed_version caddy)"
  sys_apt -o "DPkg::Lock::Timeout=$PKG_APT_LOCK_WAIT" update </dev/null || die "apt-get update failed"
  sys_apt "${apt_opts[@]}" install --no-install-recommends --allow-change-held-packages caddy </dev/null ||
    die "apt-get install caddy (Caddy repository) failed"
  if pkg_caddy_works; then
    sys_apt_mark unhold caddy </dev/null >/dev/null || true
    log_changed "caddy $(pkg_caddy_version) from the Caddy repository"
    return 0
  fi
  local new
  new="$(pkg_caddy_version)"
  [[ -n $prev ]] || die "caddy $new from the Caddy repository does not run with $CADDY_DIR/Caddyfile (journalctl -u caddy)"
  warn "caddy $new rejects $CADDY_DIR/Caddyfile or does not stay up; going back to caddy $prev"
  if ! sys_apt "${apt_opts[@]}" install --no-install-recommends --allow-downgrades --allow-change-held-packages "caddy=$prev" </dev/null ||
    ! sys_apt_mark hold caddy </dev/null >/dev/null || ! sys_systemctl restart caddy.service </dev/null; then
    die "going back to caddy $prev failed; Caddy may be down (journalctl -u caddy)"
  fi
  warn "caddy $prev is back and held (no automatic updates, doctor: caddy_package). Fix the Caddyfile for Caddy $new (caddy validate --config $CADDY_DIR/Caddyfile), then run install.sh update --force"
}

# pkg_caddy_ensure (update): the Caddy repository and caddy from it; existing installs switch
# here. Dies on failure (update runs it in a subshell and only warns).
pkg_caddy_ensure() {
  log_step "caddy package"
  local r
  r="$(pkg_caddy_repo)"
  if [[ $r == changed ]]; then
    log_changed "Caddy repository ($PKG_CADDY_REPO, key $PKG_CADDY_KEY_FPR)"
  fi
  pkg_caddy_upgrade
  if [[ $r == unchanged ]] && pkg_caddy_from_repo; then
    log_ok "caddy $(pkg_caddy_version) from the Caddy repository"
  fi
}

pkg_daemon_json() {
  cat <<'EOF'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "live-restore": true
}
EOF
}

# pkg_wait_docker: 0 once the Docker daemon answers, 1 after PKG_DOCKER_WAIT seconds.
pkg_wait_docker() {
  local i
  for ((i = 0; ; i++)); do
    if pkg_docker_answers; then
      return 0
    fi
    if ((i >= PKG_DOCKER_WAIT)); then
      return 1
    fi
    sleep 1
  done
}

# pkg_daemon_json_ensure writes the missing /etc/docker/daemon.json (log rotation, live-restore)
# unless Docker runs in swarm mode, keeps it only if dockerd accepts it, and restarts Docker for
# it only without running containers. If Docker does not come back with it, the file is removed
# again and Docker restarted with its defaults.
pkg_daemon_json_ensure() {
  local f="$ROOT/etc/docker/daemon.json"
  if pkg_swarm_active; then
    warn "docker: swarm mode is active, live-restore is incompatible with it; /etc/docker/daemon.json not written"
    return 0
  fi
  if [[ ! -d $ROOT/etc/docker ]]; then
    ensure_dir /etc/docker 0755 root root >/dev/null
  fi
  pkg_daemon_json | write_file /etc/docker/daemon.json 0644 root root >/dev/null
  if ! pkg_daemon_json_valid "$f"; then
    rm -f -- "$f"
    warn "docker: dockerd --validate rejected the new daemon.json; removed it, Docker keeps its defaults"
    return 0
  fi
  if [[ -n "$(pkg_running_containers)" ]]; then
    # A restart now would stop the running containers (live-restore is not active yet), but
    # live-restore is one of the options dockerd reloads (SIGHUP); log options are not.
    if sys_systemctl reload docker.service </dev/null; then
      log_changed "docker: live-restore (/etc/docker/daemon.json, reloaded)"
      warn "docker: log rotation takes effect on the next Docker restart (containers are running; live-restore keeps them running then)"
    else
      warn "docker: log rotation and live-restore take effect on the next Docker restart (containers are running)"
    fi
    return 0
  fi
  if sys_systemctl restart docker.service </dev/null; then
    log_changed "docker: log rotation and live-restore (/etc/docker/daemon.json)"
    return 0
  fi
  rm -f -- "$f"
  sys_systemctl restart docker.service </dev/null || true
  warn "docker: did not start with the new daemon.json; removed it again and restarted Docker with its defaults"
}

# shellcheck disable=SC2034 # PKG_RESTIC_BIN is read by cfg_keys (config.sh)
pkg_install() {
  log_step "packages"
  local missing=() want=("${PKG_LIST[@]}") repo
  repo="$(pkg_caddy_repo)"
  if [[ $repo == changed ]]; then
    log_changed "Caddy repository ($PKG_CADDY_REPO, key $PKG_CADDY_KEY_FPR)"
  fi
  # Docker from the archive only if no Docker is there yet (a stopped Docker CE stays).
  if ! pkg_docker_answers && [[ -n "$(pkg_missing docker-ce)" ]]; then
    want+=("${PKG_DOCKER[@]}")
  fi
  mapfile -t missing < <(pkg_missing "${want[@]}")
  if ((${#missing[@]} > 0)); then
    # stdin from /dev/null and the conffile defaults: apt never asks (curl | bash, CI).
    sys_apt -o "DPkg::Lock::Timeout=$PKG_APT_LOCK_WAIT" update </dev/null || die "apt-get update failed"
    sys_apt -o "DPkg::Lock::Timeout=$PKG_APT_LOCK_WAIT" -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold \
      install --no-install-recommends "${missing[@]}" </dev/null ||
      die "apt-get install ${missing[*]} failed"
    log_changed "installed ${missing[*]}"
  else
    log_ok "all packages present"
  fi
  pkg_caddy_upgrade
  local bin v
  bin="$(pkg_restic_path)"
  [[ -n $bin ]] || die "restic is not installed"
  v="$(pkg_restic_version "$bin")"
  [[ -n $v ]] || die "restic ($bin) does not run"
  version_ge "$v" "$PKG_RESTIC_MIN" ||
    die "restic $v at $bin is too old (need >= $PKG_RESTIC_MIN; install a newer restic first)"
  PKG_RESTIC_BIN="$bin"
  if [[ ! -e $ROOT/etc/docker/daemon.json && ! -L $ROOT/etc/docker/daemon.json ]]; then
    pkg_daemon_json_ensure
  fi
  sys_systemctl enable --now docker.service </dev/null || die "docker.service does not start"
  pkg_wait_docker || die "the Docker daemon does not answer (systemctl status docker)"
  log_ok "restic $v ($bin), Docker"
}
