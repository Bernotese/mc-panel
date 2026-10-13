#!/usr/bin/env bash
# Deploys mc-panel from a git ref onto the internal test VM (Ubuntu 26.04, no inbound internet):
# installs the toolchain and runtime packages on first use, clones/fetches the repo with the
# VM's GitHub deploy key, builds panel, agent and files, installs them (and the pinned restic)
# with systemd units and puts Caddy in front with its internal CA (no Let's Encrypt). Names
# resolve through sslip.io: panel.<ip>.sslip.io for the UI, <server>.mc.<ip>.sslip.io for
# Minecraft (dots as dashes).
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

# quiet runs a command and shows its output only if it fails.
quiet() {
  local out
  if ! out="$("$@" 2>&1)"; then
    printf '%s\n' "$out" >&2
    return 1
  fi
}

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
  install_restic
  build
  install_system "$panel_host" "$ip"
  restart_and_check "$panel_host"

  cat <<EOF

mc-panel $(git -C "$SRC" describe --always --dirty) deployed.
  Panel:        https://$panel_host/
  Basisdomain:  $mc_domain   (Minecraft: <servername>.$mc_domain)
  Caddy-CA:     $HOME/caddy-root.crt (im Browser vertrauen, sonst Zertifikatswarnung)
  Logs:         journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f
EOF
}

