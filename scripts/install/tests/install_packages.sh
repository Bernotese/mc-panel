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
  stub pkg_caddy_repo_ok 'return 0'
  stub pkg_caddy_from_repo 'return 0'
  stub pkg_caddy_version 'echo 2.11.7'
  PKG_DOCKER_WAIT=0
}

# pkg_caddy_env: no Caddy repository yet; the downloaded key is FPR (default: the pinned one).
pkg_caddy_env() {
  PKG_TEST_FPR="${1:-$PKG_CADDY_KEY_FPR}"
  # shellcheck disable=SC2016 # the stubs expand their arguments when they run
  stub fetch 'printf "armored key\n" >"$2"'
  # shellcheck disable=SC2016
  stub pkg_dearmor 'printf "binary key\n" >"$2"'
  # shellcheck disable=SC2016
  stub pkg_key_fprs 'printf "%s\n" "$PKG_TEST_FPR"'
}

pkg_policy_universe() {
  printf '%s\n' 'caddy:' '  Installed: 2.6.2-14' '  Candidate: 2.11.7' '  Version table:' \
    '     2.11.7 500' '        500 https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version/main amd64 Packages' \
    ' *** 2.6.2-14 500' '        500 http://archive.ubuntu.com/ubuntu resolute/universe amd64 Packages' \
    '        100 /var/lib/dpkg/status' \
    '     2.6.2 500' '        500 https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version/main amd64 Packages'
}

pkg_policy_repo() {
  printf '%s\n' 'caddy:' '  Installed: 2.11.7' '  Candidate: 2.11.7' '  Version table:' \
    ' *** 2.11.7 500' '        500 https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version/main amd64 Packages' \
    '        100 /var/lib/dpkg/status' \
    '     2.6.2-14 500' '        500 http://archive.ubuntu.com/ubuntu resolute/universe amd64 Packages'
}

pkg_daemon_json_present() {
  mkdir -p "$ROOT/etc/docker"
  echo '{}' >"$ROOT/etc/docker/daemon.json"
}

test_install_pkg_missing_lists_only_missing() {
  pkg_fake_dpkg caddy nftables restic tzdata sqlite3 unattended-upgrades curl ca-certificates
  assert_eq "bzip2 openssh-client" "$(pkg_missing "${PKG_LIST[@]}" | xargs)"
}

