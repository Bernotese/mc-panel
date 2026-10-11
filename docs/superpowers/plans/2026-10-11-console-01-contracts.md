# mc-panel Teilprojekt 3a – Phase A: Verträge (Aufgaben 1–3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Die gemeinsamen Grundlagen für B1/B2/B3 auf `main` bringen: Ops/Whitelist verlassen die panel-Eigenschaften (Spec §3.1), der agentapi-Vertrag samt Client für Logs/RCON/Spielerlisten steht, und der panel-Vertrag (OpenAPI, Fehlercodes, Audit-Aktionen, Stub-Handler, MSW-Mocks) ist so weit, dass agent, panel und Frontend parallel weiterbauen können.

**Architecture:** Drei Aufgaben auf einem Branch `feat/console-contracts` (Lead, sequenziell). Aufgabe 1 ändert vorhandenen TP2-Code (Migration `00004`, `instancespec`, agent, panel, Frontend-Formular). Aufgabe 2 fügt `internal/agent/agentapi/console.go` und `internal/agent/client_console.go` hinzu. Aufgabe 3 fügt den OpenAPI-Vertrag, die Fehlercodes, Audit-Konstanten, ein Paket `internal/console` mit Stub-Handlern und die MSW-Mocks hinzu; alle neuen Routen antworten bis Phase B2 `503 agent_unavailable`.

**Tech Stack:** Go (Standardbibliothek, goose, chi), SQLite (`json_remove`/`json_type`), OpenAPI 3.1 + `openapi-typescript`, MSW 2, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md` · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-11-console-00-overview.md` (V1, V3, V4, V5; Abschnitte „Ausführung“ und „Global Constraints“ gelten hier vollständig).

## Ausführung dieser Phase

- Branch: `git worktree add ../mc-panel-wt/feat-console-contracts -b feat/console-contracts origin/main` (oder direkt im Lead-Checkout auf diesem Branch). Ein Draft-PR für alle drei Aufgaben; Commit pro Aufgabe.
- Lokal nur `make check` (und `make generate` in Aufgabe 1 und 3). „Run: go test …“ / „npx vitest …“ bedeutet: nicht lokal, sondern Push und `gh pr checks <pr> --watch`; Ergebnis in den CI-Logs prüfen.
- Eine Aufgabe ist fertig, wenn die CI ihres Commits grün ist. Danach mergt der Nutzer den PR; erst dann starten B1, B2, B3.

## Abweichung von V5 (bewusst)

V5 nennt `sse()` aus `msw` für `streamConsole`. Das geht hier nicht: `sse()` prüft schon beim Erzeugen des Handlers `typeof EventSource !== 'undefined'` (`web/node_modules/msw/lib/core/sse.mjs`) – in Vitest/jsdom gibt es kein `EventSource`, `handlers.ts` würde beim Import werfen. Außerdem ignoriert `sse()` den Rückgabewert des Resolvers, kann also kein `401/403/404` liefern. Aufgabe 3 baut den Stream deshalb mit `http.get` und einer `ReadableStream`-Antwort (`Content-Type: text/event-stream`); im Browser funktioniert das mit dem Service Worker genauso, das Ereignisformat entspricht V3.

---

### Task 1: Ops und Whitelist verlassen die Eigenschaften

**Files:**
- Create: `internal/store/migrations/00004_players_in_game.sql`
- Create: `internal/store/migrate_players_test.go` (Paket `store`, intern)
- Modify: `internal/agent/instancespec/properties.go` (Felder, Validierung, Resolve)
- Modify: `internal/agent/instancespec/env.go` (keine `OPS`/`WHITELIST`/`EXISTING_*_FILE` mehr)
- Modify: `internal/agent/instancespec/properties_test.go`, `internal/agent/instancespec/env_test.go`
- Modify: `internal/agent/instances/datadir.go` (`clearStaleLists`, `emptyListFile` entfernen)
- Modify: `internal/agent/instances/manager.go:233-236` (Aufruf entfernen)
- Modify: `internal/agent/instances/manager_test.go:600-677` (`TestEnsureClearsStaleListFiles` ersetzen)
- Modify: `internal/agent/instances/manager_hardening_test.go:121-181` (`TestListFileSymlinkIsReplaced`, `TestListFileDirectoryIsMovedAside` ersetzen)
- Modify: `internal/agent/instances/template_test.go` (Golden-Env, `allowedEnvKeys`, `variedRequests`)
- Modify: `internal/agent/agentapi/instances.go:17-19` (Kommentar)
- Modify: `internal/agent/server_instances_test.go` (`TestServerInstanceBodyLimitIs16KiB`, zwei Fälle in `TestServerRejectsInvalidInstanceRequests`)
- Modify: `internal/servers/view.go` (`propertiesJSON`, `resolvedProperties`)
- Modify: `internal/servers/servers_test.go:162-164` und `TestUpdateServerRejects`
- Modify: `api/openapi.yaml` (`ServerProperties` ohne `whitelist`/`ops`); generiert: `web/src/api/schema.d.ts`
- Modify: `web/src/features/servers/form-model.ts`, `form-model.test.ts`, `properties-fields.tsx`, `validation.ts`, `validation.test.ts`, `create-server-page.test.tsx`, `server-detail-page.test.tsx`
- Modify: `web/src/mocks/db.ts` (`resolveProperties`)
- Modify: `web/src/i18n/de.ts`, `web/src/i18n/en.ts`

**Interfaces:**
- Consumes: –
- Produces: `instancespec.Properties` ohne `Whitelist`/`Ops` (JSON-Schlüssel `whitelist`/`ops` sind danach unbekannt → `400` in panel und agent, weil beide mit `DisallowUnknownFields` dekodieren); `instancespec.Env` ohne `OPS`, `WHITELIST`, `EXISTING_OPS_FILE`, `EXISTING_WHITELIST_FILE`; TS-Typ `ServerProperties` ohne `whitelist`/`ops`; `PropertiesDraft` ohne `whitelist`/`ops`; i18n-Schlüssel `servers.props.lists_hint` (ersetzt `lists_sync_hint`).

Vorher alle Fundstellen prüfen (die Liste oben ist das Ergebnis dieser Suche; bei Abweichungen mit anpassen):

```bash
grep -rnE '\bOps\b|\bWhitelist\b|"whitelist"|"ops"|clearStaleLists|emptyListFile|maxWhitelisted|maxOps|OPS=|WHITELIST=|EXISTING_' --include='*.go' internal cmd
grep -rnP "\bops\b|whitelist(?!_enabled)|Whitelist(?!Enabled)|MAX_OPS|MAX_WHITELIST|PlayerNames|PLAYER_RE|players_(invalid|too_many)|lists_sync_hint|Whitelist und Operatoren" web/src web/e2e | grep -v schema.d.ts
```

`docs/dev/acceptance-tp2.md` bleibt unverändert (beschreibt den Stand von TP2).

- [ ] **Step 1: Migrationstest schreiben (rot)**

`internal/store/migrate_players_test.go` – im Paket `store` (intern), weil er die Migrationen gezielt nur bis Version 3 anwenden muss und `storetest` `store` importiert (Importzyklus):

```go
package store

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrationPlayersInGame: 00004 removes ops and whitelist from the stored properties and
// bumps the revision of exactly the instances that had one of them (spec 3a §3.1).
func TestMigrationPlayersInGame(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		id    int64
		props string
	}{
		{1, `{"motd":"Hallo","whitelist":["Notch"],"whitelist_enabled":true}`},
		{2, `{"ops":["jeb_"],"max_players":8}`},
		{3, `{"motd":"keep","whitelist_enabled":false}`},
		{4, `{}`},
		{5, `{"whitelist":[],"ops":["Notch"]}`},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx, `INSERT INTO servers (id, name, created_at) VALUES (?, ?, 0)`, r.id, fmt.Sprintf("s%d", r.id)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO instances (server_id, type, version, java, memory_mib, properties, desired_state, revision, created_at, updated_at)
			 VALUES (?, 'paper', '1.21.4', 'java21', 4096, ?, 'running', 5, 10, 10)`, r.id, r.props); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	want := map[int64]struct {
		props    string
		revision int64
		touched  bool
	}{
		1: {`{"motd":"Hallo","whitelist_enabled":true}`, 6, true},
		2: {`{"max_players":8}`, 6, true},
		3: {`{"motd":"keep","whitelist_enabled":false}`, 5, false},
		4: {`{}`, 5, false},
		5: {`{}`, 6, true},
	}
	for id, w := range want {
		var props string
		var revision, updatedAt int64
		if err := db.QueryRowContext(ctx, `SELECT properties, revision, updated_at FROM instances WHERE server_id = ?`, id).
			Scan(&props, &revision, &updatedAt); err != nil {
			t.Fatal(err)
		}
		if props != w.props || revision != w.revision || (updatedAt > 10) != w.touched {
			t.Errorf("instance %d: properties %s revision %d updated_at %d, want %s %d touched=%v",
				id, props, revision, updatedAt, w.props, w.revision, w.touched)
		}
	}
}
```

- [ ] **Step 2: Migration schreiben**

`internal/store/migrations/00004_players_in_game.sql`:

```sql
-- +goose Up
-- Sub-project 3a: ops and the whitelist belong to the Minecraft server itself (ops.json,
-- whitelist.json) and are changed live over RCON; the panel no longer stores or syncs them.
-- Instances that had either list get a new revision so that their container is recreated
-- without the OPS/WHITELIST variables (running ones show "restart required").
-- The files on disk keep the lists; only names of a disabled whitelist (which TP2 emptied
-- in whitelist.json) are lost - they were not in effect.
UPDATE instances
SET properties = json_remove(properties, '$.ops', '$.whitelist'),
    revision   = revision + 1,
    updated_at = CAST(strftime('%s', 'now') AS INTEGER) * 1000
WHERE json_type(properties, '$.ops') IS NOT NULL
   OR json_type(properties, '$.whitelist') IS NOT NULL;

-- +goose Down
-- The removed lists cannot be restored here; ops.json and whitelist.json still hold them.
SELECT 1;
```

- [ ] **Step 3: `instancespec`-Tests anpassen (rot)**

`internal/agent/instancespec/properties_test.go`:

- In `TestPropertiesValidation` den gültigen Fall `{Whitelist: names200, Ops: …, WhitelistEnabled: ptr(true)}` ersetzen durch `{WhitelistEnabled: ptr(true)}`; `names200` und den Import `strings` nur behalten, solange sie noch gebraucht werden (`strings.Repeat` bleibt in Gebrauch, `names200` entfällt samt Schleife).
- Die ungültigen Fälle `"whitelist name"`, `"whitelist comma"`, `"whitelist too many"`, `"ops too many"` entfernen.
- In `TestResolveDefaults` die Bedingung `r.Whitelist == nil || r.Ops == nil ||` entfernen.
- Neuen Test anhängen (Import `encoding/json` und `bytes` ergänzen):

```go
// TestListKeysAreUnknown: since sub-project 3a the server owns ops and the whitelist; a client
// that still sends them gets a validation error from the strict decoders of panel and agent.
func TestListKeysAreUnknown(t *testing.T) {
	for _, raw := range []string{`{"ops":["Notch"]}`, `{"whitelist":["Notch"]}`} {
		dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
		dec.DisallowUnknownFields()
		var p instancespec.Properties
		if err := dec.Decode(&p); err == nil {
			t.Errorf("%s: decoded without error", raw)
		}
	}
}
```

`internal/agent/instancespec/env_test.go`:

```go
// allowedKeys is every environment variable Env may ever produce. Anything else
// (JVM_OPTS, GENERIC_PACK, MODS, PLUGINS, OPS, WHITELIST, ...) would let the panel smuggle in
// code or override what the server itself manages (ops and whitelist, spec 3a §3.1).
var allowedKeys = []string{
	"EULA", "TYPE", "VERSION", "MEMORY", "ENABLE_AUTOPAUSE", "ENABLE_RCON", "OVERRIDE_SERVER_PROPERTIES",
	"FABRIC_LOADER_VERSION", "NEOFORGE_VERSION", "MODRINTH_MODPACK", "CF_SLUG", "CF_PAGE_URL", "CF_API_KEY",
	"MOTD", "DIFFICULTY", "MODE", "MAX_PLAYERS", "PVP", "HARDCORE", "ALLOW_FLIGHT", "ENABLE_COMMAND_BLOCK",
	"ONLINE_MODE", "ENABLE_WHITELIST", "VIEW_DISTANCE", "SIMULATION_DISTANCE",
	"SPAWN_PROTECTION", "SEED", "LEVEL_TYPE",
}

