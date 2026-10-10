# Teilprojekt 2 – VM, Doku und Abnahme (Aufgaben 23–25) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Die Linux-VM so erweitern, dass Minecraft-Server produktionsnah laufen (Docker, Benutzer `mcpanel-mc`, Router auf Port 25565), einen automatisierten Instanz-Test bereitstellen, die Entwickler-Doku nachziehen und Teilprojekt 2 gegen die Abnahmekriterien der Spec prüfen.

**Architecture:** `scripts/vm-setup.sh` installiert Docker und legt `mcpanel-mc` an; der agent bekommt `instances_dir` und `mc_user` in `agent.toml`. `scripts/vm-test-instances.sh` spricht den agent direkt als root über seinen Socket an (erlaubte Peers: panel-UID und 0), legt einen Paper-Server an, prüft Härtung, Routing (Minecraft-Status-Ping über mc-router mit Hostnamen), Absturz-Neustart und Löschen. Die übrigen Kriterien prüft eine Checkliste im Browser.

**Tech Stack:** Bash (shellcheck-sauber), Python 3 (nur Standardbibliothek, für den Status-Ping), Lima/OrbStack, Docker 29 (Ubuntu-Paket `docker.io`).

**Spec:** `docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md` (§3.3, §3.4, §10, §12)

## Global Constraints

Es gelten alle Global Constraints aus `2026-10-10-instances-00-overview.md`. Zusätzlich:

| Thema | Wert |
|---|---|
| Zwischenstand VM | Ab Aufgabe 4 startet der agent in Produktion nur, wenn `mc_user` existiert; wer zwischen Aufgabe 4 und 23 in der VM testet, legt ihn vorher an: `limactl shell mcpanel sudo useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin mcpanel-mc` |
| Voraussetzung CLAUDE.md | Branch `docs/claude-md` ist in `main` gemerged (Aufgabe 24 ändert `CLAUDE.md`) |
| Voraussetzung | Branch `chore/vm-browser-access` (Caddy-Zugang `https://mcpanel.localhost:8443`, `trusted_proxies = ["192.0.2.1/32"]`) ist in `main` gemerged, bevor `feat/instances` abzweigt |
| Docker in der VM | Paket `docker.io`, Dienst `docker` aktiviert |
| Minecraft-Benutzer | System-Benutzer `mcpanel-mc` (eigene Gruppe, kein Login, Home `/nonexistent`) |
| `instances_dir` | `/srv/mc-panel` |
| Basisdomain im VM-Test | `mc.localhost` |
| Test-Server | ID `9001`, Name `vmtest`, FQDN `vmtest.mc.localhost`, Paper `1.21.11`, java21, 2048 MiB |
| Router-Port | `25565/tcp`, von Lima automatisch nach `127.0.0.1:25565` auf dem Mac weitergeleitet |
| Zeitlimits Test | Image-Pull + Start ≤ 15 min, Neustart nach Absturz ≤ 3 min |

---

### Task 23: VM mit Docker und automatisierter Instanz-Test

**Files:**
- Modify: `scripts/vm-setup.sh` (Provisionierung: Docker, `mcpanel-mc`; `agent.toml`; Reset von Containern und Daten)
- Create: `scripts/mc-status.py` (Minecraft-Status-Ping mit frei wählbarer Server-Adresse)
- Create: `scripts/vm-test-instances.sh` (Abnahme Instanzen über den agent-Socket)

**Interfaces:**
- Consumes: agent-Endpunkte aus Aufgabe 9 (`GET /v1/instances`, `POST /v1/instances/ensure|power|remove`), Konfigurationsschlüssel aus Aufgabe 4 (`instances_dir`, `mc_user`), Container-/Label-Namen aus der Übersicht.
- Produces: `scripts/mc-status.py HOST PORT SERVER_ADDRESS` (Exit 0 + JSON auf stdout bei Antwort, sonst Exit 1); `scripts/vm-test-instances.sh` endet mit `PASS: instance acceptance (agent path)`.

- [ ] **Step 1: Status-Ping schreiben** – `scripts/mc-status.py`

