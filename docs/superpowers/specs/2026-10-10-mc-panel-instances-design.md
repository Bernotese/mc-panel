# mc-panel – Teilprojekt 2: Instanzen-Lifecycle (Design-Spec)

- **Datum:** 2026-10-10
- **Status:** Entwurf, wartet auf Review
- **Autor:** Jannes (mit Claude)
- **Baut auf:** `2026-10-08-mc-panel-foundation-design.md` (Teilprojekt 1)

## 1. Ziel und Kontext

Teilprojekt 1 hat Fundament und Security geliefert; Server existieren dort nur als Stub-Datensätze für Rechte-Tests. Teilprojekt 2 macht daraus echte Minecraft-Server: anlegen, starten, stoppen, konfigurieren, automatisch neu starten – als Docker-Container mit `itzg/minecraft-server`.

### 1.1 Einsatz

mc-panel betreibt die eigenen Server des Betreibers für einen Freundeskreis: ein VPS, etwa 1–5 Instanzen. Andere Nutzer bekommen höchstens Power- (und ab Teilprojekt 3 Konsolen-)Rechte auf einzelne Server. Daraus folgt: Komfort beim Anlegen ist wichtig, Mandantentrennung und harte Quoten sind es nicht (Spec TP1 §1.2: keine Mandanten, kein Billing).

### 1.2 Erfolg

Ein Admin legt im UI einen Server an (Art, Version, RAM, Name), startet und stoppt ihn, sieht Status und Ressourcen und ändert die wichtigsten `server.properties`. Freunde verbinden sich über `<name>.<basisdomain>`. Ein abgestürzter Server startet selbst neu, nach einem Host-Neustart laufen die Server wieder, die laufen sollen.

### 1.3 Unveränderte Leitlinie

Ein kompromittiertes panel darf keinen Root auf dem Host bekommen (TP1 §3). Zugriff auf den Docker-Socket ist Root-äquivalent – das panel bekommt ihn deshalb nicht.

### 1.4 Nicht-Ziele (Teilprojekt 2)

- Konsole, Logs, Dateibrowser, Plugins/Mods-Verwaltung, Spieler-Verwaltung (Teilprojekt 3).
- Backups (Teilprojekt 4), Spieler-/TPS-Monitoring und Alerts (Teilprojekt 5).
- Installation von Docker auf dem VPS (Teilprojekt 6; in der VM erledigt es `vm-setup.sh`).
- Bedrock Edition (UDP, kein Routing nach Hostname).
- Eigener Port pro Server, schlafende Server (Auto-Pause/Wake), Umbenennen von Servern.
- Jails aus TP1 auf Minecraft-Verkehr anwenden (Docker leitet ihn über die Forward-Chain, die Jail-Regel greift nur für das panel).

## 2. Entscheidungen

| Bereich | Entscheidung | Begründung |
|---|---|---|
| Laufzeit | Docker + `itzg/minecraft-server` | Deckt alle gewünschten Arten ab, pflegt Java-Images, setzt `server.properties` aus Umgebungsvariablen |
| Docker-Zugriff | Nur der agent (root) spricht mit Docker | Hält TP1-Sicherheitsmodell; Alternativen siehe §2.1 |
| Docker-Client | Eigener schmaler Client über `net/http` auf dem Unix-Socket | Nur die nötigen Aufrufe, keine große SDK-Abhängigkeit |
| Erreichbarkeit | Subdomain pro Server über `itzg/mc-router` auf Port 25565 | Ein Port für alle, keine Port-Verwaltung; braucht Wildcard-DNS |
| Subdomain | = Servername (`^[a-z0-9][a-z0-9-]{1,31}$` ist ein gültiges DNS-Label) | Kein zusätzliches Feld |
| Konfiguration | Allowlist-Eigenschaften in der DB, als itzg-Umgebungsvariablen gesetzt | Kein Dateizugriff für das panel; freies Editieren kommt mit TP3 |
| Arten | vanilla, paper, purpur, fabric, neoforge, modrinth, curseforge | Wunsch des Betreibers |

### 2.1 Verworfene Alternativen

- **panel spricht über einen filternden Docker-Socket-Proxy:** Wer Container anlegen darf, kann `privileged` setzen oder `/` mounten; Pfadfilter verhindern das nicht.
- **Rootless Podman/Docker unter eigenem Benutzer, direkt vom panel angesprochen:** Hält den Host sicher, aber Rootless-Netzwerk kostet Latenz im Spielverkehr, Speicherlimits brauchen delegierte cgroups, der Host-Setup (TP6) wird schwerer, und es weicht vom Modell „panel unprivilegiert, agent privilegiert“ ab.

