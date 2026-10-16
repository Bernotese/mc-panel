#!/usr/bin/env bash
# Runs test suites on the test VM (started by scripts/test-vm.sh, i.e. make test-vm). Runs as the
# ordinary VM user in its own copy of the tree: dev mode, own sockets in .dev/, own ports (panel
# 18080, Playwright mock 5174), so the panel deployed on the VM (127.0.0.1:8080) stays untouched.
# Tools are set up on first use; Go and Node come from scripts/testvm-deploy.sh. Runs from all
# copies on the VM share one lock around the timing-sensitive parts (race tests, Vitest, e2e,
# shots): parallel runs of several teammates overload the VM and make those tests fail. Lint,
# npm ci and builds run outside the lock.
#
#   bash scripts/vm-test.sh [all|go|lint|web|e2e|shots]
set -euo pipefail

export PATH="/usr/local/go/bin:/usr/local/lib/nodejs/bin:${HOME}/.local/bin:${PATH}"
# The VM user's default umask is 0002; tests that check directory modes expect the CI's 0022.
umask 022
cd "$(dirname "$0")/.."

suite="${1:-all}"
GOLANGCI_VERSION="2.14.0"
E2E_PORT=18080
VM_LOCK="${HOME}/.cache/mcpanel-vm-test.lock"

log() { printf '\n==> %s\n' "$*"; }
die() {
  echo "vm-test: $*" >&2
  exit 1
}

need_go() {
  command -v go > /dev/null || die "go not found in /usr/local/go; run 'make deploy-testvm' once"
  local want
  want="$(awk '/^go /{print $2; exit}' go.mod)"
  case "$(go env GOVERSION)" in
    "go${want}"*) ;;
    *) die "go $(go env GOVERSION) does not match go.mod (${want}); run 'make deploy-testvm' to update it" ;;
  esac
}

need_node_modules() {
  command -v npm > /dev/null || die "npm not found in /usr/local/lib/nodejs; run 'make deploy-testvm' once"
  local hash marker=web/node_modules/.mcpanel-lock-hash
  hash="$(sha256sum web/package-lock.json | cut -d' ' -f1)"
  if [ "$(cat "${marker}" 2> /dev/null)" != "${hash}" ]; then
    log "npm ci (package-lock.json changed)"
    (cd web && npm ci --ignore-scripts --no-audit --no-fund)
    echo "${hash}" > "${marker}"
  fi
}

need_golangci() {
  local bin="${HOME}/.local/bin/golangci-lint"
  if [ -x "${bin}" ] && "${bin}" version 2> /dev/null | grep -q "version ${GOLANGCI_VERSION} "; then
    return
  fi
  log "installing golangci-lint ${GOLANGCI_VERSION} to ~/.local/bin"
  local arch tmp name base
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64) arch=arm64 ;;
    *) die "unsupported architecture $(uname -m)" ;;
  esac
  tmp="$(mktemp -d)"
  name="golangci-lint-${GOLANGCI_VERSION}-linux-${arch}"
  base="https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_VERSION}"
  curl -fsSL -o "${tmp}/${name}.tar.gz" "${base}/${name}.tar.gz"
  curl -fsSL -o "${tmp}/checksums.txt" "${base}/golangci-lint-${GOLANGCI_VERSION}-checksums.txt"
  (cd "${tmp}" && grep " ${name}.tar.gz\$" checksums.txt | sha256sum -c -)
  tar -xzf "${tmp}/${name}.tar.gz" -C "${tmp}"
  mkdir -p "${HOME}/.local/bin"
  install -m 0755 "${tmp}/${name}/golangci-lint" "${bin}"
  rm -rf "${tmp}"
}

# restic for the backup integration tests of the files service (sub-project 4); they skip
# without MCPANEL_TEST_RESTIC. The shared script pins the version and keeps an installed binary
# of that version (idempotent).
need_restic() {
  sudo bash scripts/install-restic.sh
  export MCPANEL_TEST_RESTIC=/usr/local/bin/restic
}

need_playwright() {
  local version marker
  version="$(node -p "require('./web/node_modules/@playwright/test/package.json').version")"
  marker="${HOME}/.cache/mcpanel-playwright-${version}"
  [ -f "${marker}" ] && return
  log "installing Playwright ${version} Chromium and its system packages"
  (cd web && npx playwright install chromium)
  (cd web && sudo env "PATH=${PATH}" npx playwright install-deps chromium)
  mkdir -p "${HOME}/.cache"
  touch "${marker}"
}

# with_lock runs "$@" while holding the VM test lock (shared with the CI jobs, which flock the
# same file around their race and Vitest steps). Nested calls reuse a lock that is already held.
lock_held=""
with_lock() {
  if [ -n "${lock_held}" ]; then
    "$@"
    return
  fi
  mkdir -p "$(dirname "${VM_LOCK}")"
  exec 9> "${VM_LOCK}"
  if ! flock -n 9; then
    log "waiting for another test run on this VM"
    flock 9
  fi
  lock_held=1
  # No "|| rc=$?" here: it would turn off set -e inside "$@". A failure exits the script (which
  # releases the lock) unless the caller checks with_lock's status itself.
  local rc
  "$@"
  rc=$?
  lock_held=""
  exec 9>&-
  return "${rc}"
}

