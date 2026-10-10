# Teilprojekt 2 – CI, Doku und Abnahme (Aufgaben 23–25) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Den Instanz-Lebenszyklus automatisiert auf einem CI-Runner mit echtem Docker prüfen (Routing über mc-router, Härtung, Absturz-Neustart, Löschen), die Entwickler-Doku nachziehen und Teilprojekt 2 abschließen: Reviews in dieser Session, Funktionsabnahme manuell durch den Nutzer auf einem eigenen Testserver.

**Architecture:** `scripts/ci-instances.sh` läuft im neuen CI-Job `instances` als root auf einem Wegwerf-Runner (Ubuntu, Docker vorinstalliert): es startet den echten agent im Prod-Modus, spricht ihn als root über seinen Socket an (erlaubte Peers: panel-UID und 0), legt einen Paper-Server an und prüft ihn mit einem Minecraft-Status-Ping (`scripts/mc-status.py`), der wie ein Client die Server-Adresse mitschickt, nach der mc-router routet. Auf dem Mac läuft davon nichts (CLAUDE.md: nur `make check`, nichts Systemnahes).

**Tech Stack:** Bash (shellcheck-sauber), Python 3 (nur Standardbibliothek), GitHub Actions (`ubuntu-24.04`, Docker vorinstalliert).

**Spec:** `docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md` (§3.4, §10, §11, §12)

## Global Constraints

Es gelten alle Global Constraints aus `2026-10-10-instances-00-overview.md` und die Arbeitsregeln aus `CLAUDE.md`. Zusätzlich:

| Thema | Wert |
|---|---|
| Wo getestet wird | Lokal nur `make check` und `shellcheck`; alles mit root, Docker oder Minecraft ausschließlich im CI-Job `instances` |
| Test-Server | ID `9001`, Name `citest`, FQDN `citest.mc.localhost`, Paper `1.21.11`, java21, 2048 MiB |
| Benutzer auf dem Runner | `mcpanel` (nur für `panel_uid`), `mcpanel-mc` (Container-UID/GID) |
| Zeitlimits | Image-Pull + Start ≤ 15 min, Neustart nach Absturz ≤ 3 min, Job-Timeout 30 min |
| Router-Port | `25565/tcp` auf dem Runner |

---

### Task 23: CI-Abnahme der Instanzen

**Files:**
- Create: `scripts/mc-status.py` (Minecraft-Status-Ping mit frei wählbarer Server-Adresse)
- Create: `scripts/ci-instances.sh` (Abnahme über den agent-Socket, nur CI)
- Modify: `.github/workflows/ci.yml` (neuer Job `instances`)

**Interfaces:**
- Consumes: agent-Endpunkte aus Aufgabe 9 (`GET /v1/instances`, `POST /v1/instances/ensure|power|remove`), Konfigurationsschlüssel aus Aufgabe 4 (`instances_dir`, `mc_user`), Container-/Label-Namen aus der Übersicht.
- Produces: `scripts/mc-status.py HOST PORT SERVER_ADDRESS` (Exit 0 + JSON bei Antwort, sonst Exit 1); `scripts/ci-instances.sh AGENT_BINARY` endet mit `PASS: instance acceptance (agent path)`.

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

- [ ] **Step 2: Status-Ping gegen einen lokalen Fake prüfen** (reines Python ohne Netzwerk nach außen, keine Container – auf dem Mac erlaubt)

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

- [ ] **Step 3: Abnahmeskript schreiben** – `scripts/ci-instances.sh`