## 3. Architektur

```
Browser ──▶ panel (mcpanel, unprivilegiert)            agent (root)
            internal/servers                  ──JSON──▶ internal/agent/instances
            · DB: Instanzen, Einstellungen     Socket   · validiert InstanceSpec
            · Versionskataloge (HTTP, Cache)            · baut feste Container-Vorlage
            · Abgleich Soll ↔ Ist                       · Docker Engine API (/var/run/docker.sock)
                                                        · schreibt Routen-Datei für mc-router
                                                                 │
                    Docker-Netz "mcpanel-mc" (kein Port nach außen außer Router)
            ┌──────────────┐     ┌────────────────────┐ ┌──────────────────┐
 :25565 ───▶│ mcpanel-router│──▶ │ mcpanel-mc-1 (itzg)│ │ mcpanel-mc-2 …   │
            └──────────────┘     └────────────────────┘ └──────────────────┘
                                  /srv/mc-panel/instances/<id> → /data
```

### 3.1 panel: Paket `internal/servers`

- Übernimmt die Server-Handler aus `internal/users/servers.go` (Liste, Anlegen, Details, Löschen, Power) samt Power-Limiter (`internal/users/powerlimit.go`, wird nur von der Power-Route genutzt). Die Rechtevergabe (`/users/{id}/permissions`) bleibt in `internal/users`.
- Die DB ist Quelle der Wahrheit für den **Soll-Zustand** jeder Instanz.
- Ein Abgleich (§5.3) bringt den Ist-Zustand beim agent dorthin.
- Versionskataloge (§6) holt das panel selbst; der agent braucht keinen Internetzugang außer für Image-Pulls durch Docker.
- Ist der agent nicht erreichbar, bleibt die UI bedienbar; der Ist-Zustand wird als „unbekannt“ angezeigt, Änderungen am Soll-Zustand werden gespeichert und später angewandt.

### 3.2 agent: Paket `internal/agent/instances`

- **`spec`:** Validierung von `InstanceSpec` (§5.2), analog zu `banrules`. Gleiche Regeln wie im panel, aber unabhängig implementiert-geprüft (Verteidigung in der Tiefe: der agent vertraut dem panel nicht).
- **`docker`:** schmaler Client für die Docker Engine API über den Unix-Socket. Unterstützt nur: Image inspizieren/ziehen, Netz inspizieren/anlegen, Container anlegen/starten/stoppen/entfernen/inspizieren/auflisten (Filter auf Label), Stats (ein Snapshot, kein Stream).
- **`template`:** reine Funktion `Spec → Docker-Container-Konfiguration` (§5.4).
- **`router`:** erzeugt die Routen-Datei für mc-router und schreibt sie atomar (temporäre Datei + `rename`).
- **Worker:** eine serielle Warteschlange pro Instanz für alle schreibenden Operationen (§5.1).

Der agent hält keinen eigenen persistenten Zustand. Welche Container er verwaltet, liest er aus Docker (Label `mcpanel.instance`). Container ohne dieses Label fasst er nie an.

### 3.3 Dateisystem auf dem Host

```
/srv/mc-panel/                     root:root 0755   (instances_dir)
├── instances/<id>/                mcpanel-mc 0750  → /data im Container
├── router/routes.json             root:root 0644   → read-only im Router-Container
└── deleted/<name>-<zeitstempel>/  verschobene Daten gelöschter Server
```

- `mcpanel-mc` ist ein eigener Systembenutzer (kein Login, keine Shell), angelegt von `vm-setup.sh` bzw. später vom Installer. UID/GID gehen als `UID`/`GID` an itzg.
- Das panel (`mcpanel`) hat keinen Zugriff auf `/srv/mc-panel/instances`.
- `deleted/` wird nicht automatisch geleert; das dokumentiert die UI beim Löschen.

### 3.4 Konfiguration

Neue Schlüssel in `agent.toml` (alle mit Defaults; der Validator lehnt unbekannte Schlüssel weiterhin ab):

