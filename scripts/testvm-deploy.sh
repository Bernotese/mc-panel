#!/usr/bin/env bash
# Deploys mc-panel from a git ref onto the internal test VM (Ubuntu 26.04, no inbound internet):
# installs the build toolchain on first use, clones/fetches the repo with the VM's GitHub
# deploy key, builds panel, agent and files and hands them to the installer
# (scripts/install, sub-project 6): `install` on the first run, `update` afterwards. The
# installer owns users, permissions, config, systemd units and the Caddy site (deploy/ is the
# reference for them); this script only adds the VM's Caddyfile frame (tls internal, the bare
# IP redirects to the panel name). Names resolve through sslip.io: panel.<ip>.sslip.io for the
# UI, <server>.mc.<ip>.sslip.io for Minecraft (dots as dashes).
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
CADDY_CA=/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt
# Set only after a successful `install`: until then every deploy runs `install` again.
INSTALLED_MARKER=/etc/mc-panel/.testvm-installed
CADDYFILE=/etc/caddy/Caddyfile
CADDYFILE_PREV=/etc/caddy/Caddyfile.testvm-prev
CADDY_DROPIN=/etc/systemd/system/caddy.service.d/mc-panel.conf

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
  local version stage
  version="0.0.0-testvm+$(git -C "$SRC" rev-parse --short HEAD)"
  build "$version"
  stage="$(stage_installer "$version")"
  migrate_once
  install_restic
  write_caddy_frame "$panel_host" "$ip"
  if ! run_installer "$stage" "$panel_host"; then
    restore_caddy_frame
    echo "testvm-deploy: install.sh failed (details: sudo tail -n 50 /var/log/mc-panel-install.log)" >&2
    exit 1
  fi
  finish "$stage" "$panel_host"

  cat <<EOF

mc-panel $(git -C "$SRC" describe --always --dirty) deployed.
  Panel:        https://$panel_host/
  Basisdomain:  $mc_domain   (Minecraft: <servername>.$mc_domain)
  Caddy-CA:     $HOME/caddy-root.crt (im Browser vertrauen, sonst Zertifikatswarnung)
  Logs:         journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f
  Installer:    sudo $stage/install.sh doctor   (Protokoll: /var/log/mc-panel-install.log)
EOF
}

# install_packages: what the build and install-restic.sh need (bzip2 unpacks restic). Docker,
# Caddy, nftables, sqlite3, tzdata and the rest come from the installer (packages step).
install_packages() {
  local pkgs=(ca-certificates curl git jq make xz-utils bzip2) missing=() p
  for p in "${pkgs[@]}"; do
    dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q 'install ok installed' || missing+=("$p")
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    log "apt: ${missing[*]}"
    sudo apt-get update -qq
    sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${missing[@]}"
  fi
}

