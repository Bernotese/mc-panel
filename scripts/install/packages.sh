# shellcheck shell=bash
# packages.sh - installer spec §4.2: packages from the Ubuntu archive (only missing ones),
# restic >= 0.17, a Docker daemon that answers, and Docker's daemon.json only when it is missing.

PKG_LIST=(caddy nftables restic tzdata sqlite3 unattended-upgrades curl ca-certificates bzip2)
PKG_DOCKER=(docker.io)
PKG_RESTIC_MIN=0.17.0
PKG_RESTIC_BIN=""    # set by pkg_install: path of a restic >= PKG_RESTIC_MIN (cfg_keys reads it)
PKG_DOCKER_WAIT=30   # seconds to wait for the Docker daemon to answer
PKG_APT_LOCK_WAIT=300 # seconds apt waits for the dpkg lock (unattended-upgrades on a fresh host)

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

# pkg_mc_containers prints the IDs of running mc-panel containers (a query; tests stub it).
pkg_mc_containers() {
  docker ps -q --filter label=mcpanel.instance 2>/dev/null || true
}

# pkg_restic_path prints the path of restic from PATH ("" if there is none).
pkg_restic_path() {
  command -v restic 2>/dev/null || true
}

# pkg_restic_version BIN prints the version of that restic ("" if it does not run).
pkg_restic_version() {
  "$1" version 2>/dev/null | awk '$1 == "restic" { print $2; exit }'
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

# shellcheck disable=SC2034 # PKG_RESTIC_BIN is read by cfg_keys (config.sh)
pkg_install() {
  log_step "packages"
  local missing=() want=("${PKG_LIST[@]}")
  # Docker from the archive only if no Docker is there yet (a stopped Docker CE stays).
  if ! pkg_docker_answers && [[ -n "$(pkg_missing docker-ce)" ]]; then
    want+=("${PKG_DOCKER[@]}")
  fi
  mapfile -t missing < <(pkg_missing "${want[@]}")
  if ((${#missing[@]} > 0)); then
    sys_apt -o "DPkg::Lock::Timeout=$PKG_APT_LOCK_WAIT" update || die "apt-get update failed"
    sys_apt -o "DPkg::Lock::Timeout=$PKG_APT_LOCK_WAIT" install --no-install-recommends "${missing[@]}" ||
      die "apt-get install ${missing[*]} failed"
    log_changed "installed ${missing[*]}"
  else
    log_ok "all packages present"
  fi
  local bin v
  bin="$(pkg_restic_path)"
  [[ -n $bin ]] || die "restic is not installed"
  v="$(pkg_restic_version "$bin")"
  [[ -n $v ]] || die "restic ($bin) does not run"
  version_ge "$v" "$PKG_RESTIC_MIN" ||
    die "restic $v at $bin is too old (need >= $PKG_RESTIC_MIN; install a newer restic first)"
  PKG_RESTIC_BIN="$bin"
  if [[ ! -e $ROOT/etc/docker/daemon.json && ! -L $ROOT/etc/docker/daemon.json ]]; then
    if [[ ! -d $ROOT/etc/docker ]]; then
      ensure_dir /etc/docker 0755 root root >/dev/null
    fi
    pkg_daemon_json | write_file /etc/docker/daemon.json 0644 root root >/dev/null
    if [[ -n "$(pkg_mc_containers)" ]]; then
      # A restart now would stop the running Minecraft servers (live-restore is not active yet).
      warn "docker: log rotation and live-restore take effect on the next Docker restart (mc-panel containers are running)"
    else
      sys_systemctl restart docker.service || die "docker did not restart with the new daemon.json"
      log_changed "docker: log rotation and live-restore (/etc/docker/daemon.json)"
    fi
  fi
  sys_systemctl enable --now docker.service || die "docker.service does not start"
  pkg_wait_docker || die "the Docker daemon does not answer (systemctl status docker)"
  log_ok "restic $v ($bin), Docker"
}