```toml
docker_socket = "/var/run/docker.sock"
instances_dir = "/srv/mc-panel"
mc_user       = "mcpanel-mc"
[images]                                  # Defaults im Code, per Digest gepinnt
java8  = "itzg/minecraft-server:java8@sha256:…"
java17 = "itzg/minecraft-server:java17@sha256:…"
java21 = "itzg/minecraft-server:java21@sha256:…"
java25 = "itzg/minecraft-server:java25@sha256:…"
router = "itzg/mc-router@sha256:…"
```

- Image-Referenzen müssen einen Digest enthalten; ohne Digest startet der agent nicht.
- Das panel wählt nur einen Schlüssel aus dem Enum `java8|java17|java21|java25`, nie ein Image.
- Im Dev-Modus (`make dev` auf macOS) nutzt der agent den Docker-Socket von OrbStack/Docker Desktop, wenn vorhanden (`docker_socket` in der Dev-Konfiguration); sonst meldet er Docker als nicht verfügbar.

`panel.toml` bekommt keine neuen Pflichtschlüssel; Basisdomain und CurseForge-Key sind Laufzeit-Einstellungen in der DB (§4).

## 4. Datenmodell

Neue Migration `internal/store/migrations/00002_instances.sql`. `servers` bleibt unverändert.

```sql
CREATE TABLE instances (
    server_id      INTEGER PRIMARY KEY REFERENCES servers (id) ON DELETE CASCADE,
    type           TEXT    NOT NULL CHECK (type IN ('vanilla', 'paper', 'purpur', 'fabric',
                                                    'neoforge', 'modrinth', 'curseforge')),
    version        TEXT    NOT NULL,          -- 'LATEST' oder z. B. '1.21.4'
    loader_version TEXT,                      -- nur fabric/neoforge; NULL = neueste
    modpack        TEXT,                      -- nur modrinth/curseforge
    java           TEXT    NOT NULL CHECK (java IN ('java8', 'java17', 'java21', 'java25')),
    memory_mib     INTEGER NOT NULL CHECK (memory_mib BETWEEN 1024 AND 32768),
    properties     TEXT    NOT NULL,          -- JSON-Objekt, nur Allowlist-Schlüssel (§7)
    desired_state  TEXT    NOT NULL CHECK (desired_state IN ('running', 'stopped')),
    revision       INTEGER NOT NULL,          -- +1 bei jeder Spec-Änderung
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE TABLE instance_settings (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    base_domain        TEXT,                  -- z. B. 'mc.example.com'
    curseforge_api_key BLOB,                  -- verschlüsselt mit internal/secretbox
    updated_at         INTEGER NOT NULL
);
```

- sqlc-Queries in `internal/store/queries/servers.sql`, Präfix `Servers…`.
- Ein Server ohne `instances`-Zeile (Stub aus TP1) heißt im UI „nicht eingerichtet“ und kann nur gelöscht werden.
- `revision` beginnt bei 1. Der Container trägt die Revision als Label; Abweichung = „Neustart nötig“.
- `base_domain`: gültiger DNS-Name aus Labels nach `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`, mindestens zwei Labels, gesamt ≤ 200 Zeichen, kleingeschrieben gespeichert. FQDN eines Servers = `<name>.<base_domain>` (≤ 253 Zeichen).

## 5. Lebenszyklus

### 5.1 agent-API

Feste Pfade wie in TP1, IDs im Body, `DisallowUnknownFields`, Peer-UID-Prüfung unverändert. Body-Limit für `/v1/instances/*` 16 KiB (übrige Pfade weiter 4 KiB).

| Methode, Pfad | Body | Antwort |
|---|---|---|
| `GET /v1/health` | – | wie bisher plus `docker: "ok" \| "unavailable"` |
| `GET /v1/instances` | – | `{instances: [InstanceStatus], router: RouterStatus}` |
| `POST /v1/instances/ensure` | `{id, name, fqdn, revision, spec, recreate}` | `202` |
| `POST /v1/instances/power` | `{id, action}` mit `start\|stop\|restart` | `202` |
| `POST /v1/instances/remove` | `{id}` | `202` |

`InstanceStatus`: `id`, `revision`, `state` (`created\|running\|restarting\|exited\|missing`), `exit_code`, `started_at` (Unix-ms), `memory_bytes`, `memory_limit_bytes`, `cpu_percent`, `operation` (`""\|pulling\|creating\|starting\|stopping\|removing`), `last_error` (Text, ≤ 500 Zeichen, ohne Secrets).