```python
#!/usr/bin/env python3
"""Minecraft Java Edition status ping (server list ping).

Unlike generic tools it sends an explicit server address in the handshake, which is what
mc-router routes on. Usage: mc-status.py HOST PORT SERVER_ADDRESS
Prints the status JSON and exits 0, or prints the error and exits 1.
"""
import json
import socket
import struct
import sys


def varint(n: int) -> bytes:
    n &= 0xFFFFFFFF
    out = bytearray()
    while True:
        b = n & 0x7F
        n >>= 7
        if n:
            out.append(b | 0x80)
        else:
            out.append(b)
            return bytes(out)


def packet(packet_id: int, payload: bytes) -> bytes:
    body = varint(packet_id) + payload
    return varint(len(body)) + body


def recv_exact(sock: socket.socket, n: int) -> bytes:
    buf = bytearray()
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise EOFError("connection closed by server")
        buf += chunk
    return bytes(buf)


def read_varint_sock(sock: socket.socket) -> int:
    value = shift = 0
    while True:
        b = recv_exact(sock, 1)[0]
        value |= (b & 0x7F) << shift
        if not b & 0x80:
            return value
        shift += 7
        if shift > 35:
            raise ValueError("varint too long")


def read_varint_buf(buf: bytes, pos: int) -> tuple[int, int]:
    value = shift = 0
    while True:
        b = buf[pos]
        pos += 1
        value |= (b & 0x7F) << shift
        if not b & 0x80:
            return value, pos
        shift += 7
        if shift > 35:
            raise ValueError("varint too long")


def status(host: str, port: int, server_address: str) -> dict:
    addr = server_address.encode("utf-8")
    handshake = varint(-1) + varint(len(addr)) + addr + struct.pack(">H", port) + varint(1)
    with socket.create_connection((host, port), timeout=10) as sock:
        sock.sendall(packet(0x00, handshake))
        sock.sendall(packet(0x00, b""))
        length = read_varint_sock(sock)
        if length <= 0 or length > 1 << 21:
            raise ValueError(f"implausible packet length {length}")
        data = recv_exact(sock, length)
    packet_id, pos = read_varint_buf(data, 0)
    if packet_id != 0x00:
        raise ValueError(f"unexpected packet id {packet_id}")
    strlen, pos = read_varint_buf(data, pos)
    return json.loads(data[pos : pos + strlen].decode("utf-8"))


def main() -> int:
    if len(sys.argv) != 4:
        print("usage: mc-status.py HOST PORT SERVER_ADDRESS", file=sys.stderr)
        return 2
    try:
        result = status(sys.argv[1], int(sys.argv[2]), sys.argv[3])
    except (OSError, EOFError, ValueError, json.JSONDecodeError) as exc:
        print(f"mc-status: {exc}", file=sys.stderr)
        return 1
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

Ausführbar machen: `chmod +x scripts/mc-status.py`.

- [ ] **Step 2: Status-Ping gegen einen lokalen Fake prüfen**

Kurzer Selbsttest ohne Minecraft: ein Fake-Server, der die Handshake-Adresse zurückmeldet.

```bash
python3 -I - <<'PY'
import socket, subprocess, sys, threading, json, struct

def varint(n):
    n &= 0xFFFFFFFF; out = bytearray()
    while True:
        b = n & 0x7F; n >>= 7
        if n: out.append(b | 0x80)
        else: out.append(b); return bytes(out)

def serve(l):
    c, _ = l.accept()
    with c:
        data = c.recv(4096)
        addr_seen = b"vmtest.mc.localhost" in data
        body = json.dumps({"version": {"name": "fake"}, "addr_ok": addr_seen}).encode()
        payload = varint(0) + varint(len(body)) + body
        c.sendall(varint(len(payload)) + payload)

l = socket.socket(); l.bind(("127.0.0.1", 0)); l.listen(1)
port = l.getsockname()[1]
threading.Thread(target=serve, args=(l,), daemon=True).start()
out = subprocess.run([sys.executable, "-I", "scripts/mc-status.py", "127.0.0.1", str(port), "vmtest.mc.localhost"],
                     capture_output=True, text=True, timeout=10)
print(out.stdout.strip(), out.stderr.strip(), out.returncode)
assert out.returncode == 0 and json.loads(out.stdout)["addr_ok"] is True
PY
```

Expected: eine Zeile mit `{"version": {"name": "fake"}, "addr_ok": true}` und `0`, kein AssertionError.

- [ ] **Step 3: `vm-setup.sh` – Provisionierung erweitern**

In `provision()` die Paketinstallation und Benutzer ergänzen (Block ersetzen):

```bash
if ! command -v nft >/dev/null || ! command -v curl >/dev/null || ! command -v caddy >/dev/null ||
  ! command -v docker >/dev/null || ! command -v python3 >/dev/null; then
  apt-get update -qq
  apt-get install -y -qq nftables curl caddy docker.io python3
fi
systemctl enable --quiet --now docker
if ! id mcpanel >/dev/null 2>&1; then
  useradd --system --user-group --home-dir /var/lib/mc-panel --no-create-home --shell /usr/sbin/nologin mcpanel