test_install_pkg_install_installs_missing_once() {
  pkg_fake_dpkg curl ca-certificates
  pkg_env
  stub pkg_docker_answers 'return 1' # no Docker yet: docker.io is installed too
  pkg_daemon_json_present
  assert_fails pkg_install # the final Docker check fails in the stubbed world
  assert_called 'apt-get -y -q -o DPkg::Lock::Timeout=300 update'
  assert_called 'env DEBIAN_FRONTEND=noninteractive apt-get -y -q -o DPkg::Lock::Timeout=300 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install --no-install-recommends caddy nftables restic tzdata sqlite3 unattended-upgrades bzip2 openssh-client docker.io'
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
  # live-restore is reloadable; log rotation waits for the next restart
  assert_called 'systemctl reload docker.service'
  assert_contains "$out" "[changed] docker: live-restore"
  assert_contains "$out" "[warn] docker: log rotation takes effect on the next Docker restart"
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

test_install_pkg_caddy_repo_adds_the_pinned_key_and_source() {
  pkg_fake_dpkg curl
  pkg_caddy_env
  assert_eq changed "$(pkg_caddy_repo)"
  # gpg comes from the archive first (it checks the key)
  assert_called 'apt-get -y -q -o DPkg::Lock::Timeout=300 install --no-install-recommends gpg'
  assert_file_eq "$ROOT/usr/share/keyrings/caddy-stable-archive-keyring.gpg" 'binary key'
  assert_eq 644 "$(stat -c '%a' "$ROOT/usr/share/keyrings/caddy-stable-archive-keyring.gpg")"
  assert_file_contains "$ROOT/etc/apt/sources.list.d/caddy-stable.list" \
    'deb [signed-by=/usr/share/keyrings/caddy-stable-archive-keyring.gpg] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main'
  # the second run finds both and downloads nothing
  stub fetch 'fail "fetch on the second run"'
  assert_eq unchanged "$(pkg_caddy_repo)"
}

test_install_pkg_caddy_repo_has_gpg_already() {
  pkg_fake_dpkg gpg
  pkg_caddy_env
  pkg_caddy_repo >/dev/null
  assert_not_called 'apt-get'
}

test_install_pkg_caddy_repo_refuses_a_foreign_key() {
  pkg_fake_dpkg gpg
  pkg_caddy_env 0000000000000000000000000000000000000000
  assert_fails pkg_caddy_repo
  assert_contains "$(cat "$WORK/out")" "is not the expected key"
  assert_no_file "$ROOT/usr/share/keyrings/caddy-stable-archive-keyring.gpg"
  assert_no_file "$ROOT/etc/apt/sources.list.d/caddy-stable.list"
}

test_install_pkg_caddy_repo_refuses_extra_keys() {
  pkg_fake_dpkg gpg
  pkg_caddy_env "$PKG_CADDY_KEY_FPR"$'\n'"1111111111111111111111111111111111111111"
  assert_fails pkg_caddy_repo
  assert_no_file "$ROOT/usr/share/keyrings/caddy-stable-archive-keyring.gpg"
}

test_install_pkg_caddy_repo_replaces_a_changed_source_list() {
  pkg_fake_dpkg gpg
  pkg_caddy_env
  pkg_caddy_repo >/dev/null
  printf 'deb [trusted=yes] http://mirror.invalid/caddy any-version main\n' >"$ROOT/etc/apt/sources.list.d/caddy-stable.list"
  assert_eq changed "$(pkg_caddy_repo)"
  assert_file_eq "$ROOT/etc/apt/sources.list.d/caddy-stable.list" "$(pkg_caddy_sources)"
}

test_install_pkg_installed_sources_parses_apt_policy() {
  assert_eq 'http://archive.ubuntu.com/ubuntu resolute/universe amd64 Packages|/var/lib/dpkg/status' \
    "$(pkg_policy_universe | pkg_installed_sources | paste -sd'|' -)"
  stub pkg_apt_policy pkg_policy_universe
  assert_fails pkg_caddy_from_repo
  stub pkg_apt_policy pkg_policy_repo
  pkg_caddy_from_repo || fail "caddy from the repository not detected"
}

test_install_pkg_install_switches_caddy_to_the_repository() {
  # an existing install with Ubuntu's caddy: the repository is added and caddy upgraded from it
  pkg_fake_dpkg "${PKG_LIST[@]}" gpg docker.io
  pkg_env
  pkg_daemon_json_present
  pkg_caddy_env
  # shellcheck disable=SC2016 # expanded when the stub runs
  stub pkg_caddy_repo_ok '[[ -f $ROOT$PKG_CADDY_SOURCES ]]'
  stub pkg_caddy_from_repo 'return 1'
  stub pkg_installed_version 'echo 2.6.2-14'
  stub pkg_caddy_works 'return 0'
  local out
  out="$(pkg_install)"
  assert_contains "$out" "[changed] Caddy repository"
  assert_contains "$out" "[changed] caddy 2.11.7 from the Caddy repository"
  assert_called 'apt-get -y -q -o DPkg::Lock::Timeout=300 update'
  assert_called 'env DEBIAN_FRONTEND=noninteractive apt-get -y -q -o DPkg::Lock::Timeout=300 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install --no-install-recommends --allow-change-held-packages caddy'
  assert_called 'apt-mark unhold caddy'
  assert_not_called 'allow-downgrades'
  assert_file "$ROOT/etc/apt/sources.list.d/caddy-stable.list"
}

test_install_pkg_caddy_upgrade_goes_back_when_the_new_caddy_fails() {
  stub pkg_caddy_from_repo 'return 1'
  stub pkg_installed_version 'echo 2.6.2-14'
  stub pkg_caddy_version 'echo 2.11.7'
  stub pkg_caddy_works 'return 1'
  local out
  out="$(pkg_caddy_upgrade)"
  assert_contains "$out" "[warn] caddy 2.11.7 rejects /etc/caddy/Caddyfile or does not stay up; going back to caddy 2.6.2-14"
  assert_contains "$out" "caddy 2.6.2-14 is back and held"
  assert_not_contains "$out" "[changed]"
  assert_called 'install --no-install-recommends --allow-downgrades --allow-change-held-packages caddy=2.6.2-14'
  assert_called 'apt-mark hold caddy'
  assert_called 'systemctl restart caddy.service'
  assert_not_called 'apt-mark unhold'
}

test_install_pkg_caddy_upgrade_dies_when_going_back_fails() {
  stub pkg_caddy_from_repo 'return 1'
  stub pkg_installed_version 'echo 2.6.2-14'
  stub pkg_caddy_version 'echo 2.11.7'
  stub pkg_caddy_works 'return 1'
  sys_apt() {
    printf 'apt-get %s\n' "$*" >>"$WORK/calls.log"
    [[ $* != *allow-downgrades* ]]
  }
  assert_fails pkg_caddy_upgrade
  assert_contains "$(cat "$WORK/out")" "going back to caddy 2.6.2-14 failed"
}

test_install_pkg_caddy_works_validates_restarts_and_waits() {
  # shellcheck disable=SC2016 # expanded when the stub runs
  stub unit_settled 'echo "settled $1" >>"$WORK/calls.log"'
  pkg_caddy_works
  assert_called 'runuser -u caddy -- env HOME=/var/lib/caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile'
  assert_called 'systemctl restart caddy.service'
  assert_called 'settled caddy.service'
  run_as() { return 1; } # validate fails: no restart
  : >"$WORK/calls.log"
  assert_fails pkg_caddy_works
  assert_not_called 'restart'
}

test_install_pkg_install_fresh_host_installs_caddy_from_the_repository_once() {
  pkg_fake_dpkg gpg curl ca-certificates
  pkg_env
  pkg_daemon_json_present
  pkg_caddy_env
  # shellcheck disable=SC2016 # expanded when the stub runs
  stub pkg_caddy_repo_ok '[[ -f $ROOT$PKG_CADDY_SOURCES ]]'
  pkg_install >/dev/null
  # the repository is in place before the one install, which brings caddy along
  assert_file "$ROOT/etc/apt/sources.list.d/caddy-stable.list"
  assert_eq 1 "$(grep -c ' install ' "$WORK/calls.log")"
  assert_called 'install --no-install-recommends caddy nftables'
}