- **ensure:** stellt sicher, dass `mcpanel-mc-<id>` in genau dieser Revision existiert. Fehlt das Image, wird es gezogen. Existiert ein Container mit älterer Revision und `recreate` ist `false`, passiert nichts. Mit `recreate: true` wird er gestoppt, entfernt und neu angelegt; lief er vorher, wird er danach gestartet. Legt das Datenverzeichnis an (Besitzer `mcpanel-mc`), falls es fehlt. Schreibt danach die Routen-Datei neu.
- **power:** wirkt auf den vorhandenen Container; fehlt er, setzt der Worker `last_error`.
- **remove:** stoppt und entfernt den Container, verschiebt `instances/<id>` nach `deleted/<name>-<UTC-Zeitstempel>`, schreibt die Routen-Datei neu.
- Alle schreibenden Aufrufe kehren sofort mit `202` zurück und landen in der Warteschlange der Instanz. Eine neue Operation ersetzt keine laufende; die Warteschlange hat eine Länge von höchstens 4 (sonst `429`).
- `id` ist die Server-ID (int64 > 0); `name` erfüllt die Namens-Regex; `fqdn` beginnt mit `<name>.` und ist ein gültiger DNS-Name (≤ 253 Zeichen).

Der Router-Container `mcpanel-router` wird beim agent-Start und bei jedem ensure sichergestellt, sobald Docker verfügbar ist. `RouterStatus`: `state`, `last_error`.

### 5.2 InstanceSpec

```json
{
  "type": "paper",
  "version": "1.21.4",
  "loader_version": null,
  "modpack": null,
  "java": "java21",
  "memory_mib": 4096,
  "properties": { "motd": "Unser Server", "difficulty": "normal" },
  "curseforge_api_key": null
}
```

Regeln (panel und agent):

| Feld | Regel |
|---|---|
| `type` | Enum aus §4 |
| `version` | `LATEST` oder `^[0-9A-Za-z][0-9A-Za-z.\-+_]{0,63}$`; bei modrinth/curseforge ignoriert und leer |
| `loader_version` | nur fabric/neoforge, sonst `null`; Format wie `version` |
| `modpack` | nur modrinth: `^[a-z0-9-]{1,64}$`; nur curseforge: Slug wie Modrinth oder `https://www.curseforge.com/minecraft/modpacks/<slug>`; sonst `null` |
| `java` | Enum aus §4 |
| `memory_mib` | 1024–32768, Vielfaches von 256 |
| `properties` | nur Schlüssel und Typen aus §7 |
| `curseforge_api_key` | nur bei `type = curseforge`, Pflicht dort; `^[A-Za-z0-9$./_-]{1,128}$` |

Zusätzlich prüft der agent jeden Wert, der in eine Umgebungsvariable geht, auf `=`-freie Schlüssel (fest im Code) und Werte ohne Steuerzeichen (`\x00`–`\x1f`, `\x7f`).

### 5.3 Abgleich im panel

Eine Schleife läuft alle 10 s (über `clock.Clock`) und zusätzlich sofort nach jeder Nutzeraktion (Signal über einen Kanal, nicht blockierend). Pro Instanz:

| Soll | Ist | Aktion |
|---|---|---|
| beliebig | `missing` | `ensure` (recreate `false`), danach ggf. `start` |
| `stopped` | ältere Revision, nicht laufend | `ensure` mit `recreate: true` |
| `running` | ältere Revision, laufend | nichts; `restart_required = true` |
| `running` | `exited`/`created` | `power start` |
| `stopped` | `running`/`restarting` | `power stop` |
| beliebig | `operation != ""` | nichts (agent arbeitet) |

- „Neustart“ durch einen Nutzer: Ist die Revision aktuell → `power restart`; sonst `ensure` mit `recreate: true` (das startet wieder, weil der Container vorher lief).
- Container mit `mcpanel.instance`-Label, die die DB nicht kennt: Log-Warnung und Anzeige in den Einstellungen; kein automatisches Löschen.
- Fehler des agents (nicht erreichbar, `docker_unavailable`) brechen den Durchlauf ab und werden geloggt; der nächste Durchlauf versucht es erneut.
- Abstürze und automatische Neustarts erscheinen nur im Log, nicht im Audit-Log.

### 5.4 Container-Vorlage

Der agent erzeugt aus einer gültigen Spec genau diese Konfiguration; nichts davon ist vom panel steuerbar:

- Name `mcpanel-mc-<id>`, Hostname `<name>`.
- Image aus `[images].<java>`.
- Labels: `mcpanel.instance=<id>`, `mcpanel.revision=<revision>`, `mcpanel.name=<name>`, `mcpanel.fqdn=<fqdn>`.
- Netz: nur `mcpanel-mc` (bridge, vom agent angelegt). Keine veröffentlichten Ports.
- Mount: `/srv/mc-panel/instances/<id>` → `/data` (bind, rw). Keine weiteren Mounts.
- `HostConfig`: `CapDrop: ["ALL"]`, `CapAdd` nur, was itzg für den Wechsel auf `UID`/`GID` braucht (`CHOWN`, `SETUID`, `SETGID`, `DAC_OVERRIDE` – im Plan gegen das Image verifizieren und auf das Minimum reduzieren), `SecurityOpt: ["no-new-privileges"]`, `Privileged: false`, `ReadonlyRootfs: false` (itzg schreibt außerhalb von `/data`), `Memory = memory_mib × 1.25 MiB`, `MemorySwap = Memory`, `PidsLimit: 1024`, `RestartPolicy: unless-stopped`, `LogConfig`: `json-file` mit `max-size=10m`, `max-file=3`.
- `StopTimeout: 60`.
- Umgebung, fest: `EULA=TRUE`, `TYPE`, `VERSION`, `MEMORY=<memory_mib>M`, `UID`, `GID`, `ENABLE_AUTOPAUSE=false`, `ENABLE_RCON=true` (nur im internen Netz erreichbar; für TP3), `OVERRIDE_SERVER_PROPERTIES=true`; je nach Art `FABRIC_LOADER_VERSION`, `NEOFORGE_VERSION`, `MODRINTH_PROJECT`, `CF_SLUG`/`CF_PAGE_URL`, `CF_API_KEY`.
- Umgebung aus `properties` gemäß §7. Alle anderen itzg-Variablen (`JVM_OPTS`, `GENERIC_PACK`, `MODS`, `PLUGINS`, …) sind nicht setzbar.

Router-Container `mcpanel-router`: Image `[images].router`, Netz `mcpanel-mc`, Port `25565/tcp` auf dem Host veröffentlicht, Mount `/srv/mc-panel/router` → `/config` (ro), Start mit Routen-Datei und Datei-Überwachung, gleiche `HostConfig`-Härtung (ohne `CapAdd`), `RestartPolicy: unless-stopped`. Der Router bekommt keinen Docker-Socket. Das genaue Format der Routen-Datei und die Flags werden im Plan gegen die gepinnte mc-router-Version verifiziert.

### 5.5 Automatischer Neustart

- Absturz: Docker startet neu (`unless-stopped`, mit Docker-eigenem Backoff).
- Host-Neustart: Docker startet Container mit `unless-stopped` wieder, die nicht explizit gestoppt waren; der Abgleich korrigiert alles Übrige anhand von `desired_state`.
- `desired_state` ändert nur ein Nutzer (Power-Aktion), nie der Abgleich.

## 6. Versionskataloge

`GET /api/servers/catalog/{type}` (Admin). Das panel fragt feste Upstream-URLs ab; nur das Enum `type` wählt die Quelle.

| Art | Quelle | Liefert |
|---|---|---|
| vanilla | `https://piston-meta.mojang.com/mc/game/version_manifest_v2.json` | Releases, neueste zuerst |
| paper | PaperMC Fill-API v3 (`https://fill.papermc.io/v3/projects/paper`) | MC-Versionen mit stabilem Build |
| purpur | `https://api.purpurmc.org/v2/purpur` | MC-Versionen |
| fabric | `https://meta.fabricmc.net/v2/versions/game`, `…/loader` | stabile Spielversionen, Loader-Versionen |
| neoforge | NeoForged-Maven-API (`https://maven.neoforged.net/api/maven/versions/releases/net/neoforged/neoforge`) | NeoForge-Versionen, nach MC-Version gruppiert |
| modrinth, curseforge | – | `404`; das Pack bestimmt die Version |

- Antwort: `{versions: [{id, java}], loaders?: [string]}`; `java` ist die abgeleitete Empfehlung (§6.1).
- Cache im Speicher 1 h pro Art; Timeout 10 s, Antwortgröße ≤ 5 MiB; Fehler → `502 catalog_unavailable` (das Formular bietet dann Freitext).
- Die genauen Endpunkte und Antwortformate (insbesondere Fill-API v3 und NeoForged-Maven) werden im Plan gegen die echten Dienste verifiziert und als Beispielantworten in `testdata/` abgelegt.