fi
# Minecraft containers run as this user (Docker User uid:gid); it owns /srv/mc-panel/instances/<id>.
if ! id mcpanel-mc >/dev/null 2>&1; then
  useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin mcpanel-mc
fi
install -d -m 0755 -o root -g root /srv/mc-panel
```

Die Abschlusszeile der Provisionierung erweitern:

```bash
echo "provisioned: $(nft --version), $(docker --version), user mcpanel uid=$(id -u mcpanel), user mcpanel-mc uid=$(id -u mcpanel-mc), client 192.0.2.2 -> 192.0.2.1 ok"
```

- [ ] **Step 4: `vm-setup.sh` – `agent.toml` und Reset**

In `install_and_start()` die `agent.toml` erweitern:

```bash
cat >/etc/mc-panel/agent.toml <<EOF
socket = "/run/mc-panel/agent.sock"
panel_uid = $(id -u mcpanel)
firewall = "nftables"
docker_socket = "/var/run/docker.sock"
instances_dir = "/srv/mc-panel"
mc_user = "mcpanel-mc"
EOF
```

Direkt nach dem Löschen der DB (Abschnitt „Fresh state on every run“) den Instanz-Zustand zurücksetzen – nur Container mit unserem Label, nie andere:

```bash
# Fresh instance state: remove only containers carrying our labels, then their data.
ids="$(docker ps -aq --filter label=mcpanel.instance; docker ps -aq --filter label=mcpanel.router)"
if [ -n "$ids" ]; then
  # shellcheck disable=SC2086 # one container id per word
  docker rm -f $ids >/dev/null
fi
rm -rf /srv/mc-panel/instances /srv/mc-panel/router /srv/mc-panel/deleted
```

Die Abschlussmeldung des Skripts um den Router ergänzen:

```bash
echo "== minecraft: router on 127.0.0.1:25565 (Lima forward); base domain for tests: mc.localhost"
```

- [ ] **Step 5: Instanz-Abnahme schreiben** – `scripts/vm-test-instances.sh`

```bash
#!/usr/bin/env bash
# Instance acceptance over the agent socket (spec TP2 §12, criteria 2, 6, 7, 8 – agent path):
# create a Paper server as root through /run/mc-panel/agent.sock, wait until it answers a
# status ping routed by hostname through mc-router, check the container hardening, crash it
# and wait for Docker's restart, then remove it. Redeploys and resets the VM first.
set -euo pipefail
cd "$(dirname "$0")/.."

VM_NAME="${VM_NAME:-mcpanel}"
VM_TOOL="${VM_TOOL:-lima}"

case "${1:-}" in
  "") ;;
  -h | --help) echo "usage: scripts/vm-test-instances.sh   (env: VM_NAME, VM_TOOL as in vm-setup.sh)"; exit 0 ;;
  *) echo "usage: scripts/vm-test-instances.sh" >&2; exit 2 ;;
esac

vm_root() {
  case "$VM_TOOL" in
    lima) limactl shell --workdir / "$VM_NAME" sudo bash -s ;;
    orb) orb -m "$VM_NAME" -u root bash -s ;;
    *) echo "vm-test-instances: unknown VM_TOOL=$VM_TOOL (lima|orb)" >&2; exit 2 ;;
  esac
}

VM_NAME="$VM_NAME" VM_TOOL="$VM_TOOL" scripts/vm-setup.sh --no-build

# The status ping script lives in the repo on the host; copy it into the VM.
case "$VM_TOOL" in
  lima) limactl copy scripts/mc-status.py "$VM_NAME:/tmp/mc-status.py" ;;
  orb) orb push -m "$VM_NAME" scripts/mc-status.py /tmp/mc-status.py ;;
esac

vm_root <<'GUEST'
set -euo pipefail
sock=/run/mc-panel/agent.sock
agent() { curl -fsS --unix-socket "$sock" -H 'Content-Type: application/json' "$@"; }
state() {
  agent http://agent/v1/instances | python3 -I -c '
import json, sys
d = json.load(sys.stdin)
for i in d["instances"]:
    if i["id"] == 9001:
        print(i["state"], i["operation"], i["started_at"], i["last_error"].replace(" ", "_") or "-")
        break
else:
    print("absent - 0 -")'
}
wait_for() { # wait_for <seconds> <description> <command...>
  local deadline=$((SECONDS + $1)) what=$2
  shift 2
  until "$@"; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "FAIL: timeout waiting for: $what" >&2
      state >&2 || true
      docker logs --tail 50 mcpanel-mc-9001 >&2 || true
      exit 1
    fi
    sleep 5
  done
}
is_running() { [ "$(state | cut -d' ' -f1-2)" = "running " ]; }