func TestEnvPaper(t *testing.T) {
	s := validSpec()
	s.Properties = instancespec.Properties{MOTD: ptr("Hallo"), WhitelistEnabled: ptr(true)}
	env, err := instancespec.Env("survival", s)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"EULA=TRUE", "TYPE=PAPER", "VERSION=1.21.4", "MEMORY=4096M", "ENABLE_AUTOPAUSE=false",
		"ENABLE_RCON=true", "OVERRIDE_SERVER_PROPERTIES=true",
		"MOTD=Hallo", "DIFFICULTY=normal", "MODE=survival", "MAX_PLAYERS=20", "PVP=true", "HARDCORE=false",
		"ALLOW_FLIGHT=false", "ENABLE_COMMAND_BLOCK=false", "ONLINE_MODE=true", "ENABLE_WHITELIST=true",
		"VIEW_DISTANCE=10", "SIMULATION_DISTANCE=10", "SPAWN_PROTECTION=16",
		"LEVEL_TYPE=minecraft:normal",
	}
	if !slices.Equal(env, want) {
		t.Fatalf("env =\n%q\nwant\n%q", env, want)
	}
}
```

Im letzten Fall von `TestEnvPerType` `Ops: []string{"Notch"}` aus den Properties und `"OPS=Notch"` aus `has` entfernen und `not: []string{"WHITELIST", "OPS", "EXISTING_OPS_FILE", "EXISTING_WHITELIST_FILE"}` setzen. `TestEnvOmitsDisabledWhitelist` ersetzen durch:

```go
// The server owns ops.json and whitelist.json (spec 3a §3.1): Env never sends lists or sync
// modes, whatever the whitelist switch says.
func TestEnvNeverSendsPlayerLists(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		s := validSpec()
		s.Properties = instancespec.Properties{WhitelistEnabled: ptr(enabled)}
		env, err := instancespec.Env("survival", s)
		if err != nil {
			t.Fatal(err)
		}
		for _, kv := range env {
			k, _, _ := strings.Cut(kv, "=")
			if slices.Contains([]string{"WHITELIST", "OPS", "EXISTING_OPS_FILE", "EXISTING_WHITELIST_FILE"}, k) {
				t.Fatalf("whitelist_enabled=%v: env contains %q", enabled, kv)
			}
		}
		if want := "ENABLE_WHITELIST=" + strconv.FormatBool(enabled); !slices.Contains(env, want) {
			t.Fatalf("missing %s in %q", want, env)
		}
	}
}
```

(Import `strconv` ergänzen.)

- [ ] **Step 4: Rot prüfen**

Run: `make check`
Expected: PASS – die geänderten Tests kompilieren noch (sie nutzen `Whitelist`/`Ops` nicht mehr), sind aber inhaltlich rot: `TestEnvPaper`, `TestEnvNeverSendsPlayerLists` und `TestMigrationPlayersInGame` würden in der CI scheitern. Nicht pushen, direkt weiter mit der Implementierung.

- [ ] **Step 5: `instancespec` umsetzen**

`internal/agent/instancespec/properties.go`:

- Felder `Whitelist` und `Ops` aus `Properties` löschen.
- Konstanten `maxWhitelisted` und `maxOps` löschen.
- In `Validate` die beiden `players(…)`-Aufrufe löschen.
- Funktion `players` und `playerRe` löschen (sonst `unused`).
- In `Resolve` die Hilfsfunktion `list` und die Zeilen `Whitelist: list(p.Whitelist),` / `Ops: list(p.Ops),` löschen.
- Kommentar der Struktur ergänzen:

```go
// Properties are the allowlisted server.properties (spec §7). nil/empty = default.
// Ops and the whitelist are not here: the server keeps them in ops.json/whitelist.json and
// the panel changes them over RCON (spec 3a §3.1).
```

`internal/agent/instancespec/env.go` – der Block mit `OVERRIDE_SERVER_PROPERTIES` und der Listen-Block werden zu:

```go
	env = append(env,
		"MEMORY="+strconv.Itoa(s.MemoryMiB)+"M",
		"ENABLE_AUTOPAUSE=false",
		"ENABLE_RCON=true",
		"OVERRIDE_SERVER_PROPERTIES=true",
	)