```bash
#!/usr/bin/env bash
# Instance acceptance for a Linux CI runner with Docker (sub-project 2, spec §12 criteria 2,
# 6, 7, 8 on the agent path): starts the real agent as root, creates a Paper server through
# its socket, waits until it answers a status ping routed by hostname through mc-router,
# checks the container hardening, crashes it, waits for Docker's restart and removes it.
#
# Usage (as root): scripts/ci-instances.sh AGENT_BINARY
# Changes the host (users, Docker containers/network, port 25565): disposable CI runners only.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ "$#" -ne 1 ]; then
  echo "usage: scripts/ci-instances.sh AGENT_BINARY" >&2
  exit 2
fi
if [ "$(id -u)" -ne 0 ] || [ "$(uname -s)" != Linux ]; then
  echo "ci-instances: Linux and root only" >&2
  exit 1
fi

work="$(mktemp -d)"
chmod 0755 "$work"
sock="$work/run/agent.sock"
agent_pid=""

cleanup() {
  if [ -n "$agent_pid" ]; then
    kill "$agent_pid" 2>/dev/null || true
    wait "$agent_pid" 2>/dev/null || true
  fi
  ids="$(docker ps -aq --filter label=mcpanel.instance; docker ps -aq --filter label=mcpanel.router)"
  if [ -n "$ids" ]; then
    # shellcheck disable=SC2086 # one container id per word
    docker rm -f $ids >/dev/null 2>&1 || true
  fi
  docker network rm mcpanel-mc >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  echo "--- agent log" >&2
  cat "$work/agent.log" >&2 || true
  docker logs --tail 80 mcpanel-mc-9001 >&2 2>/dev/null || true
  docker logs --tail 40 mcpanel-router >&2 2>/dev/null || true
  exit 1
}

agent() { curl -fsS --unix-socket "$sock" -H 'Content-Type: application/json' "$@"; }

# status_field <python expression on instance dict i>; prints "absent" without instance 9001.
status_field() {
  agent http://agent/v1/instances | python3 -I -c '
import json, sys
expr = sys.argv[1]
for i in json.load(sys.stdin)["instances"]:
    if i["id"] == 9001:
        print(eval(expr, {}, {"i": i}))
        break
else:
    print("absent")' "$1"
}

wait_for() { # wait_for <seconds> <description> <command...>
  local deadline=$((SECONDS + $1)) what=$2
  shift 2
  until "$@"; do
    [ "$SECONDS" -lt "$deadline" ] || fail "timeout waiting for: $what (status: $(status_field i || true))"
    sleep 5
  done
}

echo "== setup: users, config, agent"
for u in mcpanel mcpanel-mc; do
  if ! id "$u" >/dev/null 2>&1; then
    useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin "$u"
  fi
done
install -d -m 0755 "$work/bin"
install -m 0755 "$1" "$work/bin/agent"
install -d -m 0750 -o root -g mcpanel "$work/run"
cat >"$work/agent.toml" <<EOF
socket = "$sock"
panel_uid = $(id -u mcpanel)
firewall = "noop"
docker_socket = "/var/run/docker.sock"
instances_dir = "$work/mc"
mc_user = "mcpanel-mc"
EOF
"$work/bin/agent" -config "$work/agent.toml" >"$work/agent.log" 2>&1 &
agent_pid=$!
for _ in $(seq 1 50); do [ -S "$sock" ] && break; sleep 0.2; done
[ -S "$sock" ] || fail "agent socket missing"
agent http://agent/v1/health | grep -q '"docker":"ok"' || fail "agent reports docker unavailable"

echo "== ensure citest (Paper 1.21.11, 2048 MiB) and start"
agent -X POST http://agent/v1/instances/ensure -d '{
  "id": 9001, "name": "citest", "fqdn": "citest.mc.localhost", "revision": 1, "recreate": false,
  "spec": {"type": "paper", "version": "1.21.11", "loader_version": null, "modpack": null,
           "java": "java21", "memory_mib": 2048, "properties": {"motd": "ci acceptance"},
           "curseforge_api_key": null}}'
created() { [ "$(status_field 'i["state"]')" = created ] && [ -z "$(status_field 'i["operation"]')" ]; }
wait_for 900 "container created (image pull)" created
agent -X POST http://agent/v1/instances/power -d '{"id": 9001, "action": "start"}'
running() { [ "$(status_field 'i["state"]')" = running ]; }
wait_for 120 "container running" running
echo "OK: container running"

routes="$work/mc/router/routes.json"
grep -q '"citest.mc.localhost"' "$routes" || fail "route missing in $routes"
grep -q '"mcpanel-mc-9001:25565"' "$routes" || fail "route target missing in $routes"
echo "OK: route present"

routed() { python3 -I scripts/mc-status.py 127.0.0.1 25565 citest.mc.localhost >"$work/status.json" 2>/dev/null; }
wait_for 600 "status ping through mc-router" routed
grep -q 'ci acceptance' "$work/status.json" || fail "status ping answered without our MOTD: $(cat "$work/status.json")"
echo "OK: status ping via citest.mc.localhost: $(head -c 200 "$work/status.json")"

echo "== hardening (docker inspect)"
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
    print("hardening failed:", bad, file=sys.stderr)
    sys.exit(1)
print("OK: hardening", sorted(checks))' "$(id -u mcpanel-mc)" "$(id -g mcpanel-mc)" || fail "hardening"
[ "$(stat -c '%U:%G %a' "$work/mc/instances/9001")" = "mcpanel-mc:mcpanel-mc 750" ] || fail "data dir owner/mode: $(stat -c '%U:%G %a' "$work/mc/instances/9001")"
echo "OK: data dir owned by mcpanel-mc (0750)"
if setpriv --reuid=mcpanel --regid=mcpanel --init-groups test -r "$work/mc/instances/9001"; then
  fail "panel user can read instance data"
fi
if setpriv --reuid=mcpanel --regid=mcpanel --init-groups test -w /var/run/docker.sock; then
  fail "panel user can use docker.sock"
fi
echo "OK: panel user has no access to instance data or docker.sock"

echo "== crash: stop the server from inside, Docker must restart it (unless-stopped)"
before="$(status_field 'i["started_at"]')"
docker exec mcpanel-mc-9001 rcon-cli stop >/dev/null
restarted() { a="$(status_field 'i["started_at"]')"; [ "$a" != 0 ] && [ "$a" != "$before" ] && running; }
wait_for 180 "automatic restart" restarted
echo "OK: restarted (started_at $before -> $(status_field 'i["started_at"]'))"

echo "== remove"
agent -X POST http://agent/v1/instances/remove -d '{"id": 9001}'
# The instance leaves GET /v1/instances only after the whole remove (container, routes,
# data move) has finished; docker inspect alone would race the routes rewrite.
gone() { [ "$(status_field i)" = absent ] && ! docker inspect mcpanel-mc-9001 >/dev/null 2>&1; }
wait_for 180 "remove finished" gone
if grep -q 'citest.mc.localhost' "$routes"; then
  fail "route still present after remove"
fi
ls -d "$work"/mc/deleted/citest-* >/dev/null 2>&1 || fail "data not moved to deleted/"
echo "OK: container and route removed, data moved to deleted/"
echo "PASS: instance acceptance (agent path)"
```

