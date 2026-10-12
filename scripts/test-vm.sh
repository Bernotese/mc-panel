#!/usr/bin/env bash
# Copies the working tree (tracked and new, not ignored files; no push needed) to the test VM and
# runs test suites there: make test-vm [SUITE=all|go|lint|web|e2e] (docs/dev/README.md, "Tests auf
# der Test-VM"). Each checkout or worktree gets its own directory on the VM, so teammates can test
# in parallel. Nothing runs as root and nothing touches the panel deployed on the VM.
set -euo pipefail

vm="${TESTVM:-claude@192.168.1.117}"
suite="${1:-all}"
case "${suite}" in
  all | go | lint | web | e2e) ;;
  *)
    echo "usage: $0 [all|go|lint|web|e2e]" >&2
    exit 2
    ;;
esac

root="$(git rev-parse --show-toplevel)"
dest="mc-panel-test/$(basename "${root}")"

# The excludes mirror .gitignore (rsync cannot read it reliably: it has a "!" rule). Excluded paths
# on the VM are kept by --delete, so node_modules and build output survive between runs.
ssh "${vm}" "mkdir -p ~/${dest}"
rsync -az --delete \
  --exclude=/.git \
  --exclude=/bin/ --exclude=/dist/ --exclude=/.dev/ \
  --exclude='*.db' --exclude='*.db-wal' --exclude='*.db-shm' \
  --include=/internal/webui/dist/.gitkeep --exclude='/internal/webui/dist/*' \
  --exclude=/web/node_modules/ --exclude=/web/.tanstack/ \
  --exclude=/web/test-results/ --exclude=/web/playwright-report/ \
  --exclude=.DS_Store --exclude=/.claude/settings.local.json \
  "${root}/" "${vm}:${dest}/"

echo "==> ${vm}:~/${dest}: suite ${suite}"
ssh "${vm}" "cd ~/${dest} && bash scripts/vm-test.sh ${suite}"