### 6.1 Java-Ableitung

| MC-Version | Java |
|---|---|
| ≤ 1.16.5 | java8 |
| 1.17 – 1.20.4 | java17 |
| 1.20.5 – 1.21.x | java21 |
| ≥ 26.1 (Jahresschema) und `LATEST` | java25 |
| Modpacks | java21 (Default) |

Ein Admin kann den Wert unter „Erweitert“ überschreiben. Die Ableitung liegt im panel (Go) und wird für das Formular im Katalog mitgeliefert; das Frontend rechnet für Freitext-Versionen mit derselben Tabelle (gemeinsame Testfälle).

## 7. Eigenschaften (Allowlist)

| Schlüssel (`properties`) | Typ / Grenzen | itzg-Variable | Default |
|---|---|---|---|
| `motd` | Text ≤ 150 Zeichen, druckbar, ohne Zeilenumbruch | `MOTD` | Servername |
| `difficulty` | `peaceful\|easy\|normal\|hard` | `DIFFICULTY` | `normal` |
| `gamemode` | `survival\|creative\|adventure\|spectator` | `MODE` | `survival` |
| `max_players` | 1–500 | `MAX_PLAYERS` | 20 |
| `pvp` | bool | `PVP` | `true` |
| `hardcore` | bool | `HARDCORE` | `false` |
| `allow_flight` | bool | `ALLOW_FLIGHT` | `false` |
| `enable_command_block` | bool | `ENABLE_COMMAND_BLOCK` | `false` |
| `online_mode` | bool (UI warnt bei `false`) | `ONLINE_MODE` | `true` |
| `whitelist_enabled` | bool | `ENABLE_WHITELIST` | `false` |
| `whitelist` | ≤ 200 Namen `^[A-Za-z0-9_]{3,16}$` | `WHITELIST` (kommagetrennt) | leer |
| `ops` | ≤ 50 Namen, wie oben | `OPS` | leer |
| `view_distance` | 3–32 | `VIEW_DISTANCE` | 10 |
| `simulation_distance` | 3–32 | `SIMULATION_DISTANCE` | 10 |
| `spawn_protection` | 0–256 | `SPAWN_PROTECTION` | 16 |
| `seed` | Text ≤ 64, druckbar | `SEED` | leer |
| `level_type` | `normal\|flat\|large_biomes\|amplified` | `LEVEL_TYPE` (`minecraft:…`) | `normal` |

- Fehlende Schlüssel = Default; unbekannte Schlüssel → `400 validation_failed` (panel) bzw. `400` (agent).
- `seed` und `level_type` wirken nur bei einer neuen Welt; das UI sagt das.

## 8. panel-API

Alle Routen über `*rbac.Router`, in `api/openapi.yaml` beschrieben, mit MSW-Mock.

| Route | Regel | Inhalt |
|---|---|---|
| `GET /servers` | Authenticated | wie bisher plus `type`, `version`, `state`, `address`, `restart_required`; `null`-Felder für nicht eingerichtete Server |
| `POST /servers` | Admin + Sudo | Name + Instanz-Konfiguration + `eula_accepted: true` (Pflicht); `201` mit `state: "creating"`; `409 base_domain_missing` ohne Basisdomain, `409 conflict` bei vergebenem Namen |
| `GET /servers/{id}` | `server.view` | plus Konfiguration, `properties`, Ist-Zustand (`InstanceStatus` ohne interne Felder), `restart_required`, `last_error` |
| `PATCH /servers/{id}` | Admin + Sudo | `version`, `loader_version`, `modpack`, `java`, `memory_mib`, `properties` (teilweise); erhöht `revision`; Antwort enthält `restart_required` |
| `DELETE /servers/{id}` | Admin + Sudo | `remove` an den agent; DB-Zeilen weg, sobald der agent `202` geantwortet hat; Daten nach `deleted/` |
| `POST /servers/{id}/power` | `server.power` | `start\|stop\|restart` setzt `desired_state` (bzw. löst Neustart aus, §5.3); `202`; Power-Limiter wie TP1 |
| `GET /servers/catalog/{type}` | Admin | §6 |
| `GET /servers/settings` | Admin | `base_domain`, `curseforge_api_key_set`, `docker` (Status), `unknown_containers` |
| `PUT /servers/settings` | Admin + Sudo | `base_domain`, `curseforge_api_key` (String setzt, `null` entfernt, fehlend = unverändert) |