Ausführbar machen: `chmod +x scripts/ci-instances.sh`.

Hinweise: Der Test erstellt mit `ensure` nur den Container (Aufgabe 8: neuer Container wird nicht automatisch gestartet), startet ihn mit `power start` und nutzt `firewall = "noop"`, weil hier nur der Instanz-Pfad geprüft wird (nftables prüft `scripts/ci-acceptance.sh`). `status_field` wertet einen festen Python-Ausdruck aus dem Skript selbst aus, nie Eingaben von außen.

- [ ] **Step 4: CI-Job** – in `.github/workflows/ci.yml` nach dem Job `nftables` einfügen (Action-Versionen wie in den übrigen Jobs, per SHA gepinnt – aus dem Job `nftables` kopieren):

```yaml
  instances:
    name: Instance acceptance (Docker, root)
    runs-on: ubuntu-24.04
    timeout-minutes: 30
    steps:
      - name: Checkout
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false

      - name: Set up Go
        uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache: true

      - name: Build agent
        run: go build -o bin/agent ./cmd/agent

      # Real agent + Docker + itzg Paper server: routing by hostname, hardening,
      # automatic restart after a crash, removal.
      - name: Instance acceptance
        run: sudo scripts/ci-instances.sh bin/agent
```

- [ ] **Step 5: Lokal prüfen (nur statisch)**