```

und

```go
		"ONLINE_MODE="+b(*p.OnlineMode),
		"ENABLE_WHITELIST="+b(*p.WhitelistEnabled),
	)
	// No OPS/WHITELIST: without them itzg leaves ops.json and whitelist.json alone, so the
	// lists the server keeps (and the panel changes over RCON) survive every recreate.
	env = append(env,
		"VIEW_DISTANCE="+strconv.Itoa(*p.ViewDistance),
```

Ist `strings` in `env.go` danach noch in Gebrauch (`strings.HasPrefix` für CurseForge, `strings.ContainsFunc` am Ende) – ja; Import bleibt.

- [ ] **Step 6: agent – Leeren der Listendateien entfernen, Tests ersetzen**

`internal/agent/instances/datadir.go`: die Funktionen `clearStaleLists` und `emptyListFile` (ab dem Kommentar `// clearStaleLists empties …` bis zum Ende von `emptyListFile`) löschen. `freePath`, `deletedTimeFormat` und der Import `instancespec` bleiben (`archiveData` nutzt sie).

`internal/agent/instances/manager.go` – im Recreate-Pfad die drei Zeilen

```go
	// After the old container is gone, so a still running server cannot write the files back.
	if err := m.clearStaleLists(req.ID, req.Name, req.Spec.Properties); err != nil {
		return err
	}
```

löschen (`m.setOp(req.ID, agentapi.OpCreating)` davor bleibt).

`internal/agent/instances/manager_test.go`: `TestEnsureClearsStaleListFiles` (Kommentar + Funktion) ersetzen durch:

```go
// TestEnsureLeavesListFiles: since sub-project 3a the server owns ops.json and whitelist.json;
// creating and recreating a container must leave them exactly as they are.
func TestEnsureLeavesListFiles(t *testing.T) {
	e := newEnv(t)
	data := instances.DataDir(e.dir, 1)
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	ops := []byte(`[{"uuid":"069a79f4-44e9-4726-a5be-fca90e38aaf5","name":"Notch","level":4}]` + "\n")
	for _, name := range []string{"ops.json", "whitelist.json"} {
		if err := os.WriteFile(filepath.Join(data, name), ops, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.ensure(t, ensureReq(1, "survival", 1))
	req := ensureReq(1, "survival", 2)
	req.Recreate = true
	e.ensure(t, req)
	if s := e.status(t, 1); s.LastError != "" || s.Revision != 2 {
		t.Fatalf("status = %+v", s)
	}
	for _, name := range []string{"ops.json", "whitelist.json"} {
		b, err := os.ReadFile(filepath.Join(data, name))
		if err != nil || string(b) != string(ops) {
			t.Fatalf("%s = %q, %v; want it unchanged", name, b, err)
		}
	}
}
```

`internal/agent/instances/manager_hardening_test.go`: `TestListFileSymlinkIsReplaced` und `TestListFileDirectoryIsMovedAside` (je Kommentar + Funktion) ersetzen durch:

```go
// TestListFilesAreNeverTouched: the agent no longer writes ops.json/whitelist.json, so a
// planted symlink or directory stays as it is and never blocks a recreate.
func TestListFilesAreNeverTouched(t *testing.T) {
	e := newEnv(t)
	data := instances.DataDir(e.dir, 1)
	if err := os.MkdirAll(filepath.Join(data, "whitelist.json"), 0o750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(data, "ops.json")); err != nil {
		t.Fatal(err)
	}
	e.ensure(t, ensureReq(1, "survival", 1))
	if s := e.status(t, 1); s.LastError != "" || s.State != agentapi.StateCreated {
		t.Fatalf("status = %+v, want the container created", s)
	}
	if fi, err := os.Lstat(filepath.Join(data, "ops.json")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("ops.json = %v, %v; want the symlink untouched", fi, err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "keep" {
		t.Fatalf("link target = %q, %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(data, "whitelist.json")); err != nil || !fi.IsDir() {
		t.Fatalf("whitelist.json = %v, %v; want the directory untouched", fi, err)
	}
}
```

Importe der beiden Testdateien bereinigen, falls `errors`/`strings` dort danach ungenutzt sind (`make check` meldet es per `go vet`).

`internal/agent/instances/template_test.go`:

- `paperRequest()`: `Properties: instancespec.Properties{MOTD: ptr("Hallo")},`.
- Golden-Env in `TestInstanceContainerGolden`:

```go
		Env: []string{
			"EULA=TRUE", "TYPE=PAPER", "VERSION=1.21.4", "MEMORY=4096M", "ENABLE_AUTOPAUSE=false",
			"ENABLE_RCON=true", "OVERRIDE_SERVER_PROPERTIES=true",
			"MOTD=Hallo", "DIFFICULTY=normal", "MODE=survival", "MAX_PLAYERS=20", "PVP=true", "HARDCORE=false",
			"ALLOW_FLIGHT=false", "ENABLE_COMMAND_BLOCK=false", "ONLINE_MODE=true", "ENABLE_WHITELIST=false",
			"VIEW_DISTANCE=10", "SIMULATION_DISTANCE=10", "SPAWN_PROTECTION=16",
			"LEVEL_TYPE=minecraft:normal",
		},
```

- `allowedEnvKeys` wie `allowedKeys` in `env_test.go` oben (ohne `EXISTING_OPS_FILE`, `EXISTING_WHITELIST_FILE`, `WHITELIST`, `OPS`).
- `variedRequests()`: `names` samt Schleife löschen; in `full` `Whitelist: names, Ops: names[:50],` löschen (Import `strings` entfernen, falls danach ungenutzt).

`internal/agent/agentapi/instances.go:17-18` – Kommentar:

```go
	// MaxInstanceRequestBytes is the body limit of the /v1/instances/* endpoints (a full
	// spec needs more than the 4 KiB of the firewall endpoints).
```

`internal/agent/server_instances_test.go`:

```go
func TestServerInstanceBodyLimitIs16KiB(t *testing.T) {
	// JSON whitespace pads a valid ensure request to more than the 4 KiB firewall limit.
	body := strings.Replace(ensureBody, `"max_players":10`, `"max_players":10`+strings.Repeat(" ", 6000), 1)
	if len(body) <= 4<<10 || len(body) > 16<<10 {
		t.Fatalf("body has %d bytes; the test needs 4 KiB < body <= 16 KiB", len(body))
	}
	st := &stubInstances{docker: agentapi.DockerOK}
	if rec := serve(newInstanceServer(st), http.MethodPost, agentapi.PathInstanceEnsure, jsonCT, body); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body)
	}
	if len(st.ensured) != 1 || *st.ensured[0].Spec.Properties.MaxPlayers != 10 {
		t.Fatalf("ensured = %+v", st.ensured)
	}
}
```

(`fmt` bleibt nur, falls anderswo in der Datei genutzt – sonst Import entfernen.) In `TestServerRejectsInvalidInstanceRequests` nach `"unknown property"` ergänzen:

```go
		{"ops property (sub-project 3a)", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"Hallo","ops":["Notch"]`), 400},
		{"whitelist property (sub-project 3a)", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"Hallo","whitelist":["Notch"]`), 400},
```

- [ ] **Step 7: panel anpassen**

`internal/servers/view.go`: aus `propertiesJSON` die Felder `Whitelist` und `Ops` löschen und in `resolvedProperties` `Whitelist: r.Whitelist, Ops: r.Ops,` entfernen.

`internal/servers/servers_test.go:162-164` ersetzen durch:

```go
	for _, k := range []string{"whitelist", "ops"} {
		if _, ok := p[k]; ok {
			t.Fatalf("properties must not contain %s since sub-project 3a: %v", k, p)
		}
	}
```

In `TestUpdateServerRejects` zwei Fälle ergänzen:

```go
		"ops property":       {map[string]any{"properties": map[string]any{"ops": []string{"Notch"}}}, 400, "validation_failed"},
		"whitelist property": {map[string]any{"properties": map[string]any{"whitelist": []string{"Notch"}}}, 400, "validation_failed"},
```

- [ ] **Step 8: OpenAPI und generierte Typen**

In `api/openapi.yaml` unter `components.schemas.ServerProperties` die Zeilen `whitelist: …` und `ops: …` löschen und die Beschreibung ergänzen:

```yaml
    ServerProperties:
      type: object
      additionalProperties: false
      description: Allowlisted server.properties. Ops and the whitelist are managed live via /servers/{id}/players (sub-project 3a).
      properties:
```

Run: `make generate`
Expected: `web/src/api/schema.d.ts` ohne `whitelist?:`/`ops?:` in `ServerProperties`; `sqlc generate` ändert nichts.

- [ ] **Step 9: Frontend anpassen (Tests zuerst)**

`web/src/features/servers/validation.test.ts`: `parsePlayerNames` und `validatePlayerNames` aus dem Import entfernen und den Testfall `it('parses and checks player lists', …)` löschen.

`web/src/features/servers/form-model.test.ts`:

- In der Fixture `survival` (Instanz-Properties, Zeilen 50–51) `whitelist: ['Notch', 'jeb_'],` und `ops: [],` löschen.
- Im erwarteten Ergebnis von `'resolves a new paper server …'` `whitelist: [],` und `ops: [],` löschen.
- Den Test `'validates properties and parses player lists'` ersetzen durch:

```ts
  it('validates properties', () => {
    const value = form({ properties: { ...emptyServerForm().properties, max_players: '0', motd: 'a\nb' } })
    expect(Object.keys(validateServerForm(value, ctx('create', paperCatalog))).sort()).toEqual(['properties.max_players', 'properties.motd'])
    const fixed = { ...value, properties: { ...value.properties, max_players: '8', motd: 'Hallo' } }
    const properties = resolveServerForm(fixed, ctx('create', paperCatalog))?.properties
    expect(properties).toMatchObject({ motd: 'Hallo', max_players: 8 })
    expect(properties).not.toHaveProperty('whitelist')
    expect(properties).not.toHaveProperty('ops')
  })
```

`web/src/features/servers/create-server-page.test.tsx` – im Test `'sends edited properties and warns when online mode is off'` den Hinweis-Text und die Whitelist-Eingabe ersetzen:

```tsx
    expect(screen.getByText('Whitelist, Operatoren und Bans verwaltest du im Tab „Spieler“ der Serverseite; Änderungen dort wirken sofort.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Whitelist')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    await waitFor(() => expect(createdInstance('props')?.properties).toMatchObject({ motd: 'Hallo', pvp: false, online_mode: false }))
```

`web/src/features/servers/server-detail-page.test.tsx:9-10`:

```ts
const LISTS_HINT = 'Whitelist, Operatoren und Bans verwaltest du im Tab „Spieler“ der Serverseite; Änderungen dort wirken sofort.'
```

- [ ] **Step 10: Frontend umsetzen**

`web/src/features/servers/validation.ts`: `MAX_WHITELIST`, `MAX_OPS`, `PLAYER_RE`, `parsePlayerNames` und `validatePlayerNames` (samt Doc-Kommentar) löschen.

`web/src/features/servers/form-model.ts`:

- Importe `MAX_OPS`, `MAX_WHITELIST`, `parsePlayerNames`, `validatePlayerNames` löschen.
- Doc-Kommentar von `PropertiesDraft`: `/** Properties as edited in the form: numbers stay text until they are valid. */`; Felder `whitelist: string` und `ops: string` löschen.
- In `PROPERTY_FIELDS` `'properties.whitelist',` und `'properties.ops',` löschen.
- In `defaultPropertiesDraft()` `whitelist: '',` und `ops: '',` löschen.
- In `formFromInstance` die Zeilen `whitelist: (p.whitelist ?? []).join('\n'),` und `ops: (p.ops ?? []).join('\n'),` löschen.
- In `validateServerForm` die beiden `check('properties.whitelist' …)`/`check('properties.ops' …)` löschen.
- In `resolveServerForm` `whitelist: parsePlayerNames(p.whitelist).names,` und `ops: parsePlayerNames(p.ops).names,` löschen.

`web/src/features/servers/properties-fields.tsx`: die Konstante `TEXTAREA_CLASS` samt Kommentar `// Same look as the shadcn Input; there is no shadcn textarea in this project.` löschen und den Block

```tsx
      <div className="grid gap-4 sm:grid-cols-2">
        <FieldRow id={`${idPrefix}-whitelist`} …>
        …
        </FieldRow>
        <FieldRow id={`${idPrefix}-ops`} …>
        …
        </FieldRow>
      </div>
      <p className="text-muted-foreground text-sm">{t('servers.props.lists_sync_hint')}</p>
```

ersetzen durch

```tsx
      <p className="text-muted-foreground text-sm">{t('servers.props.lists_hint')}</p>
```

`web/src/mocks/db.ts` – in `resolveProperties` `whitelist: [],` und `ops: [],` löschen.

`web/src/i18n/de.ts` – unter `servers.props` die Schlüssel `whitelist`, `whitelist_hint`, `ops`, `ops_hint` löschen und `lists_sync_hint` ersetzen durch:

```ts
      lists_hint: 'Whitelist, Operatoren und Bans verwaltest du im Tab „Spieler“ der Serverseite; Änderungen dort wirken sofort.',
```

unter `servers.validation` `players_invalid` und `players_too_many` löschen.

`web/src/i18n/en.ts` entsprechend:

```ts
      lists_hint: 'The whitelist, operators and bans are managed in the “Players” tab of the server page; changes there apply immediately.',
```

(`whitelist`, `whitelist_hint`, `ops`, `ops_hint`, `players_invalid`, `players_too_many` löschen.)

- [ ] **Step 11: Prüfen**

Run: `make check`
Expected: PASS (gofmt, vet, Build macOS/Linux, `tsc`).

Erneut grep aus dem Kopf dieser Aufgabe ausführen.
Expected: keine Treffer mehr außer `whitelist_enabled`/`WhitelistEnabled`/`ENABLE_WHITELIST` und dem neuen Test `TestListKeysAreUnknown`/`TestEnvNeverSendsPlayerLists`/den neuen 400-Fällen.

- [ ] **Step 12: Commit, Push, CI**

```bash
git add internal/store internal/agent internal/servers api/openapi.yaml web/src
git commit -m "feat!: ops and the whitelist belong to the server, not to the panel properties

Migration 00004 drops ops/whitelist from instances.properties and bumps the revision of the
affected instances; containers no longer get OPS/WHITELIST/EXISTING_*_FILE, and the agent no
longer empties ops.json/whitelist.json. The form drops both lists (sub-project 3a, spec §3.1).

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -u origin feat/console-contracts
gh pr create --draft --title "TP3a Phase A: Verträge für Konsole und Spieler" --body "Aufgaben 1–3 aus docs/superpowers/plans/2026-10-11-console-01-contracts.md"
gh pr checks --watch
```

Expected: alle Jobs grün (`go test ./...` inkl. `TestMigrationPlayersInGame`, `TestEnvNeverSendsPlayerLists`, `TestEnsureLeavesListFiles`, `TestListFilesAreNeverTouched`; Vitest; Playwright).

---

### Task 2: agentapi-Vertrag und Client für die Konsole

**Files:**
- Create: `internal/agent/agentapi/console.go`
- Create: `internal/agent/agentapi/console_test.go`
- Create: `internal/agent/client_console.go`
- Create: `internal/agent/client_console_test.go`
- Create: `internal/agent/export_test.go`
- Modify: `internal/agent/agentapi/agentapi.go:46-49` (Kommentar von `ErrorResponse.Code`)
- Modify: `internal/agent/client.go` (`StatusError.Is`, `Client`-Feld `logsHeaderTimeout`, Hilfsfunktionen `newRequest`, `doTimeout`, `statusError`)

**Interfaces:**
- Consumes: `instancespec.ErrInvalid`; bestehende `agentapi.ErrUnavailable`, `ErrDockerUnavailable`, `ErrBusy`, `ErrorResponse`.
- Produces: alles aus Übersicht V1 (Pfade, Codes, Limits, Sentinels, `LogsRequest`, `LogLine`, `RconRequest`, `RconResponse`, `ValidateCommand`, `PlayersRequest`, `PlayerOp`, `PlayerEntry`, `PlayerBan`, `PlayersResponse`, `LogStream`, `ConsoleClient`), zusätzlich `func (r PlayersRequest) Validate() error` (id > 0); `agent.RconTimeout = 15 * time.Second`, `agent.PlayersTimeout = 5 * time.Second`, `agent.LogsHeaderTimeout = 5 * time.Second`; `agentapi.HeaderContainerRunning = "X-Container-Running"`; `LogStream.Running() bool` (Zustand des Containers beim Öffnen, aus dem Antwort-Header des agents – fehlend oder anderer Wert als `true` → `false`); `(*agent.Client).Logs/Rcon/Players`. Fehlerverhalten: Validierungsfehler wrappen `agent.ErrInvalid` **und** `instancespec.ErrInvalid`; ein `503 rcon_unavailable` passt (wie `docker_unavailable`) zusätzlich auf `agentapi.ErrUnavailable` – Aufrufer prüfen die spezifischen Fehler zuerst; `404 not_found` passt zusätzlich auf `ErrInvalid` (bestehende 4xx-Regel), `409 not_running` zusätzlich auf `agent.ErrConflict`.

- [ ] **Step 1: Tests für den Vertrag schreiben (rot)**

`internal/agent/agentapi/console_test.go`:

```go
package agentapi_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func TestValidateCommand(t *testing.T) {
	valid := []string{
		"list",
		"say Hallo Welt äöü §aGrün",
		"/op Notch", // the panel strips one leading slash; the agent accepts it as text
		"tp @a ~ ~10 ~",
		strings.Repeat("a", agentapi.MaxCommandBytes),
		strings.Repeat("ä", agentapi.MaxCommandBytes/2), // 1000 bytes
		" list ",
	}
	for _, c := range valid {
		if err := agentapi.ValidateCommand(c); err != nil {
			t.Errorf("%q: %v", c, err)
		}
	}
	invalid := map[string]string{
		"empty":         "",
		"too long":      strings.Repeat("a", agentapi.MaxCommandBytes+1),
		"too long utf8": strings.Repeat("ä", agentapi.MaxCommandBytes/2+1),
		"newline":       "say a\nstop",
		"carriage ret":  "say a\rstop",
		"tab":           "say\ta",
		"nul":           "say a\x00b",
		"escape":        "say \x1b[31mred",
		"del":           "say a\x7f",
		"invalid utf8":  "say \xff\xfe",
		"only spaces":   "   ",
	}
	for name, c := range invalid {
		if err := agentapi.ValidateCommand(c); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestConsoleRequestsValidate(t *testing.T) {
	good := []interface{ Validate() error }{
		agentapi.LogsRequest{ID: 1},
		agentapi.LogsRequest{ID: 1, Tail: agentapi.MaxLogTail},
		agentapi.LogsRequest{ID: 1, Since: 1_760_000_000_000_000_000},
		agentapi.RconRequest{ID: 1, Command: "list"},
		agentapi.PlayersRequest{ID: 1},
	}
	for i, r := range good {
		if err := r.Validate(); err != nil {
			t.Errorf("good[%d] %+v: %v", i, r, err)
		}
	}
	bad := map[string]interface{ Validate() error }{
		"logs id 0":      agentapi.LogsRequest{},
		"logs tail -1":   agentapi.LogsRequest{ID: 1, Tail: -1},
		"logs tail 1001": agentapi.LogsRequest{ID: 1, Tail: agentapi.MaxLogTail + 1},
		"logs since -1":  agentapi.LogsRequest{ID: 1, Since: -1},
		"rcon id 0":      agentapi.RconRequest{Command: "list"},
		"rcon command":   agentapi.RconRequest{ID: 1, Command: "a\nb"},
		"players id 0":   agentapi.PlayersRequest{},
	}
	for name, r := range bad {
		if err := r.Validate(); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}
```

`internal/agent/export_test.go`:

```go
package agent

import "time"

// SetLogsHeaderTimeout shortens the wait for the logs response headers in tests.
func SetLogsHeaderTimeout(c *Client, d time.Duration) { c.logsHeaderTimeout = d }
```

`internal/agent/client_console_test.go` (nutzt `handlerServer` aus `client_instances_test.go`):

```go
package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

var _ agentapi.ConsoleClient = (*agent.Client)(nil)

func writeLines(w http.ResponseWriter, lines ...string) {
	for _, l := range lines {
		_, _ = io.WriteString(w, l+"\n")
	}
	w.(http.Flusher).Flush()
}

func TestClientLogsStreamsLines(t *testing.T) {
	var got agentapi.LogsRequest
	var ct string
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != agentapi.PathInstanceLogs {
			http.Error(w, "wrong route", http.StatusNotFound)
			return
		}
		ct = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set(agentapi.HeaderContainerRunning, "true")
		writeLines(w,
			`{"ts":1760000000000000001,"stream":"out","line":"[12:00:00 INFO]: Done (8.9s)!"}`,
			``,
			`{"ts":1760000000000000002,"stream":"err","line":"oops"}`)
	}))
	s, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 7, Tail: 500})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.Running() {
		t.Fatal("Running() = false, want true from the header")
	}
	l1, err := s.Next()
	if err != nil || l1.TS != 1760000000000000001 || l1.Stream != agentapi.StreamOut || !strings.Contains(l1.Line, "Done") {
		t.Fatalf("line 1 = %+v, %v", l1, err)
	}
	l2, err := s.Next()
	if err != nil || l2.Stream != agentapi.StreamErr || l2.Line != "oops" {
		t.Fatalf("line 2 = %+v, %v", l2, err)
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end = %v, want io.EOF", err)
	}
	if got != (agentapi.LogsRequest{ID: 7, Tail: 500}) || ct != "application/json" {
		t.Fatalf("request = %+v, content type %q", got, ct)
	}
	if err := s.Close(); err != nil { // idempotent
		t.Fatal(err)
	}
}

// The client hands out each line as it arrives and Close ends the request on the agent side.
func TestClientLogsIsLiveAndCloseCancels(t *testing.T) {
	done := make(chan struct{})
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeLines(w, `{"ts":1,"stream":"out","line":"first"}`)
		<-r.Context().Done()
		close(done)
	}))
	s, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if l, err := s.Next(); err != nil || l.Line != "first" {
		t.Fatalf("first = %+v, %v", l, err)
	}
	_ = s.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel the agent request")
	}
}

// Running comes from X-Container-Running; anything but "true" (including a missing header) is false.
func TestClientLogsRunningHeader(t *testing.T) {
	for _, tc := range []struct {
		header string // "" = not set
		want   bool
	}{
		{"true", true},
		{"false", false},
		{"", false},
		{"yes", false},
	} {
		path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tc.header != "" {
				w.Header().Set(agentapi.HeaderContainerRunning, tc.header)
			}
			w.WriteHeader(http.StatusOK)
		}))
		s, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 1})
		if err != nil {
			t.Fatalf("header %q: %v", tc.header, err)
		}
		if got := s.Running(); got != tc.want {
			t.Errorf("header %q: Running() = %v, want %v", tc.header, got, tc.want)
		}
		if _, err := s.Next(); !errors.Is(err, io.EOF) {
			t.Errorf("header %q: empty stream must end with io.EOF, got %v", tc.header, err)
		}
		_ = s.Close()
	}
}

func TestClientLogsHeaderTimeout(t *testing.T) {
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // never answers
	}))
	c := agent.NewClient(path)
	agent.SetLogsHeaderTimeout(c, 50*time.Millisecond)
	start := time.Now()
	_, err := c.Logs(context.Background(), agentapi.LogsRequest{ID: 1})
	if !errors.Is(err, agentapi.ErrUnavailable) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
}

func TestClientLogsBrokenStream(t *testing.T) {
	for name, body := range map[string]string{
		"oversized line":  strings.Repeat("x", 70<<10) + "\n",
		"not json":        "hello\n",
		"truncated line":  `{"ts":1,"stream":"out","line":"cut`,
	} {
		path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		s, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 1})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := s.Next(); !errors.Is(err, agentapi.ErrUnavailable) {
			t.Errorf("%s: Next = %v, want ErrUnavailable", name, err)
		}
		_ = s.Close()
	}
}