- `address` = `<name>.<base_domain>`; ohne Basisdomain `null`.
- Ist der agent nicht erreichbar, liefern Lese-Endpunkte den Soll-Zustand mit `state: "unknown"`; schreibende Endpunkte speichern den Soll-Zustand und antworten normal (der Abgleich holt es nach). Ausnahme `DELETE` eines eingerichteten Servers: ohne agent `503 agent_unavailable`, damit keine verwaisten Container entstehen. Nicht eingerichtete Server (ohne `instances`-Zeile) löscht das panel ohne agent.
- Scheitert ein `remove` nach dem `202` asynchron, bleibt der Container zurück und erscheint als unbekannter Container in den Einstellungen.
- Die FQDN gehört zur Container-Konfiguration (Label, Routen-Datei). Eine Änderung der Basisdomain erhöht deshalb die `revision` aller Instanzen: gestoppte Server legt der Abgleich neu an, laufende zeigen „Neustart nötig“ und sind bis dahin unter der alten Adresse erreichbar.

### 8.1 Fehlercodes

Neu in `internal/httpx/errors.go` und im Enum des Vertrags:

| Code | Status | Wann |
|---|---|---|
| `docker_unavailable` | 503 | agent erreichbar, Docker nicht |
| `base_domain_missing` | 409 | Server anlegen ohne Basisdomain |
| `catalog_unavailable` | 502 | Upstream-Katalog nicht erreichbar |

Fehlender CurseForge-Key → `400 validation_failed`. Asynchrone Fehler des agents erscheinen als `last_error`, nicht als HTTP-Fehler.

### 8.2 Audit

`server.create` (Konfiguration ohne Secrets), `server.update` (Diff der geänderten Felder), `server.delete`, `server.power` (wie TP1), `server.settings_update` (Basisdomain alt/neu, `curseforge_api_key` nur als `set`/`removed`). Neu als Konstanten in `internal/audit/types.go`: `ActionServerUpdate`, `ActionServerSettingsUpdate`.

## 9. Frontend

Bestehende shadcn-Komponenten; kein visuelles Redesign in diesem Teilprojekt.

- **Dashboard (`/`):** `ServerCard` mit Name, Adresse (Kopieren), Status-Badge (läuft, gestoppt, startet, Fehler, unbekannt, nicht eingerichtet), Art/Version, RAM-Belegung, Power-Buttons bei `server.power`, Hinweis „Neustart nötig“, Link zur Detailseite. Fehlt die Basisdomain, sehen Admins einen Hinweis mit Link zu den Einstellungen, „Server anlegen“ ist gesperrt.
- **Server anlegen (`/servers/new`, Admin):** ersetzt `create-server-dialog.tsx`. Abschnitte: Name (mit Live-Adresse), Art (mit Kurzbeschreibung), Version/Loader/Modpack (Katalog, Freitext als Fallback, Java abgeleitet, „Erweitert“), RAM (1–32 GiB, Default 4 GiB), Eigenschaften (eingeklappt, Defaults aus §7), EULA-Checkbox mit Link (Pflicht). Button „Server anlegen“ → Sudo → Detailseite.
- **Detailseite (`/servers/$id`):** Tabs Übersicht (`server.view`: Status, Adresse, RAM/CPU, Startzeit, `last_error`, Power-Buttons, Banner „Neustart nötig“ mit „Jetzt neu starten“), Einstellungen (Admin: Formular ohne Name/Art, „Änderungen speichern“, Hinweis bei laufendem Server), Löschen (Admin: Namen eintippen, Text nennt den Zielordner).
- **Einstellungen (`/settings`, Admin, neuer Navigationspunkt):** Abschnitt „Minecraft-Server“ mit Basisdomain (Beispiel für den DNS-Eintrag `*.<basisdomain>`), CurseForge-Key (nur schreibbar, „hinterlegt“, Entfernen), Docker-/agent-Status, unbekannte Container.
- **Aktualisierung:** TanStack Query alle 3 s, solange eine Operation läuft oder der Zustand von `desired_state` abweicht, sonst alle 15 s.
- Texte `de`/`en`, mobil bedienbar, MSW-Mocks für alle neuen Operationen.

## 10. Linux-VM und Entwicklung