Run: `shellcheck scripts/ci-instances.sh scripts/ci-acceptance.sh && make check`
Expected: keine Befunde, `make check` grün.

- [ ] **Step 6: Commit, Push, CI abwarten**

```bash
git add scripts/mc-status.py scripts/ci-instances.sh .github/workflows/ci.yml
git commit -m "ci: instance acceptance with the real agent and Docker

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks <pr> --watch
gh run view <run-id> --log | grep -E '^.*(OK:|PASS:|FAIL:)'
```
Expected: Job `instances` grün; im Log `OK: container running`, `OK: route present`, `OK: status ping via citest.mc.localhost: …ci acceptance…`, `OK: hardening […]`, `OK: data dir owned by mcpanel-mc (0750)`, `OK: panel user has no access …`, `OK: restarted …`, `OK: container and route removed …`, `PASS: instance acceptance (agent path)`. Schlägt `hardening` fehl, nennt die Ausgabe den Schlüssel; dann die Vorlage (Aufgabe 6) korrigieren, nicht den Test aufweichen.

---

### Task 24: Entwickler-Doku und CLAUDE.md

**Files:**
- Modify: `docs/dev/README.md`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: Ergebnisse der Aufgaben 4–23.
- Produces: Doku; keine Code-Schnittstellen.

- [ ] **Step 1: `docs/dev/README.md` ergänzen**

Neuer Abschnitt nach „UI-Vorschau in Docker“:

```markdown
## Minecraft-Server lokal (optional)

`make dev` startet den agent ohne Docker: Server lassen sich anlegen und konfigurieren, der Status bleibt „Docker nicht verfügbar“, und auf dem Mac entstehen keine Container. Wer lokal echte Minecraft-Container sehen will, startet bewusst:

    MCPANEL_DEV_DOCKER=1 make dev

- Der dev-agent nutzt dann den Docker-Socket von OrbStack, Docker Desktop oder `/var/run/docker.sock` (in dieser Reihenfolge), legt das Netz `mcpanel-mc` und den Container `mcpanel-router` (Port 25565) an und schreibt Instanzdaten nach `.dev/mc/`.
- Basisdomain in den Einstellungen: `mc.localhost`; für den Minecraft-Client `/etc/hosts` um `127.0.0.1 <name>.mc.localhost` ergänzen.
- Aufräumen: `docker rm -f $(docker ps -aq --filter label=mcpanel.instance) mcpanel-router; docker network rm mcpanel-mc`.

Funktionstests gegen echte Server (Clients, Modpacks, Neustarts) laufen getrennt auf einem Testserver; die Checkliste steht in `docs/dev/acceptance-tp2.md`.
```

In der Tabelle „CI lokal nachstellen“ zwei Zeilen ergänzen:

```markdown
| `nftables` (Docker-Integration) | nur in der CI: `go test -tags dockerintegration -exec "sudo env MCPANEL_IT_STRICT=1" ./internal/agent/...` |
| `instances` | nur in der CI: `sudo scripts/ci-instances.sh bin/agent` (echter agent + Docker + Paper-Server; verändert den Host) |
```

Im Abschnitt „Produktion“ ergänzen: „Der agent braucht Docker (Engine ≥ 25, API ≥ 1.44) und den Benutzer `mcpanel-mc`; beides richtet später der Installer (Teilprojekt 6) ein.“