checkout() {
  local ref="$1"
  if [ ! -d "$SRC/.git" ]; then
    log "git clone $REPO_URL"
    git clone -q "$REPO_URL" "$SRC"
  fi
  log "git checkout $ref"
  # --force: a tag GitHub moved (the history rewrite of 2026-10-28) replaces the local one
  # instead of failing the fetch with "would clobber existing tag".
  git -C "$SRC" fetch -q --prune --tags --force origin
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

# build <version>: web build, then panel, agent and files with that version.
build() {
  log "build $1"
  local lock_sum
  lock_sum="$(sha256sum "$SRC/web/package-lock.json" | cut -d' ' -f1)"
  if [ "$(cat "$SRC/web/node_modules/.mcpanel-lock.sha256" 2>/dev/null)" != "$lock_sum" ]; then
    (cd "$SRC/web" && quiet npm ci --ignore-scripts --no-audit --no-fund)
    echo "$lock_sum" >"$SRC/web/node_modules/.mcpanel-lock.sha256"
  fi
  (cd "$SRC/web" && quiet npm run build)
  (cd "$SRC" && export CGO_ENABLED=0 && quiet go build -trimpath -ldflags "-s -w -X main.version=$1" -o bin/ ./cmd/...)
}

# stage_installer <version>: binaries, deploy/ and the assembled install.sh in the format of
# install.sh --from-dir; prints the directory.
stage_installer() {
  local version="$1" dir="$SRC/build/testvm"
  rm -rf "$dir"
  mkdir -p "$dir"
  cp "$SRC/bin/panel" "$SRC/bin/agent" "$SRC/bin/files" "$dir/"
  if [ ! -f "$SRC/scripts/install/assemble.sh" ] || [ ! -d "$SRC/deploy" ]; then
    echo "testvm-deploy: this ref has no installer (scripts/install/assemble.sh, deploy/); deploy a ref from sub-project 6 on" >&2
    exit 1
  fi
  cp -r "$SRC/deploy" "$dir/deploy"
  quiet bash "$SRC/scripts/install/assemble.sh" "$version" "$dir/install.sh"
  chmod 0755 "$dir/install.sh"
  printf '%s\n' "$dir"
}

# migrate_once: before sub-project 6 the VM was deployed by heredocs in this script (panel on
# TCP 127.0.0.1:8080, no panel socket unit). The configs held nothing hand-made, so they are
# moved aside (*.pre-installer) and the installer writes them anew; it would only add missing
# keys and keep listen = "127.0.0.1:8080". Only that old layout is moved, and never twice (an
# existing *.pre-installer stays the copy of the original). The data (DB with the admin, secret
# key, worlds, backups) stays; stopping the agent does not stop the Minecraft containers.
migrate_once() {
  sudo test -e "$INSTALLED_MARKER" && return 0
  sudo test -e /etc/mc-panel/panel.toml || return 0
  sudo test -e /etc/mc-panel/panel.toml.pre-installer && return 0
  sudo grep -qE '^[[:space:]]*listen[[:space:]]*=[[:space:]]*"127\.0\.0\.1:8080"' /etc/mc-panel/panel.toml || return 0
  log "migrate the pre-installer deployment"
  local f
  for f in /etc/mc-panel/panel.toml /etc/mc-panel/agent.toml /etc/mc-panel-files/files.toml; do
    if sudo test -e "$f" && ! sudo test -e "$f.pre-installer"; then
      sudo mv "$f" "$f.pre-installer"
    fi
  done
  sudo systemctl stop mcpanel-panel.service mcpanel-files.service mcpanel-files.socket mcpanel-agent.service 2>/dev/null || true
}

# install_restic: the pinned restic of the files service (scripts/install-restic.sh, idempotent);
# the installer takes it as restic_binary because /usr/local/bin comes first in PATH.
install_restic() {
  log "restic"
  quiet sudo bash "$SRC/scripts/install-restic.sh"
}

caddy_validate() {
  sudo runuser -u caddy -- env HOME=/var/lib/caddy caddy validate --config "$CADDYFILE" --adapter caddyfile >/dev/null 2>&1
}

# write_caddy_frame <panel_host> <ip>: the VM's Caddyfile around the installer's site: no trust
# store changes (tls internal), the bare IP redirects to the panel name, and the installer's
# import line already in its exact managed form (the installer then leaves the Caddyfile alone;
# uninstall removes exactly that line). It must be in place before the first install: the old
# Caddyfile has the panel site inline, and the installer's import next to it would define the
# site twice. Like a Caddyfile of the installer it moves Caddy's admin API to a socket in
# /run/caddy, but only once the installer's drop-in caddy.service.d/mc-panel.conf exists, which
# creates that directory (so on a fresh VM from the second deploy on). The previous file stays
# in Caddyfile.testvm-prev for restore_caddy_frame. The installer validates and reloads Caddy
# when its site changes; a frame that changed on its own is validated and applied here (and put
# back if Caddy rejects it): with a restart, because caddy reload would send the new
# configuration to the admin address it names. A ref with web ports (deploy/caddy/mc-panel-web.caddy)
# also gets what the installer adds only to a Caddyfile of its own: the on-demand ask of
# mcpanel-web (the default web_ask_port 18101 of agent.toml) and the marked import of the web
# interfaces site, which the installer writes next.
write_caddy_frame() {
  local panel_host="$1" ip="$2" tmp admin="" ask="" web_import=""
  if [ -e "$CADDY_DROPIN" ]; then
    admin=$'\tadmin unix//run/caddy/admin.sock\n'
  fi
  if [ -f "$SRC/deploy/caddy/mc-panel-web.caddy" ]; then
    ask=$'\ton_demand_tls {\n\t\task http://127.0.0.1:18101/\n\t}\n'
    web_import=$'\nimport mc-panel-web.caddy # managed by mc-panel'
  fi
  tmp="$(mktemp)"
  cat >"$tmp" <<EOF
{
${admin}	skip_install_trust
${ask}}

import mc-panel.caddy # managed by mc-panel${web_import}

$ip {
	tls internal
	redir https://$panel_host{uri}
}
EOF
  if sudo cmp -s "$tmp" "$CADDYFILE"; then
    rm -f "$tmp"
    return 0
  fi
  log "Caddyfile frame"
  sudo install -d -m 0755 /etc/caddy
  if sudo test -f "$CADDYFILE"; then
    sudo cp -p "$CADDYFILE" "$CADDYFILE_PREV"
  fi
  sudo install -m 0644 -o root -g root "$tmp" "$CADDYFILE.new"
  sudo mv -f "$CADDYFILE.new" "$CADDYFILE"
  rm -f "$tmp"
  # On the first run mc-panel.caddy does not exist yet: the installer writes it next and
  # reloads Caddy (the running Caddy keeps its old configuration until then).
  # The same for mc-panel-web.caddy on the first deploy of a ref with web ports.
  if [ -e /etc/caddy/mc-panel.caddy ] && { [ -z "$web_import" ] || [ -e /etc/caddy/mc-panel-web.caddy ]; } &&
    systemctl is-active --quiet caddy.service; then
    if ! caddy_validate; then
      restore_caddy_frame
      echo "testvm-deploy: caddy rejects the new Caddyfile frame; the previous one is back" >&2
      exit 1
    fi
    sudo systemctl restart caddy.service
  fi
}

# restore_caddy_frame: after a failed installer run (or a rejected frame), puts the previous
# Caddyfile back if the current one does not validate (e.g. the import of a mc-panel.caddy that
# was never written), so a later Caddy restart still comes up.
restore_caddy_frame() {
  if sudo test -f "$CADDYFILE_PREV" && ! caddy_validate; then
    sudo cp -p "$CADDYFILE_PREV" "$CADDYFILE"
    echo "testvm-deploy: restored the previous $CADDYFILE" >&2
  fi
}

# run_installer <stage> <panel_host>: `install` until one has succeeded (marker
# $INSTALLED_MARKER, root-only) and while the Caddy site is missing, `update` afterwards (the
# same build again needs --force). The VM keeps its own firewall (internal network):
# --no-firewall. stdin is /dev/null and there is no terminal; the admin already exists in the
# database.
run_installer() {
  local stage="$1" panel_host="$2"
  if sudo test -e "$INSTALLED_MARKER" && [ -e /etc/caddy/mc-panel.caddy ]; then
    log "install.sh update"
    sudo "$stage/install.sh" update --from-dir "$stage" --force --yes
    return
  fi
  log "install.sh install"
  sudo "$stage/install.sh" install --from-dir "$stage" --tls internal --domain "$panel_host" --no-firewall --yes || return
  sudo install -m 0600 -o root -g root /dev/null "$INSTALLED_MARKER"
}

# finish <stage> <panel_host>: health through Caddy, copy of the Caddy CA for the browser,
# doctor (warnings are expected on the VM: no own file system for /srv/mc-panel, no host
# firewall).
finish() {
  local stage="$1" panel_host="$2" _
  for _ in $(seq 1 30); do
    if curl -fsS -o /dev/null --cacert <(sudo cat "$CADDY_CA" 2>/dev/null) \
      --resolve "$panel_host:443:127.0.0.1" "https://$panel_host/api/health" 2>/dev/null; then
      sudo install -m 0644 -o "$(id -un)" "$CADDY_CA" "$HOME/caddy-root.crt"
      # Exit code 1 (warnings) is expected on the VM; 2 means a check failed.
      local rc=0
      sudo "$stage/install.sh" doctor || rc=$?
      if [ "$rc" -ge 2 ]; then
        echo "testvm-deploy: install.sh doctor reports failures (exit $rc)" >&2
        exit 1
      fi
      return 0
    fi
    sleep 1
  done
  echo "testvm-deploy: https://$panel_host/api/health not healthy after 30 s" >&2
  sudo systemctl --no-pager --lines=20 status 'mcpanel-*' caddy >&2 || true
  exit 1
}

main "$@"
exit
