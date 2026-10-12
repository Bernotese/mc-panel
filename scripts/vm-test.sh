#!/usr/bin/env bash
# Runs test suites on the test VM (started by scripts/test-vm.sh, i.e. make test-vm). Runs as the
# ordinary VM user in its own copy of the tree: dev mode, own sockets in .dev/, own ports (panel
# 18080, Playwright mock 5174), so the panel deployed on the VM (127.0.0.1:8080) stays untouched.
# Tools are set up on first use; Go and Node come from scripts/testvm-deploy.sh. Runs from all
# copies on the VM are serialised (one lock): parallel race and Vitest runs of several teammates
# overload the VM and make timing-sensitive tests fail.
#
#   bash scripts/vm-test.sh [all|go|lint|web|e2e]
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
    (cd web && npm ci --no-audit --no-fund)
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
# without MCPANEL_TEST_RESTIC. The shared script keeps an installed binary of the pinned version.
need_restic() {
  if ! /usr/local/bin/restic version 2> /dev/null | grep -q "^restic 0\.19\.1 "; then
    log "installing restic (scripts/install-restic.sh)"
    sudo bash scripts/install-restic.sh
  fi
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

suite_go() {
  need_go
  need_restic
  log "go test -race ./... (MCPANEL_TEST_RESTIC=${MCPANEL_TEST_RESTIC})"
  CGO_ENABLED=1 go test -race -count=1 ./...
}

suite_lint() {
  need_go
  need_golangci
  need_node_modules
  log "golangci-lint run ./..."
  golangci-lint run ./...
  log "eslint + tsc"
  (cd web && npm run lint && npm run typecheck)
}

suite_web() {
  need_node_modules
  log "vitest"
  (cd web && npm test)
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

# One run at a time on the VM, across all copies (the lock is released when this script exits).
mkdir -p "$(dirname "${VM_LOCK}")"
exec 9> "${VM_LOCK}"
if ! flock -n 9; then
  log "waiting for another test run on this VM"
  flock 9
fi

case "${suite}" in
  go) suite_go ;;
  lint) suite_lint ;;
  web) suite_web ;;
  e2e) suite_e2e ;;
  all)
    suite_lint
    suite_go
    suite_web
    suite_e2e
    ;;
  *) die "unknown suite ${suite} (all|go|lint|web|e2e)" ;;
esac
log "vm-test ${suite}: OK"