suite_go() {
  need_go
  need_restic
  # Compile the race test binaries outside the lock (-exec true builds but does not run them).
  log "go test -race: build"
  CGO_ENABLED=1 go test -race -count=1 -exec true ./... > /dev/null
  log "go test -race ./... (MCPANEL_TEST_RESTIC=${MCPANEL_TEST_RESTIC})"
  with_lock env CGO_ENABLED=1 go test -race -count=1 ./...
  log "go test -race -tags acceptance ./internal/timescale (acceptance time scale)"
  env CGO_ENABLED=1 go test -race -count=1 -tags acceptance ./internal/timescale
}

need_shellcheck() {
  if ! command -v shellcheck > /dev/null 2>&1; then
    log "installing shellcheck"
    sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q shellcheck > /dev/null
  fi
}

suite_lint() {
  need_go
  need_golangci
  need_node_modules
  need_shellcheck
  log "golangci-lint run ./..."
  golangci-lint run ./...
  log "eslint + tsc"
  (cd web && npm run lint && npm run typecheck)
  # Like the CI job installer-lint (installer plan V5).
  log "installer: assemble, shellcheck, Bash tests"
  make installer INSTALLER_VERSION=0.0.0-vm
  shellcheck -x scripts/install/*.sh scripts/install/tests/*.sh build/install.sh scripts/sign-release.sh scripts/verify-release-build.sh scripts/release-lib.sh
  bash scripts/install/test.sh
}

suite_web() {
  need_node_modules
  log "vitest"
  with_lock bash -c 'cd web && npm test'
}

# Real backend (agent, files and panel in dev mode; the panel serves the built UI itself on
# E2E_PORT) and the mock backend (port 5174).
suite_e2e() {
  need_go
  need_node_modules
  need_playwright

  log "build UI and binaries"
  (cd web && npm run build)
  rm -rf .dev
  mkdir -p .dev/bin
  CGO_ENABLED=0 go build -o .dev/bin/ ./cmd/...
  cat > .dev/panel-vm.toml << EOF
listen = "127.0.0.1:${E2E_PORT}"
public_url = "http://localhost:${E2E_PORT}"
EOF

  # The stack listens on fixed ports (18080, Playwright mock 5174), so it runs under the lock: a
  # second run would otherwise collide on the ports or test the other checkout's panel.
  with_lock e2e_stack_and_playwright
}

# e2e_stack_and_playwright runs under the VM test lock. On failure the script exits: the EXIT
# trap stops the stack and the lock is released with the process (the servers do not inherit
# fd 9).
e2e_stack_and_playwright() {
  # stack_pids is global: the EXIT trap runs after this function has returned, when a local
  # array would already be gone (and a bare "wait" would then wait for the servers forever).
  stack_pids=()
  trap stop_stack EXIT
  .dev/bin/agent -dev > .dev/agent.log 2>&1 9>&- &
  stack_pids+=("$!")
  .dev/bin/files -dev > .dev/files.log 2>&1 9>&- &
  stack_pids+=("$!")
  sleep 1
  .dev/bin/panel serve -dev -config .dev/panel-vm.toml > .dev/panel.log 2>&1 9>&- &
  stack_pids+=("$!")
  local ok=""
  for _ in $(seq 1 30); do
    if curl -sf "http://127.0.0.1:${E2E_PORT}/api/health" > /dev/null; then
      ok=1
      break
    fi
    sleep 1
  done
  if [ -z "${ok}" ]; then
    tail -n 50 .dev/agent.log .dev/files.log .dev/panel.log
    die "panel did not come up on port ${E2E_PORT}"
  fi
  local password="vm e2e admin password 1"
  printf '%s\n' "${password}" | .dev/bin/panel admin create -dev -config .dev/panel-vm.toml -username e2e-admin

  local rc=0
  log "Playwright (real backend)"
  (cd web && E2E_BASE_URL="http://localhost:${E2E_PORT}" E2E_ADMIN_USER=e2e-admin E2E_ADMIN_PASSWORD="${password}" npm run e2e:real) || rc=1
  log "Playwright (mock backend)"
  (cd web && npm run e2e) || rc=1
  if [ "${rc}" -ne 0 ]; then
    tail -n 50 .dev/agent.log .dev/files.log .dev/panel.log
  fi
  stop_stack
  trap - EXIT
  [ "${rc}" -eq 0 ] || die "e2e failed"
}

# Screenshots of the UI against the mock backend for visual review (web/screenshots/, copied back
# by test-vm.sh). Not part of "all" and not run in CI.
suite_shots() {
  need_node_modules
  need_playwright
  rm -rf web/screenshots
  log "Playwright screenshots (mock backend)"
  with_lock bash -c 'cd web && npm run shots'
}

# stop_stack ends the dev servers of suite_e2e and waits only for them.
stack_pids=()
stop_stack() {
  [ "${#stack_pids[@]}" -gt 0 ] || return 0
  kill "${stack_pids[@]}" 2> /dev/null || true
  local p
  for p in "${stack_pids[@]}"; do
    for _ in $(seq 1 20); do
      kill -0 "${p}" 2> /dev/null || break
      sleep 0.5
    done
    kill -9 "${p}" 2> /dev/null || true
  done
  wait "${stack_pids[@]}" 2> /dev/null || true
  stack_pids=()
}

# For "all" the lock is held from the race tests to the end of e2e (one queue slot instead of three).
suite_all_locked() {
  suite_go
  suite_web
  suite_e2e
}

case "${suite}" in
  go) suite_go ;;
  lint) suite_lint ;;
  web) suite_web ;;
  e2e) suite_e2e ;;
  shots) suite_shots ;;
  all)
    suite_lint
    with_lock suite_all_locked
    ;;
  *) die "unknown suite ${suite} (all|go|lint|web|e2e|shots)" ;;
esac
log "vm-test ${suite}: OK"