- [ ] **Step 2: `CLAUDE.md` aktualisieren**

Im Abschnitt „Architecture“ den agent-Absatz ersetzen durch:

```markdown
- **agent** runs as root and exposes a narrow JSON API over a Unix socket (`internal/agent`). It accepts connections only from the configured panel UID, checked via peer credentials. It validates every request strictly, rejecting unknown fields, and checks ban prefixes in `agentapi`. It manages the nftables sets `mcpanel_jail4`/`mcpanel_jail6` in table `inet mcpanel` and is the only Docker client: Minecraft servers are containers built from a fixed, hardened template (`internal/agent/instances`), validated with `internal/agent/instancespec` (shared with the panel), and routed by hostname through the `mcpanel-router` container (mc-router, reloaded via SIGHUP). In dev mode Docker is off unless `MCPANEL_DEV_DOCKER=1`. The design goal is that a compromised panel does not give root on the host. If the agent fails, the panel reports `503 agent_unavailable`; in-app jails still apply.
```

Neuer Unterabschnitt nach „Data“:

```markdown
### Minecraft instances

- The panel stores the desired state per server (`instances` table: type, version, java, memory, allowlisted properties, `desired_state`, `revision`) and `internal/servers` reconciles it with the agent every 10 s and after each user action. Spec changes bump `revision`; a running container with an older `mcpanel.revision` label shows "restart required".
- Server name = subdomain (`<name>.<base_domain>`, base domain in `instance_settings`). Container `mcpanel-mc-<id>`, data in `<instances_dir>/instances/<id>`.
- Only `instancespec.Env` decides container environment variables; never add itzg variables elsewhere.
- Version catalogs (`internal/servers/catalog`) fetch fixed upstream URLs with a contact User-Agent and a 1 h cache.
- Docker integration and the instance acceptance (`scripts/ci-instances.sh`) run only in CI.
```

Im Abschnitt „Conventions“ die Zeile zum String `mcpanel` ersetzen durch:

```markdown
- The string `mcpanel` (nftables table and sets, system users `mcpanel`/`mcpanel-mc`, Docker names `mcpanel-mc-<id>`/`mcpanel-router`/network `mcpanel-mc`, labels `mcpanel.*`, `mcpanel.theme` localStorage key) is a runtime name, not the module path. Do not rename it.
```

- [ ] **Step 3: Prüfen und committen**

Run: `make check`
Expected: grün.

```bash
git add docs/dev/README.md CLAUDE.md
git commit -m "docs: document minecraft instances for local dev, CI and CLAUDE.md

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 25: Abnahme-Checkliste, Reviews, Abschluss

**Files:**
- Create: `docs/dev/acceptance-tp2.md` (Checkliste für die manuelle Funktionsabnahme durch den Nutzer)
- Sonst: nach Befund

**Interfaces:**
- Consumes: alle vorherigen Aufgaben.
- Produces: grüne CI auf dem Gesamtstand, behobene Review-Befunde, Checkliste für den Nutzer.

- [ ] **Step 1: Checkliste schreiben** – `docs/dev/acceptance-tp2.md`

```markdown
# Abnahme Teilprojekt 2 (manuell, Testserver)

Automatisch geprüft (CI): Unit- und Integrationstests, Docker-Integration (`dockerintegration`), Instanz-Abnahme über den agent (`scripts/ci-instances.sh`: Routing, Härtung, Absturz-Neustart, Löschen), Playwright gegen den Mock.

Manuell auf einem Testserver (Ubuntu 26.04 mit Docker, Release-Archiv aus einem `v*`-Tag oder `make release`, Caddy davor), getrennt von der Entwicklung:

