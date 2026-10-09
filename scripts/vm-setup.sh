#!/usr/bin/env bash
# Creates/starts a Linux VM (Lima by default, OrbStack with VM_TOOL=orb), installs the
# GoReleaser snapshot archive from dist/ and starts agent (root) and panel (user mcpanel)
# as transient systemd units. Panel state (DB, jail sets) is reset on every run.
# See docs/dev/linux-vm.md.
set -euo pipefail
cd "$(dirname "$0")/.."

VM_NAME="${VM_NAME:-mcpanel}"
VM_TOOL="${VM_TOOL:-lima}"
BUILD=1
PROVISION_ONLY=0

usage() {
  cat <<'EOF'
usage: scripts/vm-setup.sh [--no-build] [--provision-only]

  --no-build        reuse the snapshot archive already in dist/ (skip `make release`)
  --provision-only  create/start the VM and install packages, user and test network only

environment:
  VM_NAME   VM / machine name (default: mcpanel)
  VM_TOOL   lima (default) or orb (OrbStack)
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --no-build) BUILD=0 ;;
    --provision-only) PROVISION_ONLY=1 ;;
    -h | --help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
  shift
done

die() { echo "vm-setup: $*" >&2; exit 1; }

# vm_root runs the bash script on stdin as root inside the VM; arguments become $1, $2, ...
vm_root() {
  case "$VM_TOOL" in
    lima) limactl shell --workdir / "$VM_NAME" sudo bash -s -- "$@" ;;
    orb) orb -m "$VM_NAME" -u root bash -s -- "$@" ;;
    *) die "unknown VM_TOOL=$VM_TOOL (lima|orb)" ;;
  esac
}

# vm_user runs a command as the default (unprivileged) VM user.
vm_user() {
  case "$VM_TOOL" in
    lima) limactl shell --workdir / "$VM_NAME" "$@" ;;
    orb) orb -m "$VM_NAME" "$@" ;;
    *) die "unknown VM_TOOL=$VM_TOOL (lima|orb)" ;;
  esac
}

# Temp file inside the VM that holds the uploaded archive; removed again on every exit.
REMOTE_ARCHIVE=""
cleanup() {
  if [ -n "$REMOTE_ARCHIVE" ]; then
    vm_user rm -f -- "$REMOTE_ARCHIVE" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

# vm_copy copies a local file to a path inside the VM (as the default VM user).
vm_copy() {
  case "$VM_TOOL" in
    lima) limactl copy "$1" "$VM_NAME:$2" ;;
    orb) orb push -m "$VM_NAME" "$1" "$2" ;;
    *) die "unknown VM_TOOL=$VM_TOOL (lima|orb)" ;;
  esac
}

ensure_vm() {
  case "$VM_TOOL" in
    lima)
      command -v limactl >/dev/null || die "limactl not found - brew install lima"
      if ! limactl list --quiet | grep -qx "$VM_NAME"; then
        echo "== creating Lima VM $VM_NAME (Ubuntu LTS)"
        limactl start --name="$VM_NAME" --tty=false --cpus=2 --memory=4 template:ubuntu-lts
      elif [ "$(limactl list --format '{{.Status}}' "$VM_NAME")" != "Running" ]; then
        echo "== starting Lima VM $VM_NAME"
        limactl start --tty=false "$VM_NAME"
      fi
      ;;
    orb)
      command -v orb >/dev/null || die "orb not found - install OrbStack"
      if ! orb -m "$VM_NAME" true 2>/dev/null; then
        echo "== creating OrbStack machine $VM_NAME (Ubuntu)"
        orb create ubuntu "$VM_NAME"
      fi
      ;;
    *) die "unknown VM_TOOL=$VM_TOOL (lima|orb)" ;;
  esac
}

provision() {
  echo "== provisioning (nftables, curl, user mcpanel, test network)"
  vm_root <<'GUEST'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
if ! command -v nft >/dev/null || ! command -v curl >/dev/null; then
  apt-get update -qq
  apt-get install -y -qq nftables curl
fi
if ! id mcpanel >/dev/null 2>&1; then
  useradd --system --user-group --home-dir /var/lib/mc-panel --no-create-home --shell /usr/sbin/nologin mcpanel
fi
install -d -m 0750 -o root -g mcpanel /etc/mc-panel
# Test client: namespace mcpanel-client (192.0.2.2) <-> veth mcp-host in the root namespace (192.0.2.1).
if [ ! -e /run/netns/mcpanel-client ]; then
  ip netns add mcpanel-client
fi
if ! ip link show mcp-host >/dev/null 2>&1; then
  ip link add mcp-host type veth peer name mcp-client
  ip link set mcp-client netns mcpanel-client
  ip addr add 192.0.2.1/24 dev mcp-host
  ip link set mcp-host up
  ip netns exec mcpanel-client ip addr add 192.0.2.2/24 dev mcp-client
  ip netns exec mcpanel-client ip link set mcp-client up
  ip netns exec mcpanel-client ip link set lo up
fi
ip netns exec mcpanel-client ping -c 1 -W 2 192.0.2.1 >/dev/null
echo "provisioned: $(nft --version), user mcpanel uid=$(id -u mcpanel), client 192.0.2.2 -> 192.0.2.1 ok"
GUEST
}