func TestClientRconAndPlayers(t *testing.T) {
	var rcon agentapi.RconRequest
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case agentapi.PathInstanceRcon:
			_ = json.NewDecoder(r.Body).Decode(&rcon)
			_ = json.NewEncoder(w).Encode(agentapi.RconResponse{Output: "There are 0 of a max of 20 players online: "})
		case agentapi.PathInstancePlayers:
			_, _ = io.WriteString(w, `{"ops":[{"uuid":"u1","name":"Notch","level":4}],"whitelist":null,"bans":null,"errors":["banned-players.json"]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	c := agent.NewClient(path)
	ctx := context.Background()
	out, err := c.Rcon(ctx, 3, "list uuids")
	if err != nil || !strings.HasPrefix(out, "There are 0") || rcon != (agentapi.RconRequest{ID: 3, Command: "list uuids"}) {
		t.Fatalf("Rcon = %q, %v (sent %+v)", out, err, rcon)
	}
	p, err := c.Players(ctx, 3)
	if err != nil || len(p.Ops) != 1 || p.Ops[0].Level != 4 || p.Whitelist == nil || p.Bans == nil ||
		len(p.Errors) != 1 || p.Errors[0] != "banned-players.json" {
		t.Fatalf("Players = %+v, %v", p, err)
	}
}

func TestClientConsoleValidatesBeforeSending(t *testing.T) {
	calls := 0
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++ }))
	c := agent.NewClient(path)
	ctx := context.Background()
	if _, err := c.Rcon(ctx, 1, "say a\nstop"); !errors.Is(err, agent.ErrInvalid) || !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("Rcon newline: %v", err)
	}
	if _, err := c.Rcon(ctx, 0, "list"); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("Rcon id 0: %v", err)
	}
	if _, err := c.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 1001}); !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("Logs tail: %v", err)
	}
	if _, err := c.Players(ctx, 0); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("Players id 0: %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached the agent (%d calls)", calls)
	}
}

func TestClientConsoleErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   error
		not    error
	}{
		{http.StatusNotFound, `{"error":"no container","code":"not_found"}`, agentapi.ErrNotFound, agentapi.ErrNotRunning},
		{http.StatusConflict, `{"error":"stopped","code":"not_running"}`, agentapi.ErrNotRunning, agentapi.ErrNotFound},
		{http.StatusServiceUnavailable, `{"error":"rcon: connection refused","code":"rcon_unavailable"}`, agentapi.ErrRconUnavailable, agentapi.ErrDockerUnavailable},
		{http.StatusServiceUnavailable, `{"error":"docker down","code":"docker_unavailable"}`, agentapi.ErrDockerUnavailable, agentapi.ErrRconUnavailable},
		{http.StatusConflict, `{"error":"other conflict"}`, agent.ErrConflict, agentapi.ErrNotRunning},
		{http.StatusTooManyRequests, `{"error":"busy","code":"busy"}`, agentapi.ErrBusy, agentapi.ErrNotRunning},
	} {
		path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		c := agent.NewClient(path)
		_, rconErr := c.Rcon(context.Background(), 1, "list")
		_, logsErr := c.Logs(context.Background(), agentapi.LogsRequest{ID: 1})
		for _, err := range []error{rconErr, logsErr} {
			if !errors.Is(err, tc.want) || errors.Is(err, tc.not) {
				t.Errorf("%d %s: got %v, want %v and not %v", tc.status, tc.body, err, tc.want, tc.not)
			}
		}
	}
}
```

- [ ] **Step 2: `make check` – rot**

Run: `make check`
Expected: FAIL – `agentapi.ValidateCommand`, `agentapi.LogsRequest`, `agent.SetLogsHeaderTimeout` … undefiniert.

- [ ] **Step 3: `internal/agent/agentapi/console.go` schreiben**

```go
package agentapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// Console endpoints (sub-project 3a): container log stream, RCON commands and player lists.
const (
	PathInstanceLogs    = "/v1/instances/logs"
	PathInstanceRcon    = "/v1/instances/rcon"
	PathInstancePlayers = "/v1/instances/players"

	ErrorCodeNotFound        = "not_found"        // 404: container or data directory missing
	ErrorCodeNotRunning      = "not_running"      // 409: rcon on a container that is not running
	ErrorCodeRconUnavailable = "rcon_unavailable" // 503: rcon-cli exited non-zero

	MaxLogTail         = 1000
	MaxCommandBytes    = 1000
	MaxLogLineBytes    = 8 << 10
	MaxRconOutputBytes = 64 << 10

	StreamOut = "out"
	StreamErr = "err"

	// HeaderContainerRunning is set by the logs endpoint on its 200 answer: "true" or "false",
	// the container state when the stream was opened.
	HeaderContainerRunning = "X-Container-Running"
)

var (
	// ErrNotFound: the container (logs, rcon) or the data directory (players) does not exist.
	ErrNotFound = errors.New("not found")
	// ErrNotRunning: RCON needs a running container.
	ErrNotRunning = errors.New("instance not running")
	// ErrRconUnavailable: rcon-cli failed, e.g. while the server is still starting.
	ErrRconUnavailable = errors.New("rcon unavailable")
)

// LogsRequest asks for the container log. Since > 0 (Docker timestamp in ns) wins over Tail.
type LogsRequest struct {
	ID    int64 `json:"id"`
	Tail  int   `json:"tail"`
	Since int64 `json:"since"`
}

func (r LogsRequest) Validate() error {
	switch {
	case r.ID <= 0:
		return fmt.Errorf("%w: id: must be > 0", instancespec.ErrInvalid)
	case r.Tail < 0 || r.Tail > MaxLogTail:
		return fmt.Errorf("%w: tail: must be 0..%d", instancespec.ErrInvalid, MaxLogTail)
	case r.Since < 0:
		return fmt.Errorf("%w: since: must be >= 0", instancespec.ErrInvalid)
	}
	return nil
}

// LogLine is one NDJSON line of the logs stream.
type LogLine struct {
	TS     int64  `json:"ts"`     // Docker timestamp, ns since epoch
	Stream string `json:"stream"` // StreamOut | StreamErr
	Line   string `json:"line"`   // without timestamp and trailing \r\n, <= MaxLogLineBytes, valid UTF-8
}

type RconRequest struct {
	ID      int64  `json:"id"`
	Command string `json:"command"`
}

func (r RconRequest) Validate() error {
	if r.ID <= 0 {
		return fmt.Errorf("%w: id: must be > 0", instancespec.ErrInvalid)
	}
	return ValidateCommand(r.Command)
}

type RconResponse struct {
	Output string `json:"output"`
}

// ValidateCommand checks a console command (shared by panel and agent): 1..MaxCommandBytes
// bytes, valid UTF-8, no control character (< 0x20, so no newline or tab, and no 0x7f), not
// only spaces. rcon-cli gets it as one argv element, never through a shell.
func ValidateCommand(cmd string) error {
	switch {
	case len(cmd) == 0 || len(cmd) > MaxCommandBytes:
		return fmt.Errorf("%w: command: must be 1..%d bytes", instancespec.ErrInvalid, MaxCommandBytes)
	case !utf8.ValidString(cmd):
		return fmt.Errorf("%w: command: must be valid UTF-8", instancespec.ErrInvalid)
	case strings.ContainsFunc(cmd, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return fmt.Errorf("%w: command: must not contain control characters", instancespec.ErrInvalid)
	case strings.Trim(cmd, " ") == "":
		return fmt.Errorf("%w: command: must not be blank", instancespec.ErrInvalid)
	}
	return nil
}

type PlayersRequest struct {
	ID int64 `json:"id"`
}

func (r PlayersRequest) Validate() error {
	if r.ID <= 0 {
		return fmt.Errorf("%w: id: must be > 0", instancespec.ErrInvalid)
	}
	return nil
}

type PlayerOp struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	Level int    `json:"level"`
}

type PlayerEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type PlayerBan struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
	Source  string `json:"source"`
	Created string `json:"created"`
	Expires string `json:"expires"`
}

// PlayersResponse: slices are never nil (JSON []); Errors lists unreadable file names
// ("ops.json", "whitelist.json", "banned-players.json").
type PlayersResponse struct {
	Ops       []PlayerOp    `json:"ops"`
	Whitelist []PlayerEntry `json:"whitelist"`
	Bans      []PlayerBan   `json:"bans"`
	Errors    []string      `json:"errors"`
}

// LogStream is an open logs stream. Next returns io.EOF when the agent ended the stream
// (container stopped/removed); any other error means the stream broke. Close is idempotent.
// Running reports whether the container was running when the stream was opened (header
// HeaderContainerRunning; a missing or other value counts as false).
type LogStream interface {
	Next() (LogLine, error)
	Close() error
	Running() bool
}

// ConsoleClient is the panel-side view of the console endpoints (implemented by *agent.Client).
type ConsoleClient interface {
	Logs(ctx context.Context, req LogsRequest) (LogStream, error)
	Rcon(ctx context.Context, id int64, command string) (string, error)
	Players(ctx context.Context, id int64) (PlayersResponse, error)
}
```

In `internal/agent/agentapi/agentapi.go` den Feldkommentar anpassen:

```go
	Code  string `json:"code,omitempty"` // ErrorCodeBusy | ErrorCodeDockerUnavailable | ErrorCodeNotFound | ErrorCodeNotRunning | ErrorCodeRconUnavailable
```

- [ ] **Step 4: `client.go` umbauen**

In `internal/agent/client.go`:

1. `StatusError.Is` – vor dem bestehenden `switch` einfügen:

```go
func (e *StatusError) Is(target error) bool {
	// Console codes first: they are more specific than the status-based rules below.
	switch target {
	case agentapi.ErrNotFound:
		return e.StatusCode == http.StatusNotFound && e.Code == agentapi.ErrorCodeNotFound
	case agentapi.ErrNotRunning:
		return e.StatusCode == http.StatusConflict && e.Code == agentapi.ErrorCodeNotRunning
	case agentapi.ErrRconUnavailable:
		return e.StatusCode == http.StatusServiceUnavailable && e.Code == agentapi.ErrorCodeRconUnavailable
	}
	switch {
	case target == agentapi.ErrDockerUnavailable:
	// … unverändert …
```

und den Doc-Kommentar ergänzen: `// The console codes not_found (404), not_running (409) and rcon_unavailable (503) additionally match agentapi.ErrNotFound, ErrNotRunning and ErrRconUnavailable.`

2. `Client` bekommt das Feld `logsHeaderTimeout time.Duration`; in `NewClientWithTimeout`:

```go
	return &Client{
		hc: &http.Client{ /* unverändert */ },
		timeout:           timeout,
		logsHeaderTimeout: LogsHeaderTimeout,
	}
```

3. `do` aufteilen (Verhalten unverändert):

```go
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	return c.doTimeout(ctx, c.timeout, method, path, in, out)
}

func (c *Client) doTimeout(ctx context.Context, timeout time.Duration, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := newRequest(ctx, method, path, in)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", agentapi.ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read response: %w", agentapi.ErrUnavailable, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("%w: response exceeds %d bytes", agentapi.ErrUnavailable, maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return statusError(resp.StatusCode, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: decode response: %w", agentapi.ErrUnavailable, err)
	}
	return nil
}

// newRequest builds a JSON request to the agent (body only if in != nil).
func newRequest(ctx context.Context, method, path string, in any) (*http.Request, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("agent: encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("agent: build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// statusError turns a non-2xx answer into a *StatusError (message and code from the body).
func statusError(status int, data []byte) *StatusError {
	msg := http.StatusText(status)
	var er agentapi.ErrorResponse
	if json.Unmarshal(data, &er) == nil && er.Error != "" {
		msg = er.Error
	}
	return &StatusError{StatusCode: status, Message: msg, Code: er.Code}
}
```

- [ ] **Step 5: `internal/agent/client_console.go` schreiben**

```go
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

const (
	// RconTimeout covers the agent's own 10 s exec timeout plus the round trip.
	RconTimeout = 15 * time.Second
	// PlayersTimeout bounds reading the three small list files.
	PlayersTimeout = 5 * time.Second
	// LogsHeaderTimeout bounds the wait for the logs response headers; the stream itself
	// has no deadline (it ends with ctx or with the container).
	LogsHeaderTimeout = 5 * time.Second
	// maxLogLineJSON bounds one NDJSON line (an 8 KiB log line can grow by JSON escaping).
	maxLogLineJSON = 64 << 10
)

var _ agentapi.ConsoleClient = (*Client)(nil)

func (c *Client) Logs(ctx context.Context, req agentapi.LogsRequest) (agentapi.LogStream, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	hreq, err := newRequest(ctx, http.MethodPost, agentapi.PathInstanceLogs, req)
	if err != nil {
		cancel()
		return nil, err
	}
	hreq.Header.Set("Accept", "application/x-ndjson")
	timer := time.AfterFunc(c.logsHeaderTimeout, cancel)
	resp, err := c.hc.Do(hreq)
	if !timer.Stop() { // the timer fired: ctx is cancelled
		if err == nil {
			_ = resp.Body.Close()
		}
		cancel()
		return nil, fmt.Errorf("%w: logs: no response within %s", agentapi.ErrUnavailable, c.logsHeaderTimeout)
	}
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%w: %w", agentapi.ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer cancel()
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		return nil, statusError(resp.StatusCode, data)
	}
	return &logStream{
		r: bufio.NewReader(resp.Body), body: resp.Body, cancel: cancel,
		running: resp.Header.Get(agentapi.HeaderContainerRunning) == "true",
	}, nil
}

func (c *Client) Rcon(ctx context.Context, id int64, command string) (string, error) {
	req := agentapi.RconRequest{ID: id, Command: command}
	if err := req.Validate(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var out agentapi.RconResponse
	if err := c.doTimeout(ctx, RconTimeout, http.MethodPost, agentapi.PathInstanceRcon, req, &out); err != nil {
		return "", err
	}
	return out.Output, nil
}

func (c *Client) Players(ctx context.Context, id int64) (agentapi.PlayersResponse, error) {
	req := agentapi.PlayersRequest{ID: id}
	if err := req.Validate(); err != nil {
		return agentapi.PlayersResponse{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var out agentapi.PlayersResponse
	if err := c.doTimeout(ctx, PlayersTimeout, http.MethodPost, agentapi.PathInstancePlayers, req, &out); err != nil {
		return agentapi.PlayersResponse{}, err
	}
	if out.Ops == nil {
		out.Ops = []agentapi.PlayerOp{}
	}
	if out.Whitelist == nil {
		out.Whitelist = []agentapi.PlayerEntry{}
	}
	if out.Bans == nil {
		out.Bans = []agentapi.PlayerBan{}
	}
	if out.Errors == nil {
		out.Errors = []string{}
	}
	return out, nil
}

// logStream reads NDJSON lines from an open logs response.
type logStream struct {
	r       *bufio.Reader
	body    io.Closer
	cancel  context.CancelFunc
	once    sync.Once
	running bool
}

func (s *logStream) Running() bool { return s.running }

func (s *logStream) Next() (agentapi.LogLine, error) {
	for {
		raw, err := readLine(s.r, maxLogLineJSON)
		if errors.Is(err, io.EOF) {
			return agentapi.LogLine{}, io.EOF
		}
		if err != nil {
			return agentapi.LogLine{}, fmt.Errorf("%w: logs: %w", agentapi.ErrUnavailable, err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var l agentapi.LogLine
		if err := json.Unmarshal(raw, &l); err != nil {
			return agentapi.LogLine{}, fmt.Errorf("%w: logs: decode line: %w", agentapi.ErrUnavailable, err)
		}
		return l, nil
	}
}

func (s *logStream) Close() error {
	s.once.Do(func() {
		s.cancel()
		_ = s.body.Close()
	})
	return nil
}

// readLine returns the next '\n'-terminated line without the newline. It returns io.EOF only
// at a clean end; a last line without newline is io.ErrUnexpectedEOF, a line over limit bytes
// an error.
func readLine(r *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > limit+1 {
			return nil, fmt.Errorf("line exceeds %d bytes", limit)
		}
		line = append(line, chunk...)
		switch {
		case err == nil:
			return line[:len(line)-1], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) == 0:
			return nil, io.EOF
		case errors.Is(err, io.EOF):
			return nil, io.ErrUnexpectedEOF
		default:
			return nil, err
		}
	}
}
```

Validierungsfehler tragen beide Sentinels: `ErrInvalid` (aus `client.go`, = `banrules.ErrInvalid`) wird vorangestellt, `req.Validate()` liefert `instancespec.ErrInvalid`.

- [ ] **Step 6: Prüfen**

Run: `make check`
Expected: PASS.

- [ ] **Step 7: Commit, Push, CI**

```bash
git add internal/agent
git commit -m "feat(agent): agentapi contract and client for logs, RCON and player lists

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: grün, insbesondere `TestValidateCommand`, `TestConsoleRequestsValidate`, `TestClientLogs*`, `TestClientRconAndPlayers`, `TestClientConsole*` (auch unter `-race` im Job `test-race`).

---

### Task 3: panel-Vertrag – OpenAPI, Fehlercodes, Audit, Stub-Handler, MSW-Mocks

**Files:**
- Modify: `internal/httpx/errors.go` (zwei Codes)
- Modify: `internal/audit/types.go` (zwei Aktionen)
- Modify: `api/openapi.yaml` (Tag, 5 Operationen, Schemas, Error-Enum, 503-Beschreibung); generiert: `web/src/api/schema.d.ts`
- Create: `internal/console/handler.go`
- Create: `internal/console/handler_test.go`
- Modify: `cmd/panel/app.go` (`appDeps.Console`, Handler verdrahten, `Close`)
- Modify: `cmd/panel/main.go:235` (`Console: ac`)
- Modify: `web/src/api/types.ts` (Re-Exports, `ConsoleLineEvent`, `ConsoleStateEvent`)
- Modify: `web/src/api/errors.ts` (`KNOWN_CODES`)
- Modify: `web/src/i18n/de.ts`, `web/src/i18n/en.ts` (`errors.instance_not_running`, `errors.rcon_unavailable`), `web/src/i18n/i18n.test.ts` (`ERROR_CODES`)
- Create: `web/src/mocks/handlers/console.ts`
- Modify: `web/src/mocks/db.ts` (`MockPlayers`, `MockServer.players`, Seed), `web/src/mocks/handlers/servers.ts` (neue Server bekommen `players`), `web/src/mocks/handlers.ts`, `web/src/mocks/handlers.test.ts`

**Interfaces:**
- Consumes: `agentapi.ConsoleClient` (Aufgabe 2), `rbac.Router`, `httpx.WriteError`, `audit.Recorder`, `principal.Principal`.
- Produces: `httpx.CodeInstanceNotRunning = "instance_not_running"`, `httpx.CodeRconUnavailable = "rcon_unavailable"`; `audit.ActionServerConsoleCommand = "server.console_command"`, `audit.ActionServerPlayerAction = "server.player_action"`; Paket `console` mit `Deps`, `Access`, `Handler`, `NewHandler`, `Routes`, `Shutdown` exakt nach V4 (`Deps.Access == nil` bedeutet „alles verweigern“: B2 behandelt einen fehlenden `Access` wie eine fehlgeschlagene erneute Prüfung); Handler-Methoden `handleStream`, `handleCommand`, `handleOnlinePlayers`, `handlePlayerLists`, `handlePlayerAction` (B2 ersetzt ihre Rümpfe); `appDeps.Console agentapi.ConsoleClient`; TS-Typen `ConsoleCommandRequest`, `ConsoleCommandResponse`, `OnlinePlayer`, `OnlinePlayers`, `PlayerOp`, `PlayerEntry`, `PlayerBan`, `PlayerLists`, `PlayerListFile`, `PlayerAction`, `PlayerActionRequest`, `ConsoleLineEvent`, `ConsoleStateEvent`; Mock-Typ `MockPlayers` und `MockServer.players`; MSW-Operationen nach V5 (mit der Abweichung oben).

- [ ] **Step 1: Go-Tests schreiben (rot)**

`internal/console/handler_test.go`:

```go
package console_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// wantRoutes are exactly the console operations of api/openapi.yaml with their rules (V3/V4).
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers/{id}/console/stream":    rbac.Server(rbac.PermServerConsole, "id"),
	"POST /api/servers/{id}/console":          rbac.Server(rbac.PermServerConsole, "id"),
	"GET /api/servers/{id}/players":           rbac.Server(rbac.PermServerView, "id"),
	"GET /api/servers/{id}/players/lists":     rbac.Server(rbac.PermServerConsole, "id"),
	"POST /api/servers/{id}/players/actions":  rbac.Server(rbac.PermServerConsole, "id"),
}

func newRouter(t *testing.T) (*chi.Mux, []rbac.RouteInfo) {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		console.NewHandler(console.Deps{DB: db, Clock: clk, Audit: &audit.MemoryRecorder{}}).Routes(api)
	})
	mux.Handle("/*", http.NotFoundHandler())
	return mux, rt.Routes()
}

