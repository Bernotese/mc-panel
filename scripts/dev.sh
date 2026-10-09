#!/usr/bin/env bash
# Starts agent (if present), panel and the Vite dev server for local development.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p .dev/bin

go build -o .dev/bin/ ./cmd/...

pids=()
cleanup() { for p in ${pids[@]+"${pids[@]}"}; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT INT TERM

if [ -x .dev/bin/agent ]; then
  .dev/bin/agent -dev & pids+=($!)
fi
.dev/bin/panel serve -dev & pids+=($!)
(cd web && npm run dev) & pids+=($!)

echo "mc-panel dev: http://localhost:5173"
wait
