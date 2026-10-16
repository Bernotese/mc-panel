#!/usr/bin/env bash
# Instance acceptance for a Linux CI runner with Docker, split into shards that CI runs as
# parallel jobs (job instances, matrix). Every shard starts the real agent as root and the files
# service as mcpanel-mc (scripts/ci-instances/lib.sh); the panel shards then start the panel in
# production mode as mcpanel, sign in (password, mandatory TOTP, sudo) and create a Paper server
# through the panel API. What each shard checks is described at the top of its file:
#   core               agent, router, hardening, egress filter, console, files service, crash,
#                      extraction (scripts/ci-instances/core.sh)
#   plugins            plugins from the real Modrinth and Hangar, mrpack import (plugins.sh)
#   monitoring-metrics history, live and host values, RCON noise, host.disk_low/ok
#   monitoring-alerts  server.down/recovered, server.crash_loop, the down-alerts switch
#   backups            backup, restore, download, schedule, pin, delete, purge with restic
#
# MCPANEL_ACCEPTANCE_TIMESCALE=<n> (a panel built with -tags acceptance, see internal/timescale)
# makes the monitoring passes, alert windows and the backup tick n times faster; the shards
# scale their waits with it. CI uses 10.
#
# Usage (as root): scripts/ci-instances.sh SHARD AGENT_BINARY FILES_BINARY PANEL_BINARY
# Changes the host (users, Docker containers/network, ports 25565 and 8080, nftables tables
# inet mcpanel and inet mcpanel_citest, network namespace mcpcitest) and needs internet (Mojang,
# PaperMC, Modrinth, Hangar): disposable CI runners only.
set -euo pipefail
cd "$(dirname "$0")/.."

shards="core plugins monitoring-metrics monitoring-alerts backups"
if [ "$#" -ne 4 ] || ! [[ " $shards " == *" $1 "* ]]; then
  echo "usage: scripts/ci-instances.sh SHARD AGENT_BINARY FILES_BINARY PANEL_BINARY (SHARD: ${shards// /, })" >&2
  exit 2
fi
if [ "$(id -u)" -ne 0 ] || [ "$(uname -s)" != Linux ]; then
  echo "ci-instances: Linux and root only" >&2
  exit 1
fi
shard="$1"
agent_bin="$2"
files_bin="$3"
panel_bin="$4"

# shellcheck source=scripts/ci-instances/lib.sh
. scripts/ci-instances/lib.sh
# shellcheck source=/dev/null # one of scripts/ci-instances/<shard>.sh
. "scripts/ci-instances/$shard.sh"

echo "PASS: instance acceptance, shard $shard (time scale $ts)"