func TestRouteContract(t *testing.T) {
	mux, got := newRouter(t)
	if len(got) != len(wantRoutes) {
		t.Errorf("registered %d routes, want %d", len(got), len(wantRoutes))
	}
	seen := map[string]bool{}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		seen[key] = true
		w, ok := wantRoutes[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
	for key := range wantRoutes {
		if !seen[key] {
			t.Errorf("missing route %s", key)
		}
	}
	if un := rbac.Uncovered(mux, got, func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

// Until phase B2 every console route answers 503 agent_unavailable (V4).
func TestStubsAnswerAgentUnavailable(t *testing.T) {
	mux, _ := newRouter(t)
	admin := principal.Principal{UserID: 1, Username: "admin", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/servers/1/console/stream"},
		{http.MethodPost, "/api/servers/1/console"},
		{http.MethodGet, "/api/servers/1/players"},
		{http.MethodGet, "/api/servers/1/players/lists"},
		{http.MethodPost, "/api/servers/1/players/actions"},
	} {
		req := httptest.NewRequestWithContext(principal.With(context.Background(), admin), c.method, c.path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var body httpx.ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusServiceUnavailable || body.Error != httpx.CodeAgentUnavailable {
			t.Errorf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

func TestShutdownWithoutStreams(t *testing.T) {
	console.NewHandler(console.Deps{Clock: clock.NewFake(t0), Audit: &audit.MemoryRecorder{}}).Shutdown()
}
```

- [ ] **Step 2: Fehlercodes und Audit-Aktionen**

`internal/httpx/errors.go` – nach `CodeCatalogUnavailable`:

```go
	CodeInstanceNotRunning = "instance_not_running"
	CodeRconUnavailable    = "rcon_unavailable"
```

`internal/audit/types.go` – nach `ActionServerSettingsUpdate`:

```go
	ActionServerConsoleCommand = "server.console_command"
	ActionServerPlayerAction   = "server.player_action"
```

(`gofmt` richtet die Konstantenblöcke aus.)

- [ ] **Step 3: `internal/console/handler.go` (Stub nach V4)**

```go
// Package console implements the live console of a Minecraft server (sub-project 3a): the log
// stream (SSE), RCON commands and player management. The agent does all Docker and file work.
package console

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

// Deps are the dependencies of the console handlers.
type Deps struct {
	DB     *sql.DB                // store.Open
	Clock  clock.Clock
	Audit  audit.Recorder
	Agent  agentapi.ConsoleClient // nil = no agent: every call fails with agentapi.ErrUnavailable
	Access Access                 // nil = deny: every recheck fails and open streams close (Phase A stub never streams)
	Log    *slog.Logger           // nil = slog.Default()
}

// Access re-checks the caller of an open stream (spec §6.2). Implemented in B2.
type Access interface {
	// Recheck returns nil while the session p.SessionID is valid (not expired, not revoked,
	// stage full), the user exists and still holds perm on serverID (admins always do).
	Recheck(ctx context.Context, p principal.Principal, serverID int64, perm rbac.Permission) error
}

type Handler struct {
	deps Deps
}

func NewHandler(d Deps) *Handler {
	if d.Agent == nil {
		d.Agent = noAgent{}
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Handler{deps: d}
}

// Routes registers the console routes (paths relative to /api, rules per V4).
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/servers/{id}/console/stream", rbac.Server(rbac.PermServerConsole, "id"), h.handleStream)
	api.Post("/servers/{id}/console", rbac.Server(rbac.PermServerConsole, "id"), h.handleCommand)
	api.Get("/servers/{id}/players", rbac.Server(rbac.PermServerView, "id"), h.handleOnlinePlayers)
	api.Get("/servers/{id}/players/lists", rbac.Server(rbac.PermServerConsole, "id"), h.handlePlayerLists)
	api.Post("/servers/{id}/players/actions", rbac.Server(rbac.PermServerConsole, "id"), h.handlePlayerAction)
}

// Shutdown closes all open streams (called from cmd/panel on shutdown). Phase A has none.
func (h *Handler) Shutdown() {}

// notImplemented answers every console route until phase B2 replaces the handlers.
func (h *Handler) notImplemented(w http.ResponseWriter, r *http.Request) {
	h.deps.Log.DebugContext(r.Context(), "console route not implemented yet", "path", r.URL.Path)
	httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "console not implemented yet")
}

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request)        { h.notImplemented(w, r) }
func (h *Handler) handleCommand(w http.ResponseWriter, r *http.Request)       { h.notImplemented(w, r) }
func (h *Handler) handleOnlinePlayers(w http.ResponseWriter, r *http.Request) { h.notImplemented(w, r) }
func (h *Handler) handlePlayerLists(w http.ResponseWriter, r *http.Request)   { h.notImplemented(w, r) }
func (h *Handler) handlePlayerAction(w http.ResponseWriter, r *http.Request)  { h.notImplemented(w, r) }

// noAgent stands in for a missing agent client: every call fails as if the agent were down.
type noAgent struct{}

func (noAgent) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, agentapi.ErrUnavailable
}

func (noAgent) Rcon(context.Context, int64, string) (string, error) {
	return "", agentapi.ErrUnavailable
}

func (noAgent) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{}, agentapi.ErrUnavailable
}
```

- [ ] **Step 4: Verdrahtung in `cmd/panel`**

`cmd/panel/app.go`:

```go
type appDeps struct {
	Clock clock.Clock
	Agent agentapi.Client
	// Instances is the agent's instance API (in production the same *agent.Client as Agent);
	// nil means no agent: the panel works, instance states are "unknown".
	Instances agentapi.InstancesClient
	// Console is the agent's console API (logs, RCON, player lists; the same *agent.Client);
	// nil means no agent: console routes answer agent_unavailable.
	Console agentapi.ConsoleClient
	Version string // panel version, sent in the User-Agent of catalog requests
}
```

Nach `serversH := …`:

```go
	consoleH := console.NewHandler(console.Deps{DB: db, Clock: d.Clock, Audit: auditRec, Agent: d.Console})
```

Im `/api`-Block nach `serversH.Routes(api)`: `consoleH.Routes(api)`. Rückgabe:

```go
		Close:   func() { consoleH.Shutdown(); _ = closeGeo() },
```

Import `"github.com/Bernotese/mc-panel/internal/console"` ergänzen.

`cmd/panel/main.go:235`:

```go
	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clock.Real{}, Agent: ac, Instances: ac, Console: ac, Version: version})
```

Die übrigen `buildApp`-Aufrufe in Tests (`app_test.go`, `listen_test.go`, `integration_test.go`) bleiben unverändert (ohne `Console` → `noAgent`).

- [ ] **Step 5: OpenAPI**

`api/openapi.yaml`:

1. Tags – nach `servers`:

```yaml
  - { name: console, description: 'Live console: log stream, RCON commands, players' }
```

2. Pfade – direkt nach dem Block `/servers/{id}/power:` (vor `/audit:`) einfügen:

```yaml
  /servers/{id}/console/stream:
    get:
      operationId: streamConsole
      tags: [console]
      summary: Live log of the server (Server-Sent Events)
      description: |
        `text/event-stream`, kept open across container restarts. Events:

        - `line` with `id: <Docker timestamp in ns>` and `data: {"ts": <unix ms>, "stream": "out"|"err", "line": "…"}`
        - `state` with `data: {"running": true|false}` – the container log stream (re)opened or ended
        - comment `: ping` every 15 s

        With `Last-Event-ID` the stream resumes after that line; otherwise it starts with the last `tail` lines.
        The panel re-checks session and permission every 30 s and closes the stream when either is gone.
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - { name: tail, in: query, required: false, schema: { type: integer, minimum: 0, maximum: 1000, default: 500 } }
      responses:
        '200': { description: Event stream, content: { text/event-stream: { schema: { type: string } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/console:
    post:
      operationId: sendConsoleCommand
      tags: [console]
      summary: Run a console command over RCON (audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/ConsoleCommandRequest' } } }
      responses:
        '200': { description: RCON output, content: { application/json: { schema: { $ref: '#/components/schemas/ConsoleCommandResponse' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: instance_not_running, content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/players:
    get:
      operationId: getOnlinePlayers
      tags: [console]
      summary: Players currently online (cached for 10 s)
      parameters:
        - $ref: '#/components/parameters/IdPath'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/OnlinePlayers' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
  /servers/{id}/players/lists:
    get:
      operationId: getPlayerLists
      tags: [console]
      summary: Ops, whitelist and bans as kept by the server (readable while stopped)
      parameters:
        - $ref: '#/components/parameters/IdPath'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/PlayerLists' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/players/actions:
    post:
      operationId: playerAction
      tags: [console]
      summary: Kick, ban, pardon, op, deop or change the whitelist (audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/PlayerActionRequest' } } }
      responses:
        '200': { description: RCON output, content: { application/json: { schema: { $ref: '#/components/schemas/ConsoleCommandResponse' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: instance_not_running, content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
```

3. `components.responses.ServiceUnavailable`:

```yaml
    ServiceUnavailable: { description: 'Agent, Docker or RCON not available (agent_unavailable, docker_unavailable, rcon_unavailable)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
```

4. Error-Enum: `…, base_domain_missing, catalog_unavailable, instance_not_running, rcon_unavailable, internal]`.

5. Schemas – nach `UpdateServerSettingsRequest` einfügen (exakt V3):

```yaml
    ConsoleCommandRequest:
      type: object
      additionalProperties: false
      required: [command]
      properties:
        command: { type: string, minLength: 1, maxLength: 1001, description: 'Minecraft command; one leading "/" is stripped. 1..1000 bytes, no control characters.' }
    ConsoleCommandResponse:
      type: object
      required: [output]
      properties:
        output: { type: string, description: 'RCON answer; may contain § formatting codes' }
    OnlinePlayer:
      type: object
      required: [name, uuid]
      properties:
        name: { type: string }
        uuid: { type: [string, 'null'] }
    OnlinePlayers:
      type: object
      required: [available, online, max]
      properties:
        available: { type: boolean, description: false while the server is stopped or RCON does not answer }
        online: { type: array, items: { $ref: '#/components/schemas/OnlinePlayer' } }
        max: { type: [integer, 'null'] }
    PlayerOp:
      type: object
      required: [uuid, name, level]
      properties: { uuid: { type: string }, name: { type: string }, level: { type: integer } }
    PlayerEntry:
      type: object
      required: [uuid, name]
      properties: { uuid: { type: string }, name: { type: string } }
    PlayerBan:
      type: object
      required: [uuid, name, reason, source, created, expires]
      properties:
        uuid: { type: string }
        name: { type: string }
        reason: { type: string }
        source: { type: string }
        created: { type: string, description: 'As stored by the server, e.g. "2026-10-11 09:32:51 +0000"' }
        expires: { type: string, description: '"forever" or a date like created' }
    PlayerLists:
      type: object
      required: [ops, whitelist, bans, errors]
      properties:
        ops: { type: array, items: { $ref: '#/components/schemas/PlayerOp' } }
        whitelist: { type: array, items: { $ref: '#/components/schemas/PlayerEntry' } }
        bans: { type: array, items: { $ref: '#/components/schemas/PlayerBan' } }
        errors: { type: array, items: { type: string, enum: [ops.json, whitelist.json, banned-players.json] }, description: Files that exist but could not be read; their list is empty }
    PlayerAction: { type: string, enum: [kick, ban, pardon, op, deop, whitelist_add, whitelist_remove] }
    PlayerActionRequest:
      type: object
      additionalProperties: false
      required: [action, name]
      properties:
        action: { $ref: '#/components/schemas/PlayerAction' }
        name: { type: string, pattern: '^[A-Za-z0-9_]{3,16}$' }
        reason: { type: string, maxLength: 200, description: kick and ban only }
```

Run: `make generate`
Expected: `web/src/api/schema.d.ts` enthält `streamConsole`, `sendConsoleCommand`, `getOnlinePlayers`, `getPlayerLists`, `playerAction` und die Schemas.

- [ ] **Step 6: TS-Typen, Fehlercodes, Übersetzungen**

`web/src/api/types.ts` – nach `UpdateServerSettingsRequest`:

```ts
export type ConsoleCommandRequest = Schemas['ConsoleCommandRequest']
export type ConsoleCommandResponse = Schemas['ConsoleCommandResponse']
export type OnlinePlayer = Schemas['OnlinePlayer']
export type OnlinePlayers = Schemas['OnlinePlayers']
export type PlayerOp = Schemas['PlayerOp']
export type PlayerEntry = Schemas['PlayerEntry']
export type PlayerBan = Schemas['PlayerBan']
export type PlayerLists = Schemas['PlayerLists']
export type PlayerListFile = PlayerLists['errors'][number]
export type PlayerAction = Schemas['PlayerAction']
export type PlayerActionRequest = Schemas['PlayerActionRequest']

/** SSE `line` event of GET /servers/{id}/console/stream; the event id is the Docker timestamp in ns. */
export interface ConsoleLineEvent {
  ts: number // unix ms
  stream: 'out' | 'err'
  line: string
}

/** SSE `state` event: the container log stream (re)opened (true) or ended (false). */
export interface ConsoleStateEvent {
  running: boolean
}
```

`web/src/api/errors.ts` – in `KNOWN_CODES` nach `'catalog_unavailable',`: `'instance_not_running',` und `'rcon_unavailable',`.

`web/src/i18n/i18n.test.ts` – in `ERROR_CODES` nach `'catalog_unavailable',`: `'instance_not_running',` und `'rcon_unavailable',`.

`web/src/i18n/de.ts` – unter `errors` nach `catalog_unavailable`:

```ts
    instance_not_running: 'Der Server läuft nicht.',
    rcon_unavailable: 'Die Server-Konsole antwortet gerade nicht – startet der Server noch?',
```

`web/src/i18n/en.ts`:

```ts
    instance_not_running: 'The server is not running.',
    rcon_unavailable: 'The server console is not responding right now – is the server still starting?',
```

- [ ] **Step 7: Mock-Tests schreiben (rot)**

`web/src/mocks/handlers.test.ts`: `expect(ids).toHaveLength(61)` → `66`. Am Ende von `describe('mock behaviour', …)` ergänzen:

```ts
  it('runs console commands and player actions on running servers only', async () => {
    const me = loginAs('admin')
    const cmd = (id: number, command: string) => callApi('POST', `/servers/${id}/console`, { body: { command }, csrf: me.csrf_token })
    expect(field((await cmd(1, '/list')).body, 'output')).toMatch(/^There are 2 of a max of 20 players online: Alex, Steve$/)
    expect(field((await cmd(1, 'say a\nstop')).body, 'error')).toBe('validation_failed')
    expect(field((await cmd(2, 'list')).body, 'error')).toBe('instance_not_running')
    expect(db.audit.at(-1)).toMatchObject({ action: 'server.console_command', details: { command: 'list' } })

    const act = (body: object) => callApi('POST', '/servers/1/players/actions', { body, csrf: me.csrf_token })
    expect(field((await act({ action: 'op', name: 'Steve' })).body, 'output')).toBe('Made Steve a server operator')
    expect(field((await act({ action: 'ban', name: 'Steve', reason: 'griefing' })).body, 'output')).toBe('Banned Steve: griefing')
    expect(field((await act({ action: 'op', name: 'Steve', reason: 'x' })).body, 'error')).toBe('validation_failed')
    expect(field((await act({ action: 'kick', name: 'a b' })).body, 'error')).toBe('validation_failed')
    expect(field((await act({ action: 'kick', name: 'Alex', reason: 'a\nb' })).body, 'error')).toBe('validation_failed')
    const lists = (await callApi('GET', '/servers/1/players/lists')).body as { ops: Array<{ name: string }>; bans: Array<{ name: string; reason: string }> }
    expect(lists.ops.map((o) => o.name)).toContain('Steve')
    expect(lists.bans).toContainEqual(expect.objectContaining({ name: 'Steve', reason: 'griefing' }))
    expect((await callApi('GET', '/servers/1/players')).body).toMatchObject({ available: true, online: [{ name: 'Alex' }], max: 20 })
    expect((await callApi('GET', '/servers/2/players')).body).toEqual({ available: false, online: [], max: null })
    expect((await callApi('GET', '/servers/3/players')).status).toBe(404)
  })

  it('gives server.view only the online players', async () => {
    loginAs('alice')
    expect((await callApi('GET', '/servers/1/players')).status).toBe(200)
    expect((await callApi('GET', '/servers/1/players/lists')).status).toBe(403)
    expect((await callApi('GET', '/servers/1/console/stream')).status).toBe(403)
  })

  it('streams console lines as server-sent events', async () => {
    loginAs('admin')
    const read = async (id: number, until: string) => {
      const res = await fetch(`${window.location.origin}/api/servers/${id}/console/stream`, { headers: { Accept: 'text/event-stream' } })
      expect(res.headers.get('Content-Type')).toBe('text/event-stream')
      const reader = res.body!.getReader()
      const decoder = new TextDecoder()
      let text = ''
      while (!text.includes(until)) {
        const { value, done } = await reader.read()
        if (done) break
        text += decoder.decode(value, { stream: true })
      }
      await reader.cancel()
      return text
    }
    const running = await read(1, 'Done (')
    expect(running).toContain('event: state\ndata: {"running":true}')
    expect(running).toMatch(/id: \d{16,}\nevent: line\ndata: \{"ts":\d+,"stream":"out","line":"\[\d\d:\d\d:\d\d INFO\]: Starting minecraft server/)
    expect(running).toContain('Thread RCON Client /127.0.0.1 started')
    expect(await read(2, 'running')).toContain('event: state\ndata: {"running":false}')
  })
```

- [ ] **Step 8: Mock-Datenbank erweitern**

`web/src/mocks/db.ts`:

- Import um `OnlinePlayer, PlayerBan, PlayerEntry, PlayerOp` erweitern.
- Vor `MockServer`:

```ts
/** What the mock "Minecraft server" keeps itself: online players, ops.json, whitelist.json, banned-players.json. */
export interface MockPlayers {
  online: OnlinePlayer[]
  ops: PlayerOp[]
  whitelist: PlayerEntry[]
  bans: PlayerBan[]
}

export function emptyPlayers(): MockPlayers {
  return { online: [], ops: [], whitelist: [], bans: [] }
}

const ALEX_UUID = 'ec561538-f3fd-461d-aff5-086b22154bce'
const STEVE_UUID = '8667ba71-b85a-4004-af54-457a9734eed7'
```

- In `MockServer` nach `runningRevision`: `players: MockPlayers`.
- Im Seed: Server 1 bekommt

```ts
        players: {
          online: [
            { name: 'Alex', uuid: ALEX_UUID },
            { name: 'Steve', uuid: STEVE_UUID },
          ],
          ops: [{ uuid: ALEX_UUID, name: 'Alex', level: 4 }],
          whitelist: [
            { uuid: ALEX_UUID, name: 'Alex' },
            { uuid: STEVE_UUID, name: 'Steve' },
          ],
          bans: [
            { uuid: '1f0b3c55-6a7d-4e8f-9a0b-1c2d3e4f5a6b', name: 'Griefer123', reason: 'Griefing', source: 'Alex', created: '2026-10-01 18:04:12 +0000', expires: 'forever' },
          ],
        },
```

Server 2 und 3: `players: emptyPlayers(),`.

`web/src/mocks/handlers/servers.ts` – im neu angelegten `MockServer` nach `runningRevision: 1,`: `players: emptyPlayers(),` (Import aus `'../db'` ergänzen).

- [ ] **Step 9: `web/src/mocks/handlers/console.ts` schreiben**

```ts
import { http, HttpResponse } from 'msw'
import type {
  ConsoleCommandRequest,
  ConsoleCommandResponse,
  ConsoleLineEvent,
  ConsoleStateEvent,
  OnlinePlayers,
  Permission,
  PlayerAction,
  PlayerActionRequest,
  PlayerLists,
} from '@/api/types'
import { db, recordAudit, type MockPlayers, type MockServer } from '../db'
import { API, apiError, guard, idParam, op, readJson, serverPermissions, type Authed, type ErrorResponse, type MockOperation } from './util'

const PLAYER_RE = /^[A-Za-z0-9_]{3,16}$/
const ACTIONS: readonly PlayerAction[] = ['kick', 'ban', 'pardon', 'op', 'deop', 'whitelist_add', 'whitelist_remove']
const MAX_COMMAND_BYTES = 1000
const MAX_REASON_LENGTH = 200
const MAX_PLAYERS = 20
const TICK_MS = 2000
// Like agentapi.ValidateCommand: no control character (incl. newline and tab) and no DEL.
const CONTROL_RE = /[\u0000-\u001f\u007f]/
// Like the panel's reason check: printable only (Unicode category C = control/format/unassigned).
const NON_PRINTABLE_RE = /\p{C}/u

type Params = Parameters<typeof idParam>[0]

/** Session, permission and server lookup shared by every console operation. */
function access(request: Request, params: Params, perm: Permission): { g: Authed; server: MockServer } | ErrorResponse {
  const g = guard(request)
  if (g instanceof Response) return g
  const id = idParam(params)
  if (!serverPermissions(g.user, id).includes(perm)) return apiError(403, 'forbidden', `missing permission ${perm}`)
  const server = db.servers.find((s) => s.id === id)
  if (!server?.instance) return apiError(404, 'not_found', 'server not found')
  return { g, server }
}

function isRunning(s: MockServer): boolean {
  return s.status.state === 'running'
}

function hhmmss(): string {
  return new Date().toTimeString().slice(0, 8)
}

/** Minecraft's ban date format: "2026-10-11 09:32:51 +0000". */
function mcDate(d: Date): string {
  return `${d.toISOString().slice(0, 19).replace('T', ' ')} +0000`
}

/** Stable fake UUID per name (offline-mode servers derive them from the name, too). */
function mockUuid(name: string): string {
  let h = 0
  for (const c of name.toLowerCase()) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return `00000000-0000-4000-8000-${h.toString(16).padStart(12, '0')}`
}

/** Same rules as agentapi.ValidateCommand after stripping one leading "/"; null if invalid. */
function normalizeCommand(raw: unknown): string | null {
  if (typeof raw !== 'string') return null
  const cmd = raw.startsWith('/') ? raw.slice(1) : raw
  const bytes = new TextEncoder().encode(cmd).length
  if (bytes < 1 || bytes > MAX_COMMAND_BYTES || CONTROL_RE.test(cmd) || cmd.trim() === '') return null
  return cmd
}

function listOutput(p: MockPlayers, uuids: boolean): string {
  const names = p.online.map((o) => (uuids && o.uuid ? `${o.name} (${o.uuid})` : o.name)).join(', ')
  return `There are ${p.online.length} of a max of ${MAX_PLAYERS} players online: ${names}`
}

function commandOutput(server: MockServer, cmd: string): string {
  const [verb, ...rest] = cmd.trim().split(/\s+/)
  if (verb === 'list') return listOutput(server.players, rest[0] === 'uuids')
  if (verb === 'say') return ''
  return 'Unknown or incomplete command, see below for error'
}

function applyAction(p: MockPlayers, action: PlayerAction, name: string, reason: string): string {
  const lower = name.toLowerCase()
  const is = (n: { name: string }) => n.name.toLowerCase() === lower
  const uuid = p.online.find(is)?.uuid ?? mockUuid(name)
  switch (action) {
    case 'kick': {
      const player = p.online.find(is)
      if (!player) return 'No player was found'
      p.online = p.online.filter((o) => o !== player)
      return `Kicked ${player.name}: ${reason || 'Kicked by an operator'}`
    }
    case 'ban':
      if (p.bans.some(is)) return 'Nothing changed. The player is already banned'
      p.bans.push({ uuid, name, reason: reason || 'Banned by an operator.', source: 'Rcon', created: mcDate(new Date()), expires: 'forever' })
      p.online = p.online.filter((o) => !is(o))
      return `Banned ${name}: ${reason || 'Banned by an operator.'}`
    case 'pardon':
      if (!p.bans.some(is)) return "Nothing changed. The player isn't banned"
      p.bans = p.bans.filter((b) => !is(b))
      return `Unbanned ${name}`
    case 'op':
      if (p.ops.some(is)) return 'Nothing changed. The player already is an operator'
      p.ops.push({ uuid, name, level: 4 })
      return `Made ${name} a server operator`
    case 'deop':
      if (!p.ops.some(is)) return 'Nothing changed. The player is not an operator'
      p.ops = p.ops.filter((o) => !is(o))
      return `Made ${name} no longer a server operator`
    case 'whitelist_add':
      if (p.whitelist.some(is)) return 'Player is already whitelisted'
      p.whitelist.push({ uuid, name })
      return `Added ${name} to the whitelist`
    case 'whitelist_remove':
      if (!p.whitelist.some(is)) return 'Player is not whitelisted'
      p.whitelist = p.whitelist.filter((w) => !is(w))
      return `Removed ${name} from the whitelist`
  }
}

function startLines(server: MockServer): Array<[string, ConsoleLineEvent['stream']]> {
  const t = hhmmss()
  return [
    [`[${t} INFO]: Starting minecraft server version ${server.instance?.version ?? ''}`, 'out'],
    [`[${t} INFO]: Loading properties`, 'out'],
    [`[${t} INFO]: Default game type: SURVIVAL`, 'out'],
    [`[${t} INFO]: Preparing level "world"`, 'out'],
    [`[${t} INFO]: \u001b[33mPreparing spawn area: 100%\u001b[0m`, 'out'],
    [`[${t} INFO]: §aWelcome to ${server.name}§r`, 'out'],
    [`[${t} WARN]: Can't keep up! Is the server overloaded? Running 2043ms or 40 ticks behind`, 'out'],
    [`[${t} ERROR]: Could not pass event PlayerJoinEvent to ExamplePlugin v1.0`, 'err'],
    [`[${t} INFO]: Done (8.976s)! For help, type "help"`, 'out'],
    [`[${t} INFO]: Thread RCON Client /127.0.0.1 started`, 'out'],
    [`[${t} INFO]: Thread RCON Client /127.0.0.1 shutting down`, 'out'],
    [`[${t} INFO]: Alex joined the game`, 'out'],
  ]
}

function sseState(running: boolean): string {
  const data: ConsoleStateEvent = { running }
  return `event: state\ndata: ${JSON.stringify(data)}\n\n`
}

function sseLine(id: bigint, line: string, stream: ConsoleLineEvent['stream']): string {
  const data: ConsoleLineEvent = { ts: Date.now(), stream, line }
  return `id: ${id.toString()}\nevent: line\ndata: ${JSON.stringify(data)}\n\n`
}

export const consoleOperations: MockOperation[] = [
  op(
    'streamConsole',
    // Plain http.get with a ReadableStream instead of msw's sse(): sse() needs a global
    // EventSource (missing in jsdom) and cannot answer 401/403/404.
    http.get(`${API}/servers/:id/console/stream`, ({ request, params }) => {
      const a = access(request, params, 'server.console')
      if (a instanceof Response) return a
      const encoder = new TextEncoder()
      let timer: ReturnType<typeof setInterval> | undefined
      const stop = () => {
        if (timer !== undefined) clearInterval(timer)
        timer = undefined
      }
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          const send = (text: string) => {
            try {
              controller.enqueue(encoder.encode(text))
            } catch {
              stop() // the client went away
            }
          }
          if (!isRunning(a.server)) {
            send(sseState(false))
            return
          }
          // state first, before any line: the console shows "connected" at once (V3).
          send(sseState(true))
          // Event ids are Docker timestamps in ns: strictly increasing per line.
          let id = BigInt(Date.now()) * 1_000_000n
          const line = (text: string, s: ConsoleLineEvent['stream'] = 'out') => {
            id += 1n
            send(sseLine(id, text, s))
          }
          for (const [text, s] of startLines(a.server)) line(text, s)
          let tick = 0
          timer = setInterval(() => {
            tick += 1
            line(`[${hhmmss()} INFO]: Tick ${tick}`)
          }, TICK_MS)
        },
        cancel() {
          stop()
        },
      })
      request.signal.addEventListener('abort', stop)
      return new HttpResponse(stream, { headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-store' } })
    }),
  ),

  op(
    'sendConsoleCommand',
    http.post(`${API}/servers/:id/console`, async ({ request, params }) => {
      const a = access(request, params, 'server.console')
      if (a instanceof Response) return a
      const body = await readJson<Partial<ConsoleCommandRequest>>(request)
      const cmd = normalizeCommand(body?.command)
      if (cmd === null) return apiError(400, 'validation_failed', 'command: 1..1000 bytes without control characters')
      const target = `server:${a.server.id}`
      if (!isRunning(a.server)) {
        recordAudit(a.g.user, 'server.console_command', target, 'failure', { command: cmd })
        return apiError(409, 'instance_not_running', 'server is not running')
      }
      recordAudit(a.g.user, 'server.console_command', target, 'success', { command: cmd })
      return HttpResponse.json<ConsoleCommandResponse>({ output: commandOutput(a.server, cmd) })
    }),
  ),

  op(
    'getOnlinePlayers',
    http.get(`${API}/servers/:id/players`, ({ request, params }) => {
      const a = access(request, params, 'server.view')
      if (a instanceof Response) return a
      if (!isRunning(a.server)) return HttpResponse.json<OnlinePlayers>({ available: false, online: [], max: null })
      return HttpResponse.json<OnlinePlayers>({ available: true, online: a.server.players.online, max: MAX_PLAYERS })
    }),
  ),

  op(
    'getPlayerLists',
    http.get(`${API}/servers/:id/players/lists`, ({ request, params }) => {
      const a = access(request, params, 'server.console')
      if (a instanceof Response) return a
      const p = a.server.players
      return HttpResponse.json<PlayerLists>({ ops: p.ops, whitelist: p.whitelist, bans: p.bans, errors: [] })
    }),
  ),

  op(
    'playerAction',
    http.post(`${API}/servers/:id/players/actions`, async ({ request, params }) => {
      const a = access(request, params, 'server.console')
      if (a instanceof Response) return a
      const body = await readJson<Partial<PlayerActionRequest>>(request)
      if (!body?.action || !ACTIONS.includes(body.action)) return apiError(400, 'validation_failed', 'action: unknown action')
      if (typeof body.name !== 'string' || !PLAYER_RE.test(body.name)) return apiError(400, 'validation_failed', 'name must match ^[A-Za-z0-9_]{3,16}$')
      const reason = body.reason ?? ''
      if (reason !== '' && body.action !== 'kick' && body.action !== 'ban') return apiError(400, 'validation_failed', 'reason: only for kick and ban')
      if ([...reason].length > MAX_REASON_LENGTH || NON_PRINTABLE_RE.test(reason)) return apiError(400, 'validation_failed', 'reason: at most 200 printable characters')
      const target = `server:${a.server.id}`
      const details = { action: body.action, name: body.name, ...(reason ? { reason } : {}) }
      if (!isRunning(a.server)) {
        recordAudit(a.g.user, 'server.player_action', target, 'failure', details)
        return apiError(409, 'instance_not_running', 'server is not running')
      }
      const output = applyAction(a.server.players, body.action, body.name, reason)
      recordAudit(a.g.user, 'server.player_action', target, 'success', details)
      return HttpResponse.json<ConsoleCommandResponse>({ output })
    }),
  ),
]
```

`web/src/mocks/handlers.ts`: `import { consoleOperations } from './handlers/console'` und `...consoleOperations,` nach `...serverOperations,`.

Hinweis: `ErrorResponse` und `Authed` sind in `util.ts` bereits exportiert. Falls ESLint `no-control-regex` für `CONTROL_RE` meldet, die Zeile mit `// eslint-disable-next-line no-control-regex -- matches control characters on purpose (agentapi.ValidateCommand)` versehen.

- [ ] **Step 10: Prüfen**

Run: `make check`
Expected: PASS (inkl. `tsc` über Mock und Typen).

- [ ] **Step 11: Commit, Push, CI**

```bash
git add internal/httpx internal/audit internal/console cmd/panel api/openapi.yaml web/src
git commit -m "feat(console): panel contract for the console, stub routes and mocks

OpenAPI operations streamConsole, sendConsoleCommand, getOnlinePlayers, getPlayerLists and
playerAction, error codes instance_not_running/rcon_unavailable, audit actions, a stub
console handler (503 until phase B2) and MSW mocks with a server-sent event stream.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: grün – `TestRoutesMatchOpenAPI` und `TestErrorCodesMatchOpenAPI` (cmd/panel), `TestRouteContract`/`TestStubsAnswerAgentUnavailable` (console), `TestEveryRouteDeclaresARule`, Vitest `handlers.test.ts` (66 Operationen, neue Mock-Tests), `i18n.test.ts`; Playwright unverändert grün.

Danach: PR als „Ready for review“ markieren (`gh pr ready`) und dem Nutzer zum Mergen melden. Erst nach dem Merge starten B1, B2, B3.