| # | Kriterium (Spec §12) | Vorgehen | Erwartet |
|---|---|---|---|
| 2 | Paper-Server, Client verbindet sich | Basisdomain setzen (Wildcard-DNS `*.<domain>` → Server), Server `survival` (Paper) anlegen, mit einem Minecraft-Client `survival.<domain>` betreten | Status „läuft“, Client betritt die Welt |
| 3 | Weitere Arten | je einen Server Vanilla, Fabric, NeoForge und ein kleines Modrinth-Pack anlegen und starten | jeder erreicht „läuft“ und ist per Client erreichbar |
| 4 | Rechte | Benutzer mit `server.power` auf `survival` anlegen und damit anmelden | sieht nur `survival`, kann starten/stoppen, kein Tab „Einstellungen“, `PATCH /api/servers/<id>` → 403 |
| 5 | Änderung + Neustart | MOTD und RAM ändern | „Neustart nötig“; nach „Jetzt neu starten“ neue MOTD im Client, neues Limit in `docker inspect`, Welt erhalten |
| 6 | Neustart des Hosts | Server laufen lassen, Host neu starten | Server mit Soll „läuft“ laufen danach wieder |
| 8 | Löschen + Audit | Server löschen, Audit-Log ansehen, `sudo -u mcpanel panel audit verify -config /etc/mc-panel/panel.toml` | Container und Route weg, Daten unter `deleted/`, Audit vollständig, `verify` OK |

Befunde als GitHub-Issue anlegen (`gh issue create`), mit Schritten zum Nachstellen.
```

- [ ] **Step 2: Gesamtstand in der CI**

Nach dem Push des letzten Teil-PRs auf `main` (bzw. auf dem Integrations-PR):

```bash
gh pr checks <pr> --watch
```
Expected: alle Jobs grün (`go`, `web`, `openapi`, `nftables` inkl. Docker-Integration, `instances`, `e2e`, `build`); `make generate` ist durch den CI-Check „generated code up to date“ abgedeckt.

- [ ] **Step 3: Security-Review** – Skill `security-review` auf dem Gesamtstand gegen `main` vor TP2. Schwerpunkte: Env-Injektion, Pfade (`instances/<id>`, `deleted/<name>-…` – keine Traversal-Möglichkeit über `name`), fremde Container, Secrets in Logs/Audit/`last_error`, SSRF über Kataloge (feste URLs), Rechte der neuen Routen, dev-Docker nur mit Opt-in. Jeden Befund mit `superpowers:receiving-code-review` bewerten; berechtigte mit Test + Fix als eigenen Commit beheben (Tests laufen in der CI).

- [ ] **Step 4: Code-Review** – Skill `superpowers:requesting-code-review` für den Gesamtstand; Befunde wie in Step 3.

- [ ] **Step 5: Pläne abhaken** – In `docs/superpowers/plans/2026-10-10-instances-0*.md` alle erledigten Checkboxen auf `- [x]` setzen, committen, PR.

- [ ] **Step 6: Übergabe an den Nutzer** – Checkliste `docs/dev/acceptance-tp2.md` nennen; die manuelle Abnahme macht der Nutzer getrennt. Release-Tag nur nach ausdrücklicher Freigabe (ein `v*`-Tag veröffentlicht ein GitHub-Release).

## Self-Review (dieser Datei)

- Spec §10 (Entwicklung/CI) → Aufgabe 23 (CI-Job), Aufgabe 24 (Doku, Docker-Opt-in im Dev-Modus).
- Spec §12: Kriterien 1, 7 und die agent-Seite von 2, 6, 8 automatisiert in der CI (Aufgabe 23, Docker-Integration aus Aufgabe 10); 2–6, 8 mit echtem Client und Host-Neustart manuell durch den Nutzer (Aufgabe 25 Step 1).
- Nichts in dieser Datei startet Container oder braucht root auf dem Mac (CLAUDE.md).
- Das Skript räumt nur Container mit `mcpanel.instance`/`mcpanel.router`-Label und das Netz `mcpanel-mc` ab (Review Focus #2 gilt auch für Skripte).
