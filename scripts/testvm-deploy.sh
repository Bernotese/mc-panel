#!/usr/bin/env bash
# Deploys mc-panel from a git ref onto the internal test VM (Ubuntu 26.04, no inbound internet):
# installs the toolchain and runtime packages on first use, clones/fetches the repo with the
# VM's GitHub deploy key, builds panel and agent, installs them with systemd units and puts
# Caddy in front with its internal CA (no Let's Encrypt). Names resolve through sslip.io:
# panel.<ip>.sslip.io for the UI, <server>.mc.<ip>.sslip.io for Minecraft (dots as dashes).
#
# Usage on the Mac:   make deploy-testvm [REF=<branch|tag|sha>] [TESTVM=user@host]
# Usage on the VM:    bash scripts/testvm-deploy.sh [REF]          (default: main)
# Runs as a user with passwordless sudo. Changes the host it runs on: the test VM only.
set -euo pipefail

REPO_URL="git@github.com:Bernotese/mc-panel.git"
SRC="$HOME/mc-panel"
GO_ROOT=/usr/local/go
NODE_ROOT=/usr/local/lib/nodejs
NODE_MAJOR=24

log() { printf '\n==> %s\n' "$*"; }

# The whole run lives in main(): bash parses it completely before the checkout below can
# replace this file on disk.
main() {
  local ref="${1:-main}"
  # Piped through ssh, stdin is this script: no command below may read from it.
  exec </dev/null
  if [ "$(uname -s)" != Linux ]; then
    echo "testvm-deploy: runs on the test VM (Linux), not here" >&2
    exit 1
  fi
  if ! sudo -n true 2>/dev/null; then
    echo "testvm-deploy: needs passwordless sudo for $(id -un)" >&2
    exit 1
  fi

  local arch node_arch
  case "$(uname -m)" in
    x86_64) arch=amd64 node_arch=x64 ;;
    aarch64) arch=arm64 node_arch=arm64 ;;
    *) echo "testvm-deploy: unsupported architecture $(uname -m)" >&2; exit 1 ;;
  esac

  local ip dashed panel_host mc_domain
  ip="${MCPANEL_TESTVM_IP:-$(ip -4 route get 1.1.1.1 | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1)}')}"
  dashed="${ip//./-}"
  panel_host="panel.$dashed.sslip.io"
  mc_domain="mc.$dashed.sslip.io"

  install_packages
  checkout "$ref"
  install_go "$arch"
  install_node "$node_arch"
  build
  install_system "$panel_host" "$ip"
  restart_and_check "$panel_host"

  cat <<EOF

mc-panel $(git -C "$SRC" describe --always --dirty) deployed.
  Panel:        https://$panel_host/
  Basisdomain:  $mc_domain   (Minecraft: <servername>.$mc_domain)
  Caddy-CA:     $HOME/caddy-root.crt (im Browser vertrauen, sonst Zertifikatswarnung)
  Logs:         journalctl -u mcpanel-agent -u mcpanel-panel -f
EOF
}

install_packages() {
  local pkgs=(ca-certificates curl git jq make xz-utils docker.io caddy nftables) missing=()
  for p in "${pkgs[@]}"; do
    dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q 'install ok installed' || missing+=("$p")
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    log "apt: ${missing[*]}"
    sudo apt-get update -qq
    sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${missing[@]}"
  fi
  sudo systemctl enable --now docker >/dev/null
}

checkout() {
  local ref="$1"
  if [ ! -d "$SRC/.git" ]; then
    log "git clone $REPO_URL"
    git clone -q "$REPO_URL" "$SRC"
  fi
  log "git checkout $ref"
  git -C "$SRC" fetch -q --prune --tags origin
  # Branch names resolve against origin, so "main" deploys what GitHub has, not a stale local branch.
  local target="$ref"
  if git -C "$SRC" rev-parse -q --verify "origin/$ref^{commit}" >/dev/null; then
    target="origin/$ref"
  fi
  git -C "$SRC" checkout -q --force --detach "$target"
  git -C "$SRC" clean -q -fd
  git -C "$SRC" log -1 --format='    %h %s (%cr)'
}

install_go() {
  local arch="$1" want have=""
  want="go$(awk '$1 == "go" { print $2 }' "$SRC/go.mod")"
  [ -x "$GO_ROOT/bin/go" ] && have="$("$GO_ROOT/bin/go" env GOVERSION)"
  if [ "$have" != "$want" ]; then
    log "Go $want"
    local file="$want.linux-$arch.tar.gz" sum tmp
    sum="$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' |
      jq -r --arg f "$file" '.[].files[] | select(.filename == $f) | .sha256')"
    [ -n "$sum" ] || { echo "testvm-deploy: no checksum for $file" >&2; exit 1; }
    tmp="$(mktemp -d)"
    curl -fsSL -o "$tmp/$file" "https://go.dev/dl/$file"
    echo "$sum  $tmp/$file" | sha256sum -c --quiet
    sudo rm -rf "$GO_ROOT"
    sudo tar -C /usr/local -xzf "$tmp/$file"
    rm -rf "$tmp"
  fi
  export PATH="$GO_ROOT/bin:$PATH"
}

