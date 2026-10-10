#!/usr/bin/env bash
# Acceptance check 5 (spec §12), nftables part: five failed logins from the test client
# namespace (192.0.2.2) must put that IP into `nft list set inet mcpanel mcpanel_jail4`.
# Redeploys and resets the VM state via scripts/vm-setup.sh --no-build first, so every run
# starts without jails.
set -euo pipefail
cd "$(dirname "$0")/.."

VM_NAME="${VM_NAME:-mcpanel}"
VM_TOOL="${VM_TOOL:-lima}"

case "${1:-}" in
  "") ;;
  -h | --help) echo "usage: scripts/vm-test.sh   (env: VM_NAME, VM_TOOL as in vm-setup.sh)"; exit 0 ;;
  *) echo "usage: scripts/vm-test.sh" >&2; exit 2 ;;
esac

vm_root() {
  case "$VM_TOOL" in
    lima) limactl shell --workdir / "$VM_NAME" sudo bash -s ;;
    orb) orb -m "$VM_NAME" -u root bash -s ;;
    *) echo "vm-test: unknown VM_TOOL=$VM_TOOL (lima|orb)" >&2; exit 2 ;;
  esac
}

VM_NAME="$VM_NAME" VM_TOOL="$VM_TOOL" scripts/vm-setup.sh --no-build

vm_root <<'GUEST'
set -euo pipefail
ns=mcpanel-client
client_ip=192.0.2.2
base=http://192.0.2.1:8080
origin=https://mcpanel.test
body=/tmp/vm-test-body

login() {
  ip netns exec "$ns" curl -sS -o "$body" -w '%{http_code}' --max-time 5 \
    -X POST "$base/api/auth/login" \
    -H 'Content-Type: application/json' \
    -H "Origin: $origin" \
    -H 'Sec-Fetch-Site: same-origin' \
    --data '{"username":"vm-test-nobody","password":"definitely-not-the-password"}'
}

echo "== 5 failed logins from $client_ip"
for i in 1 2 3 4 5; do
  code="$(login)" || code="curl-error"
  echo "attempt $i: HTTP $code $(cat "$body" 2>/dev/null || true)"
  if [ "$code" != 401 ]; then
    echo "FAIL: expected HTTP 401 on attempt $i" >&2
    journalctl -u mcpanel-panel --no-pager -n 40
    exit 1
  fi
done

echo "== waiting for $client_ip in set inet mcpanel mcpanel_jail4"
found=0
for _ in $(seq 1 30); do
  if nft list set inet mcpanel mcpanel_jail4 2>/dev/null | grep -qE "(^|[^0-9.])192\.0\.2\.2([^0-9]|$)"; then
    found=1
    break
  fi
  sleep 1
done
if [ "$found" -ne 1 ]; then
  nft list ruleset || true
  journalctl -u mcpanel-panel -u mcpanel-agent --no-pager -n 80
  echo "FAIL: $client_ip not in inet mcpanel mcpanel_jail4 after 30s" >&2
  exit 1
fi
nft list set inet mcpanel mcpanel_jail4

echo "== informational: request from $client_ip after the ban"
if ip netns exec "$ns" curl -sS -o /dev/null --max-time 3 "$base/api/health"; then
  echo "NOTE: request still reached the panel (app-level jail answers 403; packet drop depends on the agent's chain)"
else
  echo "OK: request from $client_ip was dropped by nftables"
fi
echo "PASS: acceptance check 5 (nftables part)"
GUEST
