#!/usr/bin/env bash
# Acceptance check 5 (foundation spec §12), nftables part, for a Linux CI runner:
# runs agent (root, nftables) and panel (user mcpanel, production mode) like on the VPS,
# sends five failed logins from a test client network namespace (192.0.2.2) and expects
# that IP in `nft list set inet mcpanel mcpanel_jail4`, after which its packets are dropped.
#
# Usage (as root): scripts/ci-acceptance.sh PANEL_BINARY AGENT_BINARY
# Changes the host (user mcpanel, netns mcpanel-client, veth mcp-host, table inet mcpanel):
# only for disposable CI runners, never on a developer machine.
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: scripts/ci-acceptance.sh PANEL_BINARY AGENT_BINARY" >&2
  exit 2
fi
if [ "$(id -u)" -ne 0 ]; then
  echo "ci-acceptance: must run as root" >&2
  exit 1
fi
if [ "$(uname -s)" != Linux ]; then
  echo "ci-acceptance: Linux only" >&2
  exit 1
fi

ns=mcpanel-client
client_ip=192.0.2.2
base=http://192.0.2.1:8080
origin=https://mcpanel.test

work="$(mktemp -d)"
chmod 0755 "$work"
pids=()

cleanup() {
  for pid in "${pids[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
  ip link del mcp-host 2>/dev/null || true
  ip netns del "$ns" 2>/dev/null || true
  nft delete table inet mcpanel 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

logs() {
  echo "--- agent log" >&2
  cat "$work/agent.log" >&2 || true
  echo "--- panel log" >&2
  cat "$work/panel.log" >&2 || true
}

echo "== setup: user mcpanel, binaries, test network"
if ! id mcpanel >/dev/null 2>&1; then
  useradd --system --user-group --no-create-home --shell /usr/sbin/nologin mcpanel
fi
install -d -m 0755 "$work/bin"
install -m 0755 "$1" "$work/bin/panel"
install -m 0755 "$2" "$work/bin/agent"
install -d -m 0750 -o root -g mcpanel "$work/run"
install -d -m 0700 -o mcpanel -g mcpanel "$work/data"

ip netns add "$ns"
ip link add mcp-host type veth peer name mcp-client
ip link set mcp-client netns "$ns"
ip addr add 192.0.2.1/24 dev mcp-host
ip link set mcp-host up
ip netns exec "$ns" ip addr add "$client_ip/24" dev mcp-client
ip netns exec "$ns" ip link set mcp-client up
ip netns exec "$ns" ip link set lo up
ip netns exec "$ns" ping -c 1 -W 2 192.0.2.1 >/dev/null

cat >"$work/agent.toml" <<EOF
socket = "$work/run/agent.sock"
panel_uid = $(id -u mcpanel)
firewall = "nftables"
EOF
cat >"$work/panel.toml" <<EOF
listen = "192.0.2.1:8080"
public_url = "$origin"
data_dir = "$work/data"
agent_socket = "$work/run/agent.sock"
geoip_path = "$work/data/none.mmdb"
EOF
chmod 0644 "$work/agent.toml" "$work/panel.toml"

echo "== start agent (root) and panel (mcpanel)"
"$work/bin/agent" -config "$work/agent.toml" >"$work/agent.log" 2>&1 &
pids+=("$!")
for _ in $(seq 1 50); do [ -S "$work/run/agent.sock" ] && break; sleep 0.2; done
if [ ! -S "$work/run/agent.sock" ]; then
  logs
  echo "FAIL: agent socket missing" >&2
  exit 1
fi
setpriv --reuid=mcpanel --regid=mcpanel --init-groups \
  "$work/bin/panel" serve -config "$work/panel.toml" >"$work/panel.log" 2>&1 &
pids+=("$!")
for _ in $(seq 1 50); do curl -fsS "$base/api/health" >/dev/null 2>&1 && break; sleep 0.2; done
if ! curl -fsS "$base/api/health"; then
  logs
  echo "FAIL: panel health check" >&2
  exit 1
fi
echo
nft list table inet mcpanel >/dev/null

login() {
  ip netns exec "$ns" curl -sS -o "$work/body" -w '%{http_code}' --max-time 5 \
    -X POST "$base/api/auth/login" \
    -H 'Content-Type: application/json' \
    -H "Origin: $origin" \
    -H 'Sec-Fetch-Site: same-origin' \
    --data '{"username":"ci-nobody","password":"definitely-not-the-password"}'
}

echo "== 5 failed logins from $client_ip"
for i in 1 2 3 4 5; do
  code="$(login)" || code="curl-error"
  echo "attempt $i: HTTP $code $(cat "$work/body" 2>/dev/null || true)"
  if [ "$code" != 401 ]; then
    logs
    echo "FAIL: expected HTTP 401 on attempt $i" >&2
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
  logs
  echo "FAIL: $client_ip not in inet mcpanel mcpanel_jail4 after 30s" >&2
  exit 1
fi
nft list set inet mcpanel mcpanel_jail4

echo "== request from $client_ip after the ban must be dropped"
if ip netns exec "$ns" curl -sS -o /dev/null --max-time 3 "$base/api/health"; then
  nft list ruleset || true
  echo "FAIL: request from $client_ip still reached the panel" >&2
  exit 1
fi
echo "PASS: acceptance check 5 (nftables part)"