echo "== ensure vmtest (Paper 1.21.11, 2048 MiB)"
agent -X POST http://agent/v1/instances/ensure -d '{
  "id": 9001, "name": "vmtest", "fqdn": "vmtest.mc.localhost", "revision": 1, "recreate": false,
  "spec": {"type": "paper", "version": "1.21.11", "loader_version": null, "modpack": null,
           "java": "java21", "memory_mib": 2048, "properties": {"motd": "vm acceptance"},
           "curseforge_api_key": null}}'
agent -X POST http://agent/v1/instances/power -d '{"id": 9001, "action": "start"}'
wait_for 900 "container running" is_running
echo "OK: container running"

grep -q '"vmtest.mc.localhost"' /srv/mc-panel/router/routes.json
grep -q '"mcpanel-mc-9001:25565"' /srv/mc-panel/router/routes.json
echo "OK: route present"
GUEST

vm_root <<'GUEST'
set -euo pipefail
sock=/run/mc-panel/agent.sock
agent() { curl -fsS --unix-socket "$sock" -H 'Content-Type: application/json' "$@"; }
started_at() {
  agent http://agent/v1/instances | python3 -I -c '
import json, sys
print(next((i["started_at"] for i in json.load(sys.stdin)["instances"] if i["id"] == 9001), 0))'
}
wait_for() {
  local deadline=$((SECONDS + $1)) what=$2
  shift 2
  until "$@"; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "FAIL: timeout waiting for: $what" >&2
      docker logs --tail 50 mcpanel-mc-9001 >&2 || true
      exit 1
    fi
    sleep 5
  done
}
routed() { python3 -I /tmp/mc-status.py 127.0.0.1 25565 vmtest.mc.localhost >/tmp/mc-status.out 2>/dev/null; }

wait_for 600 "status ping through mc-router" routed
grep -q 'vm acceptance' /tmp/mc-status.out
echo "OK: status ping via vmtest.mc.localhost: $(head -c 200 /tmp/mc-status.out)"

echo "== hardening (docker inspect)"
uid="$(id -u mcpanel-mc)"
gid="$(id -g mcpanel-mc)"
docker inspect mcpanel-mc-9001 | python3 -I -c '
import json, sys
c = json.load(sys.stdin)[0]
h, cfg = c["HostConfig"], c["Config"]
want_user = sys.argv[1] + ":" + sys.argv[2]
checks = {
    "user": cfg["User"] == want_user,
    "cap_drop_all": h["CapDrop"] == ["ALL"],
    "no_cap_add": not h.get("CapAdd"),
    "no_new_privileges": h["SecurityOpt"] == ["no-new-privileges:true"],
    "not_privileged": h["Privileged"] is False,
    "memory": h["Memory"] == 2048 * 1024 * 1024 * 5 // 4 and h["MemorySwap"] == h["Memory"],
    "pids": h["PidsLimit"] == 1024,
    "restart": h["RestartPolicy"]["Name"] == "unless-stopped",
    "only_data_mount": [m["Destination"] for m in c["Mounts"]] == ["/data"],
    "no_ports": not h.get("PortBindings"),
    "network": list(c["NetworkSettings"]["Networks"]) == ["mcpanel-mc"],
    "label": cfg["Labels"].get("mcpanel.instance") == "9001",
}
bad = [k for k, ok in checks.items() if not ok]
if bad:
    print("FAIL: hardening:", bad, file=sys.stderr)
    sys.exit(1)
print("OK: hardening", sorted(checks))' "$uid" "$gid"
stat -c '%U:%G %a' /srv/mc-panel/instances/9001 | grep -qx 'mcpanel-mc:mcpanel-mc 750'
echo "OK: data dir owned by mcpanel-mc (0750)"
if sudo -u mcpanel test -r /srv/mc-panel/instances/9001; then
  echo "FAIL: panel user can read instance data" >&2
  exit 1
fi
if sudo -u mcpanel test -w /var/run/docker.sock; then
  echo "FAIL: panel user can use docker.sock" >&2
  exit 1
fi
echo "OK: panel user has no access to instance data or docker.sock"

echo "== crash: stop the server from inside, Docker must restart it (unless-stopped)"
before="$(started_at)"
docker exec mcpanel-mc-9001 rcon-cli stop >/dev/null
restarted() { a="$(started_at)"; [ "$a" != "0" ] && [ "$a" != "$before" ]; }
wait_for 180 "automatic restart" restarted
echo "OK: restarted (started_at $before -> $(started_at))"