install_and_start() {
  local arch goarch
  arch="$(echo 'uname -m' | vm_root)"
  case "$arch" in
    aarch64 | arm64) goarch=arm64 ;;
    x86_64 | amd64) goarch=amd64 ;;
    *) die "unsupported VM architecture: $arch" ;;
  esac

  if [ "$BUILD" -eq 1 ]; then
    echo "== building snapshot (make release)"
    make release
  fi
  shopt -s nullglob
  local archives=(dist/mc-panel_*_linux_"$goarch".tar.gz)
  shopt -u nullglob
  [ "${#archives[@]}" -eq 1 ] ||
    die "expected exactly one dist/mc-panel_*_linux_${goarch}.tar.gz, found ${#archives[@]} - run make release"
  echo "== installing ${archives[0]}"
  # Unpredictable name, owned by the VM user (0600), instead of a fixed /tmp path.
  REMOTE_ARCHIVE="$(vm_user mktemp /tmp/mc-panel.XXXXXXXX.tar.gz)"
  vm_copy "${archives[0]}" "$REMOTE_ARCHIVE"

  vm_root "$REMOTE_ARCHIVE" <<'GUEST'
set -euo pipefail
archive="$1"
for unit in mcpanel-panel mcpanel-agent; do
  systemctl stop "$unit.service" 2>/dev/null || true
  systemctl reset-failed "$unit.service" 2>/dev/null || true
done

tmp="$(mktemp -d)"
tar -xzf "$archive" -C "$tmp"
install -m 0755 -o root -g root "$tmp/panel" "$tmp/agent" /usr/local/bin/
rm -rf "$tmp" "$archive"

cat >/etc/mc-panel/agent.toml <<EOF
socket = "/run/mc-panel/agent.sock"
panel_uid = $(id -u mcpanel)
firewall = "nftables"
EOF
chown root:root /etc/mc-panel/agent.toml
chmod 0600 /etc/mc-panel/agent.toml

cat >/etc/mc-panel/panel.toml <<'EOF'
# VM test profile: production mode (no -dev). In production Caddy terminates TLS and the
# panel listens on its default unix socket; here it listens on TCP 192.0.2.1:8080 so the
# test client namespace (192.0.2.2) reaches it directly. public_url must use https.
listen = "192.0.2.1:8080"
public_url = "https://mcpanel.test"
data_dir = "/var/lib/mc-panel"
agent_socket = "/run/mc-panel/agent.sock"
trusted_proxies = ["127.0.0.1/32", "::1/128"]
EOF
chown root:mcpanel /etc/mc-panel/panel.toml
chmod 0640 /etc/mc-panel/panel.toml

# Fresh state on every run: no DB, no jail entries left over from earlier tests.
rm -f /var/lib/mc-panel/panel.db /var/lib/mc-panel/panel.db-wal /var/lib/mc-panel/panel.db-shm
for set in mcpanel_jail4 mcpanel_jail6; do
  nft flush set inet mcpanel "$set" 2>/dev/null || true
done

systemd-run --unit=mcpanel-agent --description="mc-panel agent (VM test)" \
  -p User=root -p Group=mcpanel \
  -p RuntimeDirectory=mc-panel -p RuntimeDirectoryMode=0750 \
  /usr/local/bin/agent -config /etc/mc-panel/agent.toml
for _ in $(seq 1 50); do [ -S /run/mc-panel/agent.sock ] && break; sleep 0.2; done
if [ ! -S /run/mc-panel/agent.sock ]; then
  journalctl -u mcpanel-agent --no-pager -n 50
  echo "agent socket /run/mc-panel/agent.sock missing" >&2
  exit 1
fi

systemd-run --unit=mcpanel-panel --description="mc-panel panel (VM test)" \
  -p User=mcpanel -p Group=mcpanel \
  -p StateDirectory=mc-panel -p StateDirectoryMode=0700 \
  /usr/local/bin/panel serve -config /etc/mc-panel/panel.toml
for _ in $(seq 1 50); do curl -fsS http://192.0.2.1:8080/api/health >/dev/null 2>&1 && break; sleep 0.2; done
if ! curl -fsS http://192.0.2.1:8080/api/health; then
  journalctl -u mcpanel-panel --no-pager -n 50
  echo "panel health check failed" >&2
  exit 1
fi
echo
stat -c 'socket: %a %U:%G %n' /run/mc-panel/agent.sock
systemctl is-active mcpanel-agent.service mcpanel-panel.service
if nft list table inet mcpanel >/dev/null 2>&1; then
  echo "nftables: table inet mcpanel present"
else
  journalctl -u mcpanel-agent --no-pager -n 50
  echo "table inet mcpanel missing (the agent creates it on startup)" >&2
  exit 1
fi
GUEST
}

ensure_vm
provision
if [ "$PROVISION_ONLY" -eq 1 ]; then
  echo "== done (provision only)"
  exit 0
fi
install_and_start
echo "== done: agent + panel running in VM $VM_NAME (panel at http://192.0.2.1:8080 inside the VM)"
