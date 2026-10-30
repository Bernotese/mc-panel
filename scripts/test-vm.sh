#!/usr/bin/env bash
# Copies the working tree (tracked and new, not ignored files; no push needed) to the test VM and
# runs test suites there: make test-vm [SUITE=all|go|lint|web|e2e|shots] (docs/dev/README.md, "Tests
# on the test VM"). Each checkout or worktree gets its own directory on the VM, so teammates can test
# in parallel. Nothing runs as root and nothing touches the panel deployed on the VM.
set -euo pipefail

vm="${TESTVM:-claude@192.168.1.117}"
suite="${1:-all}"
case "${suite}" in
  all | go | lint | web | e2e | shots) ;;
  *)
    echo "usage: $0 [all|go|lint|web|e2e|shots]" >&2
    exit 2
    ;;
esac

root="$(git rev-parse --show-toplevel)"
dest="mc-panel-test/$(basename "${root}")"

# The SSH connection to the VM sometimes stalls (#123, N6): keepalives end a dead connection after
# about a minute instead of hanging until the run is killed by hand.
ssh_opts=(-o ServerAliveInterval=15 -o ServerAliveCountMax=4 -o ConnectTimeout=10)

# The excludes mirror .gitignore (rsync cannot read it reliably: it has a "!" rule). Excluded paths
# on the VM are kept by --delete, so node_modules and build output survive between runs. They
# have no trailing slash on purpose: "/x/" matches only a directory, so a symlink web/node_modules
# (or bin, .dev, ...) in a worktree would be copied to the VM.
ssh "${ssh_opts[@]}" "${vm}" "mkdir -p ~/${dest}"
rsync -az --delete -e "ssh ${ssh_opts[*]}" \
  --exclude=/.git \
  --exclude=/bin --exclude=/build --exclude=/dist --exclude=/.dev \
  --exclude='*.db' --exclude='*.db-wal' --exclude='*.db-shm' \
  --include=/internal/webui/dist/.gitkeep --exclude='/internal/webui/dist/*' \
  --exclude=/web/node_modules --exclude=/web/.tanstack \
  --exclude=/web/test-results --exclude=/web/playwright-report --exclude=/web/screenshots \
  --exclude=.DS_Store --exclude=/.claude/settings.local.json \
  "${root}/" "${vm}:${dest}/"

echo "==> ${vm}:~/${dest}: suite ${suite}"
ssh "${ssh_opts[@]}" "${vm}" "cd ~/${dest} && bash scripts/vm-test.sh ${suite}"

# Screenshots come back into the local checkout (gitignored) for review.
if [ "${suite}" = shots ]; then
  rm -rf "${root}/web/screenshots"
  rsync -az -e "ssh ${ssh_opts[*]}" "${vm}:${dest}/web/screenshots/" "${root}/web/screenshots/"
  echo "==> screenshots in ${root}/web/screenshots/"
fi