- `scripts/vm-setup.sh` installiert zusätzlich Docker (Ubuntu-Paket `docker.io`), legt `mcpanel-mc` an, setzt im VM-Profil `base_domain` nicht (der Test legt sie per API fest) und nutzt die Lima-Weiterleitung für `25565` (der Router veröffentlicht den Port in der VM, Lima leitet ihn nach `127.0.0.1:25565` auf dem Mac). Basisdomain im VM-Test: `mc.localhost`. Der Minecraft-Client (Java) nutzt den macOS-Resolver, der `*.localhost` nur zu `::1` auflöst, während die Lima-Weiterleitung nur auf IPv4 lauscht; pro Server ist deshalb ein `/etc/hosts`-Eintrag `127.0.0.1 <name>.mc.localhost` nötig – dokumentiert in `docs/dev/linux-vm.md`.
- `vm-setup.sh` setzt weiterhin den Zustand zurück; neu: es entfernt dabei alle Container mit Label `mcpanel.instance` und leert `/srv/mc-panel/instances`.
- `make dev`: agent nutzt den Docker-Socket des Macs, falls vorhanden (§3.4).

## 11. Tests

| Ebene | Inhalt |
|---|---|
| agent `spec` | Tabellentests für jede Regel aus §5.2 und §7, inkl. Injektionsversuchen (`=`, Zeilenumbruch, Null-Byte, überlange Werte, unbekannte Schlüssel) |
| agent `template` | Golden-Test: Spec → exakt erwartete Container-Konfiguration (Caps, Mounts, Limits, Labels, Env); Negativtest: keine Spec erzeugt zusätzliche Mounts, Ports oder Env-Variablen |
| agent `docker` | Fake-Docker-Server (`httptest` auf Unix-Socket): Lebenszyklus, Label-Filter (fremde Container werden nie angefasst), Fehlerpfade, Timeouts |
| agent `router` | Routen-Datei aus Container-Liste, atomares Schreiben, leere Liste |
| agent Worker | Serielle Abarbeitung pro Instanz, Warteschlangenlimit, `last_error` |
| agent Integration | Build-Tag `dockerintegration` (nur Linux): echter Docker-Daemon, kleines gepinntes Test-Image statt itzg (über die `[images]`-Konfiguration); neuer Schritt im bestehenden Linux-CI-Job |
| panel `servers` | Handler mit `storetest` + Fake-agent: Rechte (view/power/admin), Sudo, Validierung, Audit-Einträge, agent nicht erreichbar |
| panel Abgleich | Fake-Clock + Fake-agent: jede Zeile der Tabelle in §5.3, agent fällt aus und kommt wieder, veraltete Revision, Basisdomain-Wechsel |
| panel Katalog | `httptest`-Upstreams mit Beispielantworten aus `testdata/`, Cache, Timeout, Größenlimit, Java-Ableitung |
| Vertrag | bestehende Tests: OpenAPI ↔ Routen, Fehlercodes ↔ Enum, Mock pro Operation |
| Frontend | Vitest: Java-Ableitung (gleiche Fälle wie Go), Formular-Validierung, Status-Badge; Playwright gegen Mock: Server anlegen → Detailseite → Einstellung ändern → „Neustart nötig“ → Neustart |

Konventionen aus TP1 gelten unverändert (nur Standardbibliothek in Go-Tests, `clock.Clock`, Kontexte überall).

## 12. Abnahmekriterien

1. `make test` und `make lint` grün; `dockerintegration`-Tests in CI grün.
2. In der VM legt ein Admin einen Paper-Server an; er startet, und ein Minecraft-Client verbindet sich über `<name>.<basisdomain>`.
3. Je ein Server mit Vanilla, Fabric, NeoForge und einem Modrinth-Pack startet in der VM.
4. Ein Nutzer mit `server.power` auf Server A kann A starten und stoppen, aber nicht ändern; Zugriff auf Server B → `403`.
5. RAM oder MOTD ändern → „Neustart nötig“; nach dem Neustart gilt die neue Konfiguration, die Welt ist erhalten.
6. `docker kill mcpanel-mc-<id>` → automatischer Neustart; nach einem VM-Neustart laufen alle Server mit `desired_state = running` wieder.
7. `docker inspect` belegt: Caps auf das Minimum aus §5.4 reduziert, `no-new-privileges`, Speicherlimit, nur `/data` gemountet, keine veröffentlichten Ports außer am Router. Der panel-Benutzer hat keinen Zugriff auf `docker.sock` und `/srv/mc-panel/instances`.
8. Löschen entfernt Container und Route, die Daten liegen unter `deleted/`; alle Admin-Aktionen stehen im Audit-Log, `panel audit verify` meldet OK.