echo "== remove"
agent -X POST http://agent/v1/instances/remove -d '{"id": 9001}'
# The instance leaves GET /v1/instances only after the whole remove (container, routes,
# data move) has finished; checking docker inspect alone would race the routes rewrite.
gone() {
  ! docker inspect mcpanel-mc-9001 >/dev/null 2>&1 &&
    agent http://agent/v1/instances | python3 -I -c '
import json, sys
sys.exit(any(i["id"] == 9001 for i in json.load(sys.stdin)["instances"]))'
}
wait_for 180 "remove finished" gone
if grep -q 'vmtest.mc.localhost' /srv/mc-panel/router/routes.json; then
  echo "FAIL: route still present after remove" >&2
  exit 1
fi
ls -d /srv/mc-panel/deleted/vmtest-* >/dev/null
echo "OK: container and route removed, data moved to $(ls -d /srv/mc-panel/deleted/vmtest-*)"
echo "PASS: instance acceptance (agent path)"
GUEST
```

Ausführbar machen: `chmod +x scripts/vm-test-instances.sh`.

Hinweis: Jeder `vm_root`-Block ist eine eigene Shell; Hilfsfunktionen werden deshalb pro Block definiert. Block 1 legt den Server an und wartet auf „running“, Block 2 prüft Routing, Härtung, Absturz und Löschen.

- [ ] **Step 6: shellcheck**

Run: `shellcheck scripts/vm-setup.sh scripts/vm-test.sh scripts/vm-test-instances.sh`
Expected: keine Befunde.

- [ ] **Step 7: In der VM ausführen** (erfordert die Aufgaben 4–16; Nutzer vorher fragen, falls die VM noch nicht existiert)

Run:
```bash
make release
scripts/vm-setup.sh --no-build
scripts/vm-test.sh
scripts/vm-test-instances.sh
```
Expected:
- `vm-setup.sh` meldet zusätzlich `Docker version 29.…` und `user mcpanel-mc uid=…`.
- `vm-test.sh` endet weiter mit `PASS: acceptance check 5 (nftables part)`.
- `vm-test-instances.sh` zeigt `OK: container running`, `OK: route present`, `OK: status ping via vmtest.mc.localhost: {"version": …"description": …vm acceptance…}`, `OK: hardening [...]`, `OK: data dir owned by mcpanel-mc (0750)`, `OK: panel user has no access …`, `OK: restarted …`, `OK: container and route removed …` und endet mit `PASS: instance acceptance (agent path)`.

Schlägt `hardening` fehl, die Ausgabe nennt den Schlüssel; dann die Vorlage (Aufgabe 6) prüfen, nicht den Test aufweichen.

- [ ] **Step 8: Vom Mac aus über die Lima-Weiterleitung**

Run (während ein Server läuft, z. B. vor dem `remove`-Schritt oder nach Anlegen über das UI):
```bash
python3 -I scripts/mc-status.py 127.0.0.1 25565 vmtest.mc.localhost
```
Expected: dasselbe Status-JSON wie in der VM.

- [ ] **Step 9: Commit**

```bash
git add scripts/vm-setup.sh scripts/mc-status.py scripts/vm-test-instances.sh
git commit -m "chore(vm): docker, mcpanel-mc and an instance acceptance test

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 24: Entwickler-Doku und CLAUDE.md

**Files:**
- Modify: `docs/dev/linux-vm.md`
- Modify: `docs/dev/README.md`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: Ergebnisse der Aufgaben 4–23 (Konfigurationsschlüssel, Pakete, Skripte, Routen).
- Produces: Doku; keine Code-Schnittstellen.

- [ ] **Step 1: `docs/dev/linux-vm.md` ergänzen**

Im Diagramm unter „Was die Skripte aufbauen“ ergänzen:

```
                              docker.service, Benutzer mcpanel-mc, /srv/mc-panel (instances/, router/, deleted/)
                              mcpanel-router (mc-router) auf 0.0.0.0:25565 ──Lima──▶ 127.0.0.1:25565 auf dem Mac
scripts/vm-test-instances.sh ▶ agent-Socket als root: Paper-Server vmtest, Status-Ping über vmtest.mc.localhost
```

Neuer Abschnitt nach „Im Browser ansehen“:

```markdown
## Minecraft-Server in der VM

`vm-setup.sh` installiert Docker (`docker.io`), legt den Benutzer `mcpanel-mc` an (die Container laufen als dessen UID/GID) und setzt `instances_dir = "/srv/mc-panel"` in `agent.toml`. Jeder Lauf entfernt alle Container mit Label `mcpanel.instance` bzw. `mcpanel.router` und löscht `/srv/mc-panel/{instances,router,deleted}`; andere Container bleiben unberührt.

Automatischer Test ohne UI (spricht den agent als root über seinen Socket an):

    scripts/vm-test-instances.sh

Er endet mit `PASS: instance acceptance (agent path)` und prüft Routing über mc-router, die Container-Härtung, den automatischen Neustart und das Löschen. Der erste Lauf lädt das itzg-Image (~500 MB).

Im Browser (`https://mcpanel.localhost:8443`, Admin wie oben anlegen):

1. Einstellungen → Minecraft-Server → Basisdomain `mc.localhost`.
2. Server anlegen, z. B. `survival`, Paper.
3. Auf dem Mac `/etc/hosts` um `127.0.0.1 survival.mc.localhost` ergänzen (der Minecraft-Client nutzt den macOS-Resolver, der `*.localhost` nur zu `::1` auflöst; die Lima-Weiterleitung lauscht nur auf IPv4).
4. Im Minecraft-Client (Java Edition, passende Version) den Server `survival.mc.localhost` hinzufügen.

Prüfen ohne Client: `python3 -I scripts/mc-status.py 127.0.0.1 25565 survival.mc.localhost`.
```

In „Nützliche Befehle“ ergänzen:

```bash
limactl shell mcpanel sudo docker ps --filter label=mcpanel.instance
limactl shell mcpanel sudo cat /srv/mc-panel/router/routes.json
limactl shell mcpanel sudo docker logs -f mcpanel-mc-<id>
limactl shell mcpanel sudo curl -s --unix-socket /run/mc-panel/agent.sock http://agent/v1/instances
```

In „Fehlersuche“ ergänzen:

```markdown
| `docker: unavailable` in den Einstellungen | `limactl shell mcpanel systemctl status docker`; agent-Log (`journalctl -u mcpanel-agent`) |
| Server bleibt bei „wird erstellt“ | Image-Pull läuft oder scheitert: `last_error` auf der Detailseite, `sudo docker images` |
| Status-Ping scheitert, Container läuft | `routes.json` prüfen; mc-router-Log: `sudo docker logs mcpanel-router` |
| Minecraft-Client findet `<name>.mc.localhost` nicht | `/etc/hosts`-Eintrag auf dem Mac fehlt (siehe oben) |
```

- [ ] **Step 2: `docs/dev/README.md` ergänzen**

Neuer Abschnitt nach „Erster Start“:

```markdown
## Minecraft-Server lokal (`make dev`)

Der dev-agent nutzt den Docker-Socket des Macs, falls vorhanden (in dieser Reihenfolge `~/.orbstack/run/docker.sock`, `~/.docker/run/docker.sock`, `/var/run/docker.sock`). Ohne Docker zeigt das panel „Docker nicht verfügbar“, alles andere funktioniert weiter.

- Instanzdaten liegen unter `.dev/mc/` (`instances/`, `router/`, `deleted/`); `make clean` löscht sie mit.
- Die Container laufen als dein Benutzer (im Dev-Modus ist `mc_user` leer).
- Basisdomain in den Einstellungen: `mc.localhost`; für den Minecraft-Client `/etc/hosts` um `127.0.0.1 <name>.mc.localhost` ergänzen.
- mc-router belegt Port 25565 auf dem Mac; ein lokal laufender anderer Minecraft-Server kollidiert damit.
- Container aufräumen: `docker rm -f $(docker ps -aq --filter label=mcpanel.instance) mcpanel-router`.
```

In der Make-Targets- bzw. CI-Tabelle (Abschnitt „CI lokal nachstellen“) die Zeile ergänzen:

```markdown
| Docker-Integration (Linux) | in der VM bzw. CI: `go test -tags dockerintegration -exec sudo -count=1 ./internal/agent/...` |
```

Im Abschnitt „Produktion“ ergänzen: „Der agent braucht Docker (Engine ≥ 25, API ≥ 1.44) und den Benutzer `mcpanel-mc`; beides richtet später der Installer (Teilprojekt 6) ein.“

- [ ] **Step 3: `CLAUDE.md` aktualisieren**

Im Abschnitt „Architecture“ den agent-Absatz ersetzen durch:

```markdown
- **agent** runs as root and exposes a narrow JSON API over a Unix socket (`internal/agent`). It accepts connections only from the configured panel UID, checked via peer credentials. It validates every request strictly, rejecting unknown fields, and checks ban prefixes in `agentapi`. It manages the nftables sets `mcpanel_jail4`/`mcpanel_jail6` in table `inet mcpanel` and is the only Docker client: Minecraft servers are containers built from a fixed, hardened template (`internal/agent/instances`), validated with `internal/agent/instancespec` (shared with the panel), and routed by hostname through the `mcpanel-router` container (mc-router, reloaded via SIGHUP). The design goal is that a compromised panel does not give root on the host. If the agent fails, the panel reports `503 agent_unavailable`; in-app jails still apply.
```

Neuer Unterabschnitt nach „Data“:

```markdown
### Minecraft instances

