#!/usr/bin/env bash
# Starts agent (if present), the files service (if present), panel and the Vite dev server for
# local development.
# If any of them exits, the others are stopped and the script exits non-zero.
# Compatible with macOS' bash 3.2 (no `wait -n`).
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p .dev/bin

go build -o .dev/bin/ ./cmd/...

pids=()
names=()
cleanup() { for p in ${pids[@]+"${pids[@]}"}; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# start NAME CMD... runs CMD in the background and remembers its pid.
start() {
  local name="$1"
  shift
  "$@" &
  pids+=($!)
  names+=("$name")
}

if [ -x .dev/bin/agent ]; then
  start agent .dev/bin/agent -dev
fi
# The files service runs as the developer in dev mode (instances below .dev/, the same tree the
# dev agent uses); in production it is the unprivileged user mcpanel-mc.
if [ -x .dev/bin/files ]; then
  start files .dev/bin/files -dev
fi
start panel .dev/bin/panel serve -dev
start vite bash -c 'cd web && exec npm run dev'

echo "mc-panel dev: http://localhost:5173"
while :; do
  for i in "${!pids[@]}"; do
    if ! kill -0 "${pids[$i]}" 2>/dev/null; then
      rc=0
      wait "${pids[$i]}" || rc=$?
      echo "dev: ${names[$i]} exited (status $rc); stopping the others" >&2
      exit 1
    fi
  done
  sleep 1
done
