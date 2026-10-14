# shellcheck shell=bash
# Tests of packages.sh (installer spec §4.2). Run: bash scripts/install/test.sh
# shellcheck disable=SC2034,SC2317,SC2329

pkg_fake_dpkg() { # NAME... count as installed
  PKG_FAKE_INSTALLED=" $* "
  dpkg-query() {
    local pkg="${!#}"
    if [[ $PKG_FAKE_INSTALLED == *" $pkg "* ]]; then
      echo "install ok installed"
    else
      echo "unknown ok not-installed"
      return 1
    fi
  }
}

# pkg_env: Docker answers (no swarm, accepts daemon.json), restic 0.18.1 at /usr/bin/restic, no
# running containers.
pkg_env() {
  stub pkg_docker_answers 'return 0'
  stub pkg_restic_path 'echo /usr/bin/restic'
  stub pkg_restic_version 'echo 0.18.1'
  stub pkg_running_containers ':'
  stub pkg_swarm_active 'return 1'
  stub pkg_daemon_json_valid 'return 0'
  PKG_DOCKER_WAIT=0
}

pkg_daemon_json_present() {
  mkdir -p "$ROOT/etc/docker"
  echo '{}' >"$ROOT/etc/docker/daemon.json"
}

test_install_pkg_missing_lists_only_missing() {
  pkg_fake_dpkg caddy nftables restic tzdata sqlite3 unattended-upgrades curl ca-certificates
  assert_eq bzip2 "$(pkg_missing "${PKG_LIST[@]}" | xargs)"
}

test_install_pkg_install_installs_missing_once() {
  pkg_fake_dpkg curl ca-certificates
  pkg_env
  stub pkg_docker_answers 'return 1' # no Docker yet: docker.io is installed too
  pkg_daemon_json_present
  assert_fails pkg_install # the final Docker check fails in the stubbed world
  assert_called 'apt-get -y -q -o DPkg::Lock::Timeout=300 update'
  assert_called 'env DEBIAN_FRONTEND=noninteractive apt-get -y -q -o DPkg::Lock::Timeout=300 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install --no-install-recommends caddy nftables restic tzdata sqlite3 unattended-upgrades bzip2 docker.io'
  assert_eq 1 "$(grep -c ' install ' "$WORK/calls.log")"
}

test_install_pkg_install_keeps_preinstalled_docker() {
  pkg_fake_dpkg caddy curl ca-certificates
  pkg_env
  pkg_daemon_json_present
  pkg_install >/dev/null
  assert_not_called 'docker.io'
}

test_install_pkg_install_keeps_stopped_docker_ce() {
  pkg_fake_dpkg "${PKG_LIST[@]}" docker-ce
  pkg_env
  stub pkg_docker_answers 'return 1'
  pkg_daemon_json_present
  assert_fails pkg_install
  assert_not_called 'apt-get'
  assert_called 'systemctl enable --now docker.service'
}

test_install_pkg_install_second_run_installs_nothing() {
  pkg_fake_dpkg "${PKG_LIST[@]}" docker.io
  pkg_env
  pkg_daemon_json_present
  pkg_install >/dev/null
  assert_not_called 'apt-get'
  assert_not_called 'restart docker'
}

test_install_pkg_install_refuses_old_restic() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  stub pkg_restic_version 'echo 0.16.4'
  assert_fails pkg_install
  assert_contains "$(cat "$WORK/out")" "too old"
}

test_install_pkg_install_refuses_missing_restic() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  stub pkg_restic_path ':'
  assert_fails pkg_install
  assert_contains "$(cat "$WORK/out")" "restic is not installed"
}

test_install_pkg_install_takes_restic_from_path() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  stub pkg_restic_path 'echo /usr/local/bin/restic'
  stub pkg_restic_version 'echo 0.19.1'
  pkg_daemon_json_present
  pkg_install >/dev/null
  assert_eq /usr/local/bin/restic "$PKG_RESTIC_BIN"
}

test_install_pkg_restic_version_parses_output() {
  restic() { echo "restic 0.18.1 compiled with go1.24.4 on linux/amd64"; }
  assert_eq 0.18.1 "$(pkg_restic_version restic)"
}

test_install_pkg_daemon_json_only_when_missing() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  pkg_install >/dev/null
  assert_file_contains "$ROOT/etc/docker/daemon.json" '"live-restore": true'
  assert_called 'systemctl restart docker.service'
  printf '{"data-root": "/mnt/docker"}\n' >"$ROOT/etc/docker/daemon.json"
  : >"$WORK/calls.log"
  pkg_install >/dev/null
  assert_file_eq "$ROOT/etc/docker/daemon.json" '{"data-root": "/mnt/docker"}'
  assert_not_called 'systemctl restart docker'
}

test_install_pkg_daemon_json_no_restart_with_running_servers() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  stub pkg_running_containers 'echo 3f2a1b' # any container, not only mc-panel's
  local out
  out="$(pkg_install)"
  assert_file_contains "$ROOT/etc/docker/daemon.json" '"live-restore": true'
  assert_not_called 'systemctl restart docker'
  assert_contains "$out" "[warn]"
}

test_install_pkg_install_fails_without_docker() {
  pkg_fake_dpkg "${PKG_LIST[@]}" docker.io
  pkg_env
  stub pkg_docker_answers 'return 1'
  pkg_daemon_json_present
  assert_fails pkg_install
  assert_contains "$(cat "$WORK/out")" "does not answer"
}

test_install_pkg_daemon_json_removed_when_dockerd_rejects_it() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  stub pkg_daemon_json_valid 'return 1'
  local out
  out="$(pkg_install)"
  assert_contains "$out" "[warn] docker: dockerd --validate rejected"
  assert_no_file "$ROOT/etc/docker/daemon.json"
  assert_not_called 'systemctl restart docker'
}

test_install_pkg_daemon_json_not_written_in_swarm_mode() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  stub pkg_swarm_active 'return 0'
  assert_contains "$(pkg_install)" "[warn] docker: swarm mode"
  assert_no_file "$ROOT/etc/docker/daemon.json"
  assert_not_called 'systemctl restart docker'
}

test_install_pkg_daemon_json_removed_when_docker_does_not_restart() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  echo 0 >"$WORK/restarts"
  sys_systemctl() {
    printf 'systemctl %s\n' "$*" >>"$WORK/calls.log"
    if [[ $* == "restart docker.service" ]]; then
      local n
      n=$(($(cat "$WORK/restarts") + 1))
      echo "$n" >"$WORK/restarts"
      ((n > 1)) # the first restart (with the new daemon.json) fails
    fi
  }
  local out
  out="$(pkg_install)"
  assert_contains "$out" "[warn] docker: did not start with the new daemon.json"
  assert_no_file "$ROOT/etc/docker/daemon.json"
  assert_eq 2 "$(cat "$WORK/restarts")"
}