- The panel stores the desired state per server (`instances` table: type, version, java, memory, allowlisted properties, `desired_state`, `revision`) and `internal/servers` reconciles it with the agent every 10 s and after each user action. Spec changes bump `revision`; a running container with an older `mcpanel.revision` label shows "restart required".
- Server name = subdomain (`<name>.<base_domain>`, base domain in `instance_settings`). Container `mcpanel-mc-<id>`, data in `<instances_dir>/instances/<id>`.
- Only `instancespec.Env` decides container environment variables; never add itzg variables elsewhere.
- Version catalogs (`internal/servers/catalog`) fetch fixed upstream URLs with a contact User-Agent and a 1 h cache.
```

Im Abschnitt „Conventions“ die Zeile zum String `mcpanel` ersetzen durch:

```markdown
- The string `mcpanel` (nftables table and sets, system users `mcpanel`/`mcpanel-mc`, Docker names `mcpanel-mc-<id>`/`mcpanel-router`/network `mcpanel-mc`, labels `mcpanel.*`, `mcpanel.theme` localStorage key, VM name) is a runtime name, not the module path. Do not rename it.
```

Im Abschnitt „Commands“ ergänzen:

```bash
scripts/vm-test-instances.sh                          # instance acceptance in the Lima VM (agent path)
go test -tags dockerintegration -exec sudo ./internal/agent/...   # Linux with Docker only
```

- [ ] **Step 4: Prüfen**

Run: `make lint && make test`
Expected: grün (reine Doku-Änderung; prüft, dass nichts anderes versehentlich geändert wurde).

- [ ] **Step 5: Commit**

```bash
git add docs/dev/linux-vm.md docs/dev/README.md CLAUDE.md
git commit -m "docs: document minecraft instances for VM, local dev and CLAUDE.md

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 25: Abnahme, Reviews, Abschluss

**Files:** nach Befund

**Interfaces:**
- Consumes: alle vorherigen Aufgaben.
- Produces: abgehakte Abnahmekriterien, behobene Review-Befunde.

- [ ] **Step 1: Gesamtlauf lokal**

Run:
```bash
make generate && git diff --exit-code
make test && make lint
(cd web && npm run build && npm run e2e)
```
Expected: kein Diff nach `make generate` (generierter Code ist aktuell), alle Tests und Linter grün, Playwright (Mock) grün inkl. `servers.spec.ts`.

- [ ] **Step 2: Docker-Integrationstest in der VM** (Go ist in der VM nicht installiert; Testbinary auf dem Mac bauen)

Run:
```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -tags dockerintegration -o .dev/bin/agent-docker.test ./internal/agent/docker
limactl copy .dev/bin/agent-docker.test mcpanel:/tmp/agent-docker.test
limactl shell mcpanel sudo /tmp/agent-docker.test -test.v -test.count=1
```
Expected: `PASS`. Läuft der echte agent in der VM (Router und Netz `mcpanel-mc` existieren), überspringt `TestIntegrationManagerCycle` sich absichtlich, um den Host nicht zu verändern; die Client-Tests laufen trotzdem. Vollständig und strikt (`MCPANEL_IT_STRICT=1`) läuft der Test im Linux-Job der CI.

- [ ] **Step 3: VM-Abnahme**

Run:
```bash
make release
scripts/vm-setup.sh --no-build
scripts/vm-test.sh
scripts/vm-test-instances.sh
```
Expected: beide Tests enden mit `PASS`.

- [ ] **Step 4: Abnahmekriterien im Browser** (`https://mcpanel.localhost:8443`, Admin mit TOTP, Basisdomain `mc.localhost`)