install_node() {
  local node_arch="$1"
  if ! [ -x "$NODE_ROOT/bin/node" ] || [ "$("$NODE_ROOT/bin/node" -p 'process.versions.node.split(".")[0]')" != "$NODE_MAJOR" ]; then
    log "Node.js $NODE_MAJOR"
    local base="https://nodejs.org/dist/latest-v$NODE_MAJOR.x" file tmp
    tmp="$(mktemp -d)"
    curl -fsSL -o "$tmp/SHASUMS256.txt" "$base/SHASUMS256.txt"
    file="$(awk -v s="-linux-$node_arch.tar.xz" '$2 ~ s"$" { print $2 }' "$tmp/SHASUMS256.txt")"
    curl -fsSL -o "$tmp/$file" "$base/$file"
    (cd "$tmp" && grep " $file\$" SHASUMS256.txt | sha256sum -c --quiet)
    sudo rm -rf "$NODE_ROOT"
    sudo install -d "$NODE_ROOT"
    sudo tar -C "$NODE_ROOT" --strip-components=1 -xJf "$tmp/$file"
    rm -rf "$tmp"
  fi
  export PATH="$NODE_ROOT/bin:$PATH"
}

build() {
  log "build"
  local lock_sum
  lock_sum="$(sha256sum "$SRC/web/package-lock.json" | cut -d' ' -f1)"
  if [ "$(cat "$SRC/web/node_modules/.mcpanel-lock.sha256" 2>/dev/null)" != "$lock_sum" ]; then
    (cd "$SRC/web" && npm ci --no-audit --no-fund --loglevel=error)
    echo "$lock_sum" >"$SRC/web/node_modules/.mcpanel-lock.sha256"
  fi
  (cd "$SRC/web" && npm run --silent build >/dev/null)
  local version
  version="0.0.0-testvm+$(git -C "$SRC" rev-parse --short HEAD)"
  (cd "$SRC" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$version" -o bin/ ./cmd/...)
}

install_system() {
  local panel_host="$1" ip="$2"
  log "install"
  id mcpanel >/dev/null 2>&1 ||
    sudo useradd --system --user-group --home-dir /var/lib/mc-panel --no-create-home --shell /usr/sbin/nologin mcpanel
  id mcpanel-mc >/dev/null 2>&1 ||
    sudo useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin mcpanel-mc
  sudo install -d -m 0755 -o root -g root /srv/mc-panel
  sudo install -d -m 0750 -o root -g mcpanel /etc/mc-panel
  sudo install -m 0755 "$SRC/bin/panel" "$SRC/bin/agent" /usr/local/bin/

  sudo tee /etc/mc-panel/agent.toml >/dev/null <<EOF
socket = "/run/mc-panel/agent.sock"
panel_uid = $(id -u mcpanel)
firewall = "nftables"
instances_dir = "/srv/mc-panel"
mc_user = "mcpanel-mc"
EOF
  sudo chmod 0600 /etc/mc-panel/agent.toml

  sudo tee /etc/mc-panel/panel.toml >/dev/null <<EOF
listen = "127.0.0.1:8080"
public_url = "https://$panel_host"
data_dir = "/var/lib/mc-panel"
agent_socket = "/run/mc-panel/agent.sock"
trusted_proxies = ["127.0.0.1/32"]
EOF
  sudo chown root:mcpanel /etc/mc-panel/panel.toml
  sudo chmod 0640 /etc/mc-panel/panel.toml

  sudo tee /etc/systemd/system/mcpanel-agent.service >/dev/null <<'EOF'
[Unit]
Description=mc-panel agent (nftables, Docker)
Wants=docker.service
After=network-online.target docker.service

[Service]
User=root
Group=mcpanel
RuntimeDirectory=mc-panel
RuntimeDirectoryMode=0750
ExecStart=/usr/local/bin/agent -config /etc/mc-panel/agent.toml
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF

  sudo tee /etc/systemd/system/mcpanel-panel.service >/dev/null <<'EOF'
[Unit]
Description=mc-panel web panel
Wants=mcpanel-agent.service
After=network-online.target mcpanel-agent.service

[Service]
User=mcpanel
Group=mcpanel
StateDirectory=mc-panel
StateDirectoryMode=0700
ExecStart=/usr/local/bin/panel serve -config /etc/mc-panel/panel.toml
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF

  # tls internal: Caddy signs with its own local CA instead of asking Let's Encrypt, which
  # cannot reach this VM. The bare IP redirects to the panel name.
  sudo tee /etc/caddy/Caddyfile >/dev/null <<EOF
{
	skip_install_trust
}

$panel_host {
	tls internal
	reverse_proxy 127.0.0.1:8080
}

$ip {
	tls internal
	redir https://$panel_host{uri}
}
EOF
  sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null
  sudo systemctl daemon-reload
  sudo systemctl enable mcpanel-agent mcpanel-panel caddy >/dev/null 2>&1
}

restart_and_check() {
  local panel_host="$1"
  log "restart"
  sudo systemctl restart mcpanel-agent
  sudo systemctl restart mcpanel-panel
  sudo systemctl reload-or-restart caddy
  local _
  for _ in $(seq 1 30); do
    if curl -fsS -o /dev/null --cacert <(sudo cat /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt 2>/dev/null) \
      --resolve "$panel_host:443:127.0.0.1" "https://$panel_host/api/health" 2>/dev/null; then
      sudo install -m 0644 -o "$(id -un)" /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt "$HOME/caddy-root.crt"
      return 0
    fi
    sleep 1
  done
  echo "testvm-deploy: https://$panel_host/api/health not healthy after 30 s" >&2
  sudo systemctl --no-pager --lines=20 status mcpanel-agent mcpanel-panel caddy >&2 || true
  exit 1
}

main "$@"
exit