install_packages() {
  # bzip2: install-restic.sh unpacks the release; tzdata: restic reads the backup time zone from
  # /usr/share/zoneinfo (it embeds no zone data and silently falls back to UTC without it).
  local pkgs=(ca-certificates curl git jq make xz-utils bzip2 tzdata docker.io caddy nftables) missing=()
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

# install_restic: the pinned restic release for the files service's backups (spec 4 §7), from
# the checked-out tree (the script is read after the checkout).
install_restic() {
  log "restic"
  quiet sudo bash "$SRC/scripts/install-restic.sh"
}

build() {
  log "build"
  local lock_sum
  lock_sum="$(sha256sum "$SRC/web/package-lock.json" | cut -d' ' -f1)"
  if [ "$(cat "$SRC/web/node_modules/.mcpanel-lock.sha256" 2>/dev/null)" != "$lock_sum" ]; then
    (cd "$SRC/web" && quiet npm ci --no-audit --no-fund)
    echo "$lock_sum" >"$SRC/web/node_modules/.mcpanel-lock.sha256"
  fi
  (cd "$SRC/web" && quiet npm run build)
  local version
  version="0.0.0-testvm+$(git -C "$SRC" rev-parse --short HEAD)"
  (cd "$SRC" && export CGO_ENABLED=0 && quiet go build -trimpath -ldflags "-s -w -X main.version=$version" -o bin/ ./cmd/...)
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
  sudo install -m 0755 "$SRC/bin/panel" "$SRC/bin/agent" "$SRC/bin/files" /usr/local/bin/
  # Spec 3b §3.3: the files service (mcpanel-mc) may enter instances/, never write it; the
  # agent enforces the same owner and mode at start.
  sudo install -d -m 0750 -o root -g mcpanel-mc /srv/mc-panel/instances
  # Spec 4 §3.1: restic repos and their keys belong to mcpanel-mc alone (the agent enforces the
  # same owner and mode at start). It must exist before the files service starts: systemd sets
  # up ReadWritePaths only for existing paths.
  sudo install -d -m 0700 -o mcpanel-mc -g mcpanel-mc /srv/mc-panel/backups

  sudo tee /etc/mc-panel/agent.toml >/dev/null <<EOF
socket = "/run/mc-panel/agent.sock"
panel_uid = $(id -u mcpanel)
firewall = "nftables"
instances_dir = "/srv/mc-panel"
mc_user = "mcpanel-mc"
EOF
  sudo chmod 0600 /etc/mc-panel/agent.toml

  # The files service runs as mcpanel-mc without supplementary groups, so it cannot read
  # /etc/mc-panel (root:mcpanel 0750): its config lives in a directory of its own. socket and
  # socket_mode are unused here (systemd hands over the socket, see mcpanel-files.socket).
  sudo install -d -m 0750 -o root -g mcpanel-mc /etc/mc-panel-files
  sudo tee /etc/mc-panel-files/files.toml >/dev/null <<EOF
socket = "/run/mc-panel-files/files.sock"
panel_uid = $(id -u mcpanel)
instances_dir = "/srv/mc-panel"
restic_binary = "/usr/local/bin/restic"
EOF
  sudo chown root:mcpanel-mc /etc/mc-panel-files/files.toml
  sudo chmod 0640 /etc/mc-panel-files/files.toml

  sudo tee /etc/mc-panel/panel.toml >/dev/null <<EOF
listen = "127.0.0.1:8080"
public_url = "https://$panel_host"
data_dir = "/var/lib/mc-panel"
agent_socket = "/run/mc-panel/agent.sock"
files_socket = "/run/mc-panel-files/files.sock"
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

  # Spec 3b §3.1: the files service never runs as root and has no supplementary groups. systemd
  # creates the socket (owner mcpanel-mc, group mcpanel, 0660) in a directory of its own and
  # hands it over (LISTEN_FDS); /run/mc-panel stays the agent's, so stopping one unit never
  # removes the other's socket.
  sudo tee /etc/systemd/system/mcpanel-files.socket >/dev/null <<'EOF'
[Unit]
Description=mc-panel files service socket

[Socket]
ListenStream=/run/mc-panel-files/files.sock
SocketUser=mcpanel-mc
SocketGroup=mcpanel
SocketMode=0660
DirectoryMode=0755
RemoveOnStop=yes

[Install]
WantedBy=sockets.target
EOF

  # MemoryMax/TasksMax: defense in depth against crafted archives (zip parsing); 1G and 512
  # tasks leave room for restic (index and pack buffers, spec 4 §3.2). IOWeight/CPUWeight keep
  # backups behind the Minecraft servers and the panel. No network: local repos only.
  sudo tee /etc/systemd/system/mcpanel-files.service >/dev/null <<'EOF'
[Unit]
Description=mc-panel files service (unprivileged, Minecraft data)
Requires=mcpanel-files.socket
Wants=mcpanel-agent.service
After=network-online.target mcpanel-files.socket mcpanel-agent.service

[Service]
User=mcpanel-mc
Group=mcpanel-mc
ExecStart=/usr/local/bin/files -config /etc/mc-panel-files/files.toml
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
ProtectSystem=strict
ReadWritePaths=/srv/mc-panel/instances /srv/mc-panel/backups
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
RestrictAddressFamilies=AF_UNIX
SystemCallArchitectures=native
UMask=0027
MemoryMax=1G
TasksMax=512
IOWeight=50
CPUWeight=50

[Install]
WantedBy=multi-user.target
EOF

  sudo tee /etc/systemd/system/mcpanel-panel.service >/dev/null <<'EOF'
[Unit]
Description=mc-panel web panel
Wants=mcpanel-agent.service mcpanel-files.socket
After=network-online.target mcpanel-agent.service mcpanel-files.socket

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
  quiet sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
  sudo systemctl daemon-reload
  sudo systemctl enable mcpanel-agent mcpanel-files.socket mcpanel-files.service mcpanel-panel caddy >/dev/null 2>&1
}

# check_files_service: socket unit and service active, socket mcpanel-mc:mcpanel 0660; the
# service runs as mcpanel-mc without supplementary groups, without capabilities and with
# no_new_privs (spec 3b §3.1); backups available (restic) and /srv/mc-panel/backups
# mcpanel-mc 0700 (spec 4 §3); prints what is wrong otherwise.
check_files_service() {
  local pid uid status
  if ! sudo systemctl is-active --quiet mcpanel-files.socket || ! sudo systemctl is-active --quiet mcpanel-files.service; then
    echo "testvm-deploy: mcpanel-files.socket/.service not active" >&2
    sudo systemctl --no-pager --lines=20 status mcpanel-files.socket mcpanel-files.service >&2 || true
    return 1
  fi
  if [ "$(sudo stat -c '%U:%G %a' /run/mc-panel-files/files.sock)" != "mcpanel-mc:mcpanel 660" ] ||
    [ "$(sudo stat -c '%a' /run/mc-panel-files)" != 755 ]; then
    echo "testvm-deploy: files socket: $(sudo stat -c '%U:%G %a %n' /run/mc-panel-files /run/mc-panel-files/files.sock)" >&2
    return 1
  fi
  pid="$(systemctl show -p MainPID --value mcpanel-files)"
  uid="$(id -u mcpanel-mc)"
  status="$(sudo cat "/proc/$pid/status")"
  if ! grep -qxP "Uid:\t$uid\t$uid\t$uid\t$uid" <<<"$status" ||
    ! grep -qxP 'CapEff:\t0000000000000000' <<<"$status" ||
    ! grep -qxP 'NoNewPrivs:\t1' <<<"$status" ||
    grep -P '^Groups:' <<<"$status" | grep -qw "$(getent group mcpanel | cut -d: -f3)"; then
    echo "testvm-deploy: mcpanel-files runs with unexpected credentials:" >&2
    grep -E '^(Uid|Gid|Groups|CapEff|NoNewPrivs):' <<<"$status" >&2
    return 1
  fi
  if ! sudo -u mcpanel curl -fsS --max-time 10 --unix-socket /run/mc-panel-files/files.sock \
    http://files/v1/backups/status | grep -q '"available":true'; then
    echo "testvm-deploy: backups unavailable in mcpanel-files (restic?): $(sudo -u mcpanel curl -sS --unix-socket /run/mc-panel-files/files.sock http://files/v1/backups/status 2>&1)" >&2
    return 1
  fi
  if [ "$(sudo stat -c '%U:%G %a' /srv/mc-panel/backups)" != "mcpanel-mc:mcpanel-mc 700" ]; then
    echo "testvm-deploy: /srv/mc-panel/backups: $(sudo stat -c '%U:%G %a' /srv/mc-panel/backups)" >&2
    return 1
  fi
}

restart_and_check() {
  local panel_host="$1"
  log "restart"
  sudo systemctl restart mcpanel-agent
  sudo systemctl stop mcpanel-files.service
  sudo systemctl restart mcpanel-files.socket
  sudo systemctl start mcpanel-files.service
  sudo systemctl restart mcpanel-panel
  sudo systemctl reload-or-restart caddy
  local _
  for _ in $(seq 1 30); do
    if curl -fsS -o /dev/null --cacert <(sudo cat /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt 2>/dev/null) \
      --resolve "$panel_host:443:127.0.0.1" "https://$panel_host/api/health" 2>/dev/null; then
      sudo install -m 0644 -o "$(id -un)" /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt "$HOME/caddy-root.crt"
      check_files_service || exit 1
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