| # | Kriterium (Spec §12) | Vorgehen | Erwartet |
|---|---|---|---|
| 1 | Tests und Lint grün | Step 1, Step 2, CI des PR | grün |
| 2 | Paper-Server, Client verbindet sich | Server `survival` (Paper) anlegen; `/etc/hosts` auf dem Mac; Minecraft-Client verbindet sich mit `survival.mc.localhost` (falls kein Client verfügbar: `python3 -I scripts/mc-status.py 127.0.0.1 25565 survival.mc.localhost` + Nutzer bitten, den Client-Test selbst zu machen) | Status „läuft“, Client betritt die Welt |
| 3 | Vanilla, Fabric, NeoForge, Modrinth-Pack starten | je einen Server anlegen (Modrinth z. B. `fabric-boosted` oder ein anderes kleines Pack), je 4096 MiB; nacheinander, um RAM zu sparen (VM hat 4 GiB: jeweils den vorherigen stoppen) | jeder erreicht „läuft“; Status-Ping je Hostname antwortet |
| 4 | Rechte | Benutzer `friend` anlegen, `server.power` auf `survival`; als `friend` anmelden | sieht nur `survival`, kann starten/stoppen, kein Tab „Einstellungen“; `PATCH /api/servers/<id>` → 403; anderer Server → 403 |
| 5 | Änderung + Neustart | MOTD und RAM von `survival` ändern | „Neustart nötig“; nach „Jetzt neu starten“ zeigt der Status-Ping die neue MOTD, `docker inspect` das neue Limit, die Welt (z. B. ein gesetzter Block / `level.dat`-Zeitstempel unverändert) ist erhalten |
| 6 | Absturz + VM-Neustart | `docker exec mcpanel-mc-<id> rcon-cli stop` → läuft nach < 3 min wieder; `limactl stop mcpanel && limactl start mcpanel && scripts/vm-setup.sh --no-build` → **Achtung:** `vm-setup.sh` setzt den Zustand zurück; für diesen Test stattdessen nach `limactl start` nur prüfen: `limactl shell mcpanel sudo docker ps --filter label=mcpanel.instance` | Container mit `desired_state=running` laufen nach dem VM-Start wieder (Docker `unless-stopped`) |
| 7 | Härtung | in `vm-test-instances.sh` geprüft | `OK: hardening`, `OK: panel user has no access …` |
| 8 | Löschen + Audit | `survival` löschen; Audit-Log ansehen; `sudo -u mcpanel /usr/local/bin/panel audit verify -config /etc/mc-panel/panel.toml` | Container und Route weg, Daten unter `/srv/mc-panel/deleted/survival-*`; Audit enthält `server.create`, `server.update`, `server.power`, `server.delete`, `server.settings_update`; `verify` meldet OK |

Jede Abweichung ist ein Befund: mit Test + Fix als eigenen Commit beheben (`superpowers:systematic-debugging`), dann das Kriterium erneut prüfen.

- [ ] **Step 5: Security-Review** – Skill `security-review` auf dem Branch `feat/instances` gegen `main`. Schwerpunkte: Env-Injektion, Pfade (`instances/<id>`, `deleted/<name>-…` – keine Traversal-Möglichkeit über `name`), fremde Container, Secrets in Logs/Audit/`last_error`, SSRF über Kataloge (feste URLs), Rechte der neuen Routen. Jeden Befund mit `superpowers:receiving-code-review` bewerten; berechtigte mit Test + Fix als eigenen Commit beheben.

- [ ] **Step 6: Code-Review** – Skill `superpowers:requesting-code-review` für den Gesamtstand; Befunde wie in Step 5.

- [ ] **Step 7: Pläne abhaken** – In `docs/superpowers/plans/2026-10-10-instances-0*.md` alle erledigten Checkboxen auf `- [x]` setzen und committen:

```bash
git add docs/superpowers/plans/2026-10-10-instances-0*.md
git commit -m "docs(plan): tick off sub-project 2 tasks

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 8: Abschluss** – Skill `superpowers:finishing-a-development-branch`. Danach mit dem Nutzer klären, ob ein Release getaggt werden soll (ein Tag löst den Release-Workflow aus → nur nach ausdrücklicher Zustimmung).

## Self-Review (dieser Datei)

- Spec §10 (VM: Docker, `mcpanel-mc`, Reset, Port 25565, `/etc/hosts`-Hinweis) → Aufgabe 23 Steps 3–5, Aufgabe 24 Step 1.
- Spec §3.4 (Dev-Modus mit Docker-Socket des Macs) → Konfiguration in Aufgabe 4, Doku in Aufgabe 24 Step 2.
- Spec §12 Kriterien 1–8 → Aufgabe 25 Steps 1–4 (Tabelle), automatisiert soweit ohne Minecraft-Client möglich (Aufgabe 23).
- Der Reset in `vm-setup.sh` entfernt nur Container mit `mcpanel.instance`/`mcpanel.router`-Label (Review Focus #2 gilt auch für Skripte).
- `vm-test-instances.sh` nutzt ID 9001, damit es nie mit vom UI angelegten Servern (kleine IDs) kollidiert; der vorgeschaltete Reset räumt ohnehin auf.
