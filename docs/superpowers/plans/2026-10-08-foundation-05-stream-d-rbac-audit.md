# Stream D – Benutzer, Rechte & Audit – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Benutzerverwaltung (CRUD, Rollen, 2FA-Reset), Rechte pro Server mit DB-gestütztem `rbac.Checker`, Server-Stub-API, manipulationssicheres Audit-Log mit SHA-256-Hash-Kette (Recorder, Verify, Liste) sowie die CLI-Funktionen `panel admin create` und `panel audit verify` bauen.

**Architecture:** Drei Pakete auf Basis der Phase-0-Verträge: `internal/rbac` bekommt `DBChecker` (Tabelle `permissions`) und `Uncovered` (Route-Coverage über `chi.Walk`); `internal/audit` bekommt `SQLRecorder` (Mutex + `BEGIN IMMEDIATE`, Hash-Kette), `Verify`, HTTP-Handler und CLI; `internal/users` enthält Benutzer-, Rechte- und Server-Stub-Handler plus `RunAdminCreate`. Alle Routen werden mit exakt den Regeln aus der OpenAPI-Tabelle auf dem übergebenen `*rbac.Router` registriert; SQL ausschließlich über sqlc (`Rbac…`/`Audit…`).

**Tech Stack:** Go, chi v5, modernc.org/sqlite, sqlc, `internal/password` (argon2id), Standardbibliothek (`crypto/sha256`, `encoding/json`, `testing`, `net/http/httptest`).

**Spec:** `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md` (§5 Rechte, §6 Audit-Log, §12 Abnahmekriterien 3, 4, 6)

## Global Constraints

Alle Punkte aus `docs/superpowers/plans/2026-10-08-foundation-00-overview.md`, Abschnitt „Global Constraints“, gelten vollständig (Modulpfad `github.com/Bernotese/mc-panel`, kein CGO, Unix-ms-Zeitstempel, `clock.Clock` statt `time.Now()`, keine Migrationen ändern, Phase-0-Dateien schreibgeschützt, Routen nur über `*rbac.Router` mit vollem Pfad relativ zu `/api`, nie `Route()` im Produktivcode, sqlc-Präfixe `Rbac…`/`Audit…`, keine neuen Go-Abhängigkeiten, Tests nur Standardbibliothek, Fehlercodes nur aus `internal/httpx/errors.go`, Conventional Commits mit `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`).

Stream-spezifische, exakte Werte:

- **Eigene Pfade:** `internal/users/**`, `internal/audit/**` (außer `types.go`, `memory.go`), `internal/rbac/checker.go`, `internal/rbac/coverage.go` (+ Tests), `internal/store/queries/rbac.sql`, `internal/store/queries/audit.sql`, `internal/store/rbac.sql.go`, `internal/store/audit.sql.go`. Keine andere Datei wird angelegt oder geändert.
- **Benutzername:** `^[a-zA-Z0-9_.-]{3,32}$`; Eindeutigkeit case-insensitiv (Spalte `COLLATE NOCASE`) → `409 conflict`.
- **Rolle:** `admin` | `user`; sonst `400 validation_failed`.
- **Passwort:** nur `password.Validate(pw, username)` + `password.Hash(pw)`; Verstoß → `400 validation_failed` mit `err.Error()` als Nachricht.
- **`webauthn_handle`:** 32 Byte aus `crypto/rand` beim Anlegen.
- **Servername:** `^[a-z0-9][a-z0-9-]{1,31}$`; Duplikat → `409 conflict`.
- **Power-Aktion:** `start` | `stop` | `restart`, sonst `400 validation_failed`; Antwort `202 {"status":"accepted"}`; Audit `server.power` mit Details `{"action": <action>, "name": <servername>}`; keine echte Wirkung.
- **`server.view`-Implikation (Entscheidung):** Wer für einen Server irgendein Recht erhält, bekommt `server.view` automatisch dazu. Normalisiert wird beim Schreiben (`PUT /users/{id}/permissions`); der `DBChecker` prüft danach exakt die gespeicherten Zeilen. Ein Grant mit leerer Rechteliste entfernt den Server.
- **Rechte-Reihenfolge in Antworten:** immer in der Reihenfolge von `rbac.AllPermissions` (`server.view, server.power, server.console, server.files, server.plugins, server.backups`); Grants aufsteigend nach `server_id`.
- **`last_login_at`:** `MAX(ts)` der `login_attempts` mit `user_id = id AND success = 1 AND stage <> 'sudo'`; `null`, wenn keine.
- **Letzter Admin:** Löschen oder Herabstufen des einzigen Admins → `409 conflict`; Löschen des eigenen Kontos → `409 conflict`. Prüfung innerhalb der Schreibtransaktion.
- **Admin setzt Passwort (PATCH):** widerruft alle Sessions des Zielbenutzers (`sessions.revoked_at = now`).
- **reset-2fa:** `totp_secret_enc = NULL`, `totp_enabled = 0`, `totp_last_step = 0`, alle `webauthn_credentials` und `recovery_codes` des Benutzers löschen, alle offenen Sessions widerrufen – in einer Transaktion.
- **Transaktionen:** `db.BeginTx` startet dank `_txlock=immediate` aus `store.Open` ein `BEGIN IMMEDIATE`. Audit-Einträge werden **erst nach dem Commit** geschrieben (der `SQLRecorder` öffnet eine eigene Schreibtransaktion).
- **Audit-Ziele:** `user:<id>`, `server:<id>`. Akteur ohne Principal: `actor_type=system`, `actor_name=system`, `actor_id=NULL`. CLI: `actor_type=cli`, `actor_name=cli`.
- **Audit-Details:** `encoding/json`-Marshal der `map[string]any` (Schlüssel auf jeder Ebene sortiert); leer/`nil` → `{}`. Der gespeicherte Text ist kanonisch und wird nie neu kodiert.
- **Audit-Kanonisierung:** `json.Marshal([]any{id, ts, actor_type, actor_id|null, actor_name, action, target, ip, result, details})` – kompaktes JSON-Array, Standard-HTML-Escaping von `encoding/json`, `details` als JSON-**String** (gespeicherter Text). Beispiel: `[1,1760000000000,"user",7,"jannes","user.create","user:8","203.0.113.7","success","{\"role\":\"user\",\"username\":\"alice\"}"]`.
- **Audit-Hash:** `hash = hex(SHA-256(prev_hash || canonical))`, Kleinbuchstaben, 64 Zeichen; erster `prev_hash = ""`.
- **Audit-IDs:** explizit `id = letzte id + 1` (erste ID 1), gelesen in derselben `BEGIN IMMEDIATE`-Transaktion; zusätzlich prozesslokaler Mutex.
- **Verify:** Batches à 1000 Zeilen in `id`-Reihenfolge; Eintrag fehlerhaft, wenn `id != vorherige id + 1`, `prev_hash != vorheriger hash` oder `hash != ComputeHash(prev_hash, row)`.
- **Audit-Liste:** Filter `actor` (exakt `actor_name`), `action` (Präfix), `result`, `from`/`to` (Unix-ms, inklusiv), `cursor` (dezimale ID, liefert `id < cursor`), `limit` Standard 50, erlaubt 1–200; ungültig → `400 validation_failed`. Sortierung `id DESC`; `next_cursor` = ID des letzten Elements, wenn weitere existieren, sonst `null`. Filter werden über SQLite-`GLOB` mit maskierten Metazeichen (`*`, `?`, `[`) umgesetzt.
- **Notifications (`notify.Event`, `Time = clock.Now()`, `IP` = Client-IP, `Username` = betroffener Benutzer):** `EventUserCreated` (Anlegen), `EventUserDeleted` (Löschen), `EventPermissionsChanged` (Rollenwechsel, Rechte-PUT), `Event2FAChanged` (reset-2fa), `EventAuditChainBroken` (`GET /audit/verify` mit Befund).

## Review Focus

1. **Letzter Admin (Übersicht Punkt 2):** Den einzigen Admin löschen oder auf `user` herabstufen bzw. das eigene Konto löschen muss mit `409 conflict` abgelehnt werden, ohne Datenänderung, mit Audit-Eintrag `result=denied`. → Task 7: `TestDemoteLastAdminIsRejected`, `TestChangeRole` (zweiter Teil), `TestDeleteSelfIsRejected`, `TestDeleteLastAdminIsRejected`.
2. **Parallele Audit-Schreibzugriffe (Übersicht Punkt 5):** 50 gleichzeitige `Record`-Aufrufe ergeben IDs 1..50 ohne Lücke und eine gültige Kette. → Task 3: `TestRecordConcurrent`.
3. **Panel und CLI schreiben gleichzeitig ins Audit-Log:** Zwei getrennte `*sql.DB`-Handles (zwei Prozesse) mit je eigenem Mutex dürfen keine verzweigte Kette erzeugen – das verhindert nur `BEGIN IMMEDIATE` mit Lesen des letzten Hashes in der Transaktion. → Task 3: `TestRecordConcurrentAcrossConnections`.
4. **Audit-Eintrag während offener Handler-Transaktion:** Ruft ein Handler `Record` innerhalb seiner eigenen `BEGIN IMMEDIATE`-Transaktion auf, blockiert der `SQLRecorder` bis `busy_timeout` (5 s) und der Eintrag geht verloren. Erwartet: Eintrag vorhanden, Kette gültig, Request schnell. → Task 7: `TestHandlersWithSQLRecorder`.
5. **Rechte-PUT mit teilweise ungültigem Inhalt:** Unbekannte `server_id`, unbekanntes Recht oder `server_id <= 0` → `400 validation_failed`, und der bisherige Rechtesatz bleibt vollständig erhalten (atomar). → Task 8: `TestSetPermissionsValidationIsAtomic`.

## Dateistruktur

```
internal/store/queries/rbac.sql            Rbac*-Queries: Users, Servers, Permissions, 2FA-Reset, Session-Widerruf, Last-Login
internal/store/rbac.sql.go                 generiert (sqlc) – committen
internal/store/queries/audit.sql           Audit*-Queries: letzter Eintrag, Insert, Verify-Batches, gefilterte Liste
internal/store/audit.sql.go                generiert (sqlc) – committen
internal/rbac/checker.go                   DBChecker implementiert rbac.Checker über die Tabelle permissions
internal/rbac/checker_test.go
internal/rbac/coverage.go                  Uncovered: Routen in chi, die nicht über rbac.Router registriert wurden
internal/rbac/coverage_test.go
internal/audit/canonical.go                Canonical, ComputeHash, CanonicalDetails (Hash-Format)
internal/audit/canonical_test.go
internal/audit/sqlrecorder.go              SQLRecorder (audit.Recorder): Akteur/IP füllen, Kette schreiben
internal/audit/helpers_test.go             gemeinsame Test-Helfer (t0, userCtx, readAll, assertChain)
internal/audit/sqlrecorder_test.go
internal/audit/verify.go                   Verify, VerifyReport
internal/audit/verify_test.go              (+ Helfer seed, insertChain, mustExec)
internal/audit/cli.go                      RunAuditVerify, ErrChainBroken
internal/audit/cli_test.go
internal/audit/handler.go                  GET /audit, GET /audit/verify
internal/audit/handler_test.go
internal/users/handler.go                  Handler, NewHandler, Routes, gemeinsame HTTP-Helfer
internal/users/service.go                  newUser (Validierung, Hash, Insert), Fehler, isUniqueViolation
internal/users/users.go                    GET/POST /users, GET /users/{id}, userJSON
internal/users/manage.go                   PATCH/DELETE /users/{id}, POST /users/{id}/reset-2fa, inTx, Letzter-Admin-Schutz
internal/users/permissions.go              GET/PUT /users/{id}/permissions, orderedPerms, loadGrants
internal/users/servers.go                  Server-Stub: GET/POST /servers, GET/DELETE /servers/{id}, POST /servers/{id}/power
internal/users/cli.go                      RunAdminCreate
internal/users/helpers_test.go             Test-Umgebung (echte DB, echter rbac.Router, Fake-Clock, MemoryRecorder, notify.Recording)
internal/users/users_test.go
internal/users/manage_test.go
internal/users/permissions_test.go
internal/users/servers_test.go
internal/users/cli_test.go
internal/users/routes_test.go              Vertragstest: alle 15 Stream-D-Routen mit exakter Regel + Coverage
```

---

### Task 1: Rbac-Queries und DB-gestützter `rbac.Checker`

**Files:**
- Create: `internal/store/queries/rbac.sql`, `internal/rbac/checker.go`
- Generated: `internal/store/rbac.sql.go`
- Test: `internal/rbac/checker_test.go`

**Interfaces:**
- Consumes: `store.New(db store.DBTX) *store.Queries`, `store.DBTX`, Modelle `store.User`, `store.Server` (Phase 0); `rbac.Checker`, `rbac.Permission`, `rbac.NewRouter`, `rbac.Server` (Phase 0); `storetest.Open/CreateUser/CreateServer`.
- Produces (generiert, von Tasks 6–10 genutzt):
```go
func (q *Queries) RbacListUsers(ctx context.Context) ([]User, error)
func (q *Queries) RbacGetUser(ctx context.Context, id int64) (User, error)
func (q *Queries) RbacGetUserByUsername(ctx context.Context, username string) (User, error)
func (q *Queries) RbacCreateUser(ctx context.Context, arg RbacCreateUserParams) (User, error)
	// RbacCreateUserParams{Username, PasswordHash, Role string; WebauthnHandle []byte; PasswordChangedAt, CreatedAt, UpdatedAt int64}
func (q *Queries) RbacUpdateUserRole(ctx context.Context, arg RbacUpdateUserRoleParams) error            // {Role string; UpdatedAt, ID int64}
func (q *Queries) RbacUpdateUserPassword(ctx context.Context, arg RbacUpdateUserPasswordParams) error    // {PasswordHash string; PasswordChangedAt, UpdatedAt, ID int64}
func (q *Queries) RbacDeleteUser(ctx context.Context, id int64) (int64, error)
func (q *Queries) RbacCountAdmins(ctx context.Context) (int64, error)
func (q *Queries) RbacClearTOTP(ctx context.Context, arg RbacClearTOTPParams) error                      // {UpdatedAt, ID int64}
func (q *Queries) RbacDeleteUserPasskeys(ctx context.Context, userID int64) error
func (q *Queries) RbacDeleteUserRecoveryCodes(ctx context.Context, userID int64) error
func (q *Queries) RbacRevokeUserSessions(ctx context.Context, arg RbacRevokeUserSessionsParams) error    // {RevokedAt sql.NullInt64; UserID int64}
func (q *Queries) RbacCountUserPasskeys(ctx context.Context, userID int64) (int64, error)
func (q *Queries) RbacLastLogin(ctx context.Context, userID sql.NullInt64) (int64, error)
func (q *Queries) RbacListServers(ctx context.Context) ([]Server, error)
func (q *Queries) RbacListViewableServers(ctx context.Context, userID int64) ([]Server, error)
func (q *Queries) RbacGetServer(ctx context.Context, id int64) (Server, error)
func (q *Queries) RbacGetServerByName(ctx context.Context, name string) (Server, error)
func (q *Queries) RbacCreateServer(ctx context.Context, arg RbacCreateServerParams) (Server, error)     // {Name string; CreatedAt int64}
func (q *Queries) RbacDeleteServer(ctx context.Context, id int64) (int64, error)
func (q *Queries) RbacHasPermission(ctx context.Context, arg RbacHasPermissionParams) (int64, error)     // {UserID, ServerID int64; Permission string}
func (q *Queries) RbacListUserPermissions(ctx context.Context, userID int64) ([]RbacListUserPermissionsRow, error) // Row{ServerID int64; Permission string}
func (q *Queries) RbacListUserServerPermissions(ctx context.Context, arg RbacListUserServerPermissionsParams) ([]string, error) // {UserID, ServerID int64}
func (q *Queries) RbacDeleteUserPermissions(ctx context.Context, userID int64) error
func (q *Queries) RbacInsertPermission(ctx context.Context, arg RbacInsertPermissionParams) error       // {UserID, ServerID int64; Permission string}
```
- Produces (Code):
```go
type DBChecker struct{ /* unexported */ }
func NewDBChecker(db store.DBTX) *DBChecker
func (c *DBChecker) HasServerPermission(ctx context.Context, userID, serverID int64, p Permission) (bool, error)
```

- [ ] **Step 1: `internal/store/queries/rbac.sql` schreiben**

```sql
-- Stream D: users, servers, permissions. All query names start with "Rbac".

-- name: RbacListUsers :many
SELECT * FROM users ORDER BY username;

-- name: RbacGetUser :one
SELECT * FROM users WHERE id = ?;

-- name: RbacGetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: RbacCreateUser :one
INSERT INTO users (username, password_hash, role, webauthn_handle, password_changed_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: RbacUpdateUserRole :exec
UPDATE users SET role = ?, updated_at = ? WHERE id = ?;

-- name: RbacUpdateUserPassword :exec
UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?;

-- name: RbacDeleteUser :execrows
DELETE FROM users WHERE id = ?;

-- name: RbacCountAdmins :one
SELECT count(*) FROM users WHERE role = 'admin';

-- name: RbacClearTOTP :exec
UPDATE users SET totp_secret_enc = NULL, totp_enabled = 0, totp_last_step = 0, updated_at = ? WHERE id = ?;

-- name: RbacDeleteUserPasskeys :exec
DELETE FROM webauthn_credentials WHERE user_id = ?;

-- name: RbacDeleteUserRecoveryCodes :exec
DELETE FROM recovery_codes WHERE user_id = ?;

-- name: RbacRevokeUserSessions :exec
UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL;

-- name: RbacCountUserPasskeys :one
SELECT count(*) FROM webauthn_credentials WHERE user_id = ?;

-- name: RbacLastLogin :one
SELECT ts FROM login_attempts
WHERE user_id = ? AND success = 1 AND stage <> 'sudo'
ORDER BY ts DESC
LIMIT 1;

-- name: RbacListServers :many
SELECT * FROM servers ORDER BY name;

-- name: RbacListViewableServers :many
SELECT * FROM servers
WHERE id IN (SELECT server_id FROM permissions WHERE user_id = ? AND permission = 'server.view')
ORDER BY name;

-- name: RbacGetServer :one
SELECT * FROM servers WHERE id = ?;

-- name: RbacGetServerByName :one
SELECT * FROM servers WHERE name = ?;

-- name: RbacCreateServer :one
INSERT INTO servers (name, created_at) VALUES (?, ?)
RETURNING *;

-- name: RbacDeleteServer :execrows
DELETE FROM servers WHERE id = ?;

-- name: RbacHasPermission :one
SELECT count(*) FROM permissions WHERE user_id = ? AND server_id = ? AND permission = ?;

-- name: RbacListUserPermissions :many
SELECT server_id, permission FROM permissions WHERE user_id = ? ORDER BY server_id, permission;

-- name: RbacListUserServerPermissions :many
SELECT permission FROM permissions WHERE user_id = ? AND server_id = ? ORDER BY permission;

-- name: RbacDeleteUserPermissions :exec
DELETE FROM permissions WHERE user_id = ?;

-- name: RbacInsertPermission :exec
INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?);
```

- [ ] **Step 2: Generieren und Signaturen prüfen**

```bash
cd /Users/jannes/Workspace/mc-panel
sqlc generate
git status --short internal/store
grep -n "^func (q \*Queries) Rbac" internal/store/rbac.sql.go | wc -l
go build ./...
```
Expected: `git status` zeigt nur `?? internal/store/queries/rbac.sql` und `?? internal/store/rbac.sql.go` (`db.go`, `models.go`, `common.sql.go` unverändert); `25` Rbac-Methoden; Build ohne Fehler. Die Signaturen müssen den unter **Interfaces** gelisteten entsprechen. Hinweis zur Typinferenz: Erzeugt sqlc für `RbacListViewableServers` den Parametertyp `interface{}` statt `int64`, kompiliert der Code unverändert (es wird ein `int64` übergeben). Erzeugt sqlc für `RbacLastLogin` `int64` statt `sql.NullInt64`, in `internal/users/users.go` (Task 6) `sql.NullInt64{Int64: u.ID, Valid: true}` durch `u.ID` ersetzen – sonst nichts ändern.

- [ ] **Step 3: Failing test `internal/rbac/checker_test.go`**

Hinweis: Phase 0 definiert in `internal/rbac/router_test.go` (Paket `rbac_test`) bereits `fakeChecker`, `now`, `setup`, `do` – diese Namen hier nicht neu deklarieren.

```go
package rbac_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestDBChecker(t *testing.T) {
	db := storetest.Open(t)
	alice := storetest.CreateUser(t, db, "alice", "user")
	bob := storetest.CreateUser(t, db, "bob", "user")
	srvA := storetest.CreateServer(t, db, "alpha")
	srvB := storetest.CreateServer(t, db, "beta")
	if _, err := db.Exec(`INSERT INTO permissions (user_id, server_id, permission)
		VALUES (?, ?, 'server.view'), (?, ?, 'server.power')`, alice, srvA, alice, srvA); err != nil {
		t.Fatal(err)
	}
	c := rbac.NewDBChecker(db)
	cases := []struct {
		name         string
		user, server int64
		perm         rbac.Permission
		want         bool
	}{
		{"granted view", alice, srvA, rbac.PermServerView, true},
		{"granted power", alice, srvA, rbac.PermServerPower, true},
		{"not granted console", alice, srvA, rbac.PermServerConsole, false},
		{"other server", alice, srvB, rbac.PermServerView, false},
		{"other user", bob, srvA, rbac.PermServerView, false},
		{"unknown server", alice, 9999, rbac.PermServerView, false},
		{"invalid permission", alice, srvA, rbac.Permission("server.root"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.HasServerPermission(context.Background(), tc.user, tc.server, tc.perm)
			if err != nil || got != tc.want {
				t.Fatalf("HasServerPermission = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestDBCheckerThroughRouter(t *testing.T) {
	db := storetest.Open(t)
	alice := storetest.CreateUser(t, db, "alice", "user")
	bob := storetest.CreateUser(t, db, "bob", "user")
	srv := storetest.CreateServer(t, db, "alpha")
	if _, err := db.Exec(`INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, 'server.power')`, alice, srv); err != nil {
		t.Fatal(err)
	}
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clock.NewFake(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)))
	rt.Post("/servers/{id}/power", rbac.Server(rbac.PermServerPower, "id"), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	for _, tc := range []struct {
		name string
		uid  int64
		code int
	}{
		{"alice has server.power", alice, http.StatusNoContent},
		{"bob has nothing", bob, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/servers/%d/power", srv), nil)
			req = req.WithContext(principal.With(req.Context(), principal.Principal{
				UserID: tc.uid, Role: principal.RoleUser, Stage: principal.StageFull,
			}))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.code {
				t.Fatalf("code = %d, want %d (body %s)", rec.Code, tc.code, rec.Body)
			}
		})
	}
}
```

- [ ] **Step 4: Run** `go test ./internal/rbac/ -run 'TestDBChecker' -v`
Expected: FAIL – Compilerfehler `undefined: rbac.NewDBChecker`.

- [ ] **Step 5: Implementation `internal/rbac/checker.go`**

```go
package rbac

import (
	"context"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/store"
)

// DBChecker answers per-server permission questions from the permissions table.
// Admins never reach the checker: the router lets them pass before asking it.
// server.view is stored explicitly (see users: granting any permission implies view),
// so the checker only needs an exact lookup.
type DBChecker struct {
	q *store.Queries
}

var _ Checker = (*DBChecker)(nil)

func NewDBChecker(db store.DBTX) *DBChecker {
	return &DBChecker{q: store.New(db)}
}

func (c *DBChecker) HasServerPermission(ctx context.Context, userID, serverID int64, p Permission) (bool, error) {
	if !p.Valid() {
		return false, nil
	}
	n, err := c.q.RbacHasPermission(ctx, store.RbacHasPermissionParams{
		UserID:     userID,
		ServerID:   serverID,
		Permission: string(p),
	})
	if err != nil {
		return false, fmt.Errorf("rbac: check permission: %w", err)
	}
	return n > 0, nil
}
```

- [ ] **Step 6: Run** `go test ./internal/rbac/ -v`
Expected: PASS (`TestDBChecker`, `TestDBCheckerThroughRouter` und die Phase-0-Tests `TestEnforcement`, `TestRoutesRegistry`, `TestZeroRulePanics`); letzte Zeile `ok  	github.com/Bernotese/mc-panel/internal/rbac`.

- [ ] **Step 7: Commit**

```bash
git add internal/store/queries/rbac.sql internal/store/rbac.sql.go internal/rbac/checker.go internal/rbac/checker_test.go
git commit -m "feat(rbac): add sqlc queries and DB-backed permission checker" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Route-Coverage-Helfer `rbac.Uncovered`

**Files:**
- Create: `internal/rbac/coverage.go`
- Test: `internal/rbac/coverage_test.go`

**Interfaces:**
- Consumes: `chi.Walk`, `chi.Routes`, `rbac.RouteInfo`, `(*rbac.Router).Routes()` (Phase 0); `fakeChecker` aus `internal/rbac/router_test.go` (Phase 0, gleiches Testpaket).
- Produces:
```go
// Returns sorted "METHOD /pattern" entries present in mux but not in registered.
func Uncovered(mux chi.Routes, registered []RouteInfo, ignore func(method, pattern string) bool) []string
```

Verhalten von chi (relevant für den Vergleich): `chi.Walk` setzt Pfade von Sub-Routern zusammen (`/api/*` + `/health` → `/api/health`) – identisch zu `RouteInfo.Pattern` des `rbac.Router` nach `Route("/api", …)`. Die von `Mount` zusätzlich angelegten Stub-Knoten (`/api`, `/api/`) liefert `Routes()` nicht. `mux.Handle("/*", …)` erscheint für jede HTTP-Methode als `/*`; der Aufrufer ignoriert das per `ignore`.

- [ ] **Step 1: Failing test `internal/rbac/coverage_test.go`**

```go
package rbac_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

func noopHandler(http.ResponseWriter, *http.Request) {}

func buildCoverageMux(catchAll bool) (*chi.Mux, *rbac.Router) {
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, fakeChecker{}, clock.Real{})
	rt.Route("/api", func(api *rbac.Router) {
		api.Get("/health", rbac.Public(), noopHandler)
		api.Post("/servers/{id}/power", rbac.Server(rbac.PermServerPower, "id"), noopHandler)
	})
	// Routes registered directly on chi bypass rule enforcement and must be reported.
	mux.Post("/debug/reset", noopHandler)
	mux.Route("/internal", func(r chi.Router) { r.Get("/x", noopHandler) })
	if catchAll {
		mux.Handle("/*", http.HandlerFunc(noopHandler))
	}
	return mux, rt
}

func TestUncoveredReportsRawChiRoutes(t *testing.T) {
	want := []string{"GET /internal/x", "POST /debug/reset"}

	mux, rt := buildCoverageMux(false)
	if got := rbac.Uncovered(mux, rt.Routes(), nil); !slices.Equal(got, want) {
		t.Fatalf("Uncovered = %v, want %v", got, want)
	}

	mux, rt = buildCoverageMux(true)
	ignoreSPA := func(_, pattern string) bool { return pattern == "/*" }
	if got := rbac.Uncovered(mux, rt.Routes(), ignoreSPA); !slices.Equal(got, want) {
		t.Fatalf("Uncovered with ignore = %v, want %v", got, want)
	}
	if got := rbac.Uncovered(mux, rt.Routes(), nil); !slices.Contains(got, "GET /*") {
		t.Fatalf("catch-all must be reported without ignore, got %v", got)
	}
}

func TestUncoveredFullyCovered(t *testing.T) {
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, fakeChecker{}, clock.Real{})
	rt.Route("/api", func(api *rbac.Router) {
		api.Get("/users/{id}", rbac.Admin(), noopHandler)
		api.Delete("/users/{id}", rbac.Admin().WithSudo(), noopHandler)
	})
	if got := rbac.Uncovered(mux, rt.Routes(), nil); len(got) != 0 {
		t.Fatalf("Uncovered = %v, want none", got)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/rbac/ -run TestUncovered -v`
Expected: FAIL – `undefined: rbac.Uncovered`.

- [ ] **Step 3: Implementation `internal/rbac/coverage.go`**

```go
package rbac

import (
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
)

// Uncovered returns "METHOD /pattern" for every route reachable in mux that was not
// registered through a Router (and therefore enforces no Rule). ignore may exclude
// deliberate non-API handlers such as the SPA catch-all "/*"; it may be nil.
// The result is sorted; an empty result means every route declares a rule.
func Uncovered(mux chi.Routes, registered []RouteInfo, ignore func(method, pattern string) bool) []string {
	known := make(map[string]bool, len(registered))
	for _, ri := range registered {
		known[ri.Method+" "+ri.Pattern] = true
	}
	seen := make(map[string]bool)
	var out []string
	err := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if known[key] || seen[key] || (ignore != nil && ignore(method, route)) {
			return nil
		}
		seen[key] = true
		out = append(out, key)
		return nil
	})
	if err != nil {
		// The walk function never fails; surface any chi error so callers' tests fail loudly.
		out = append(out, "WALK-ERROR "+err.Error())
	}
	slices.Sort(out)
	return out
}
```

- [ ] **Step 4: Run** `go test ./internal/rbac/ -v`
Expected: PASS (alle Tests inkl. Phase 0); `ok  	github.com/Bernotese/mc-panel/internal/rbac`.

- [ ] **Step 5: Commit**

```bash
git add internal/rbac/coverage.go internal/rbac/coverage_test.go
git commit -m "feat(rbac): add route coverage helper" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 3: Audit-Queries, Kanonisierung und `SQLRecorder`

**Files:**
- Create: `internal/store/queries/audit.sql`, `internal/audit/canonical.go`, `internal/audit/sqlrecorder.go`
- Generated: `internal/store/audit.sql.go`
- Test: `internal/audit/canonical_test.go`, `internal/audit/helpers_test.go`, `internal/audit/sqlrecorder_test.go`

**Interfaces:**
- Consumes: `audit.Entry`, `audit.Recorder`, `audit.Result*`, `audit.Actor*`, `audit.Action*` (Phase 0 `types.go`); `principal.From`; `httpx.ClientIP`, `httpx.WithClientIP`; `clock.Clock`, `clock.NewFake`; `store.Open`, `store.Migrate`, Modell `store.AuditLog{ID, Ts int64; ActorType string; ActorID sql.NullInt64; ActorName, Action, Target, Ip, Result, Details, PrevHash, Hash string}`.
- Produces (generiert):
```go
func (q *Queries) AuditLast(ctx context.Context) (AuditLastRow, error)                       // AuditLastRow{ID int64; Hash string}
func (q *Queries) AuditInsert(ctx context.Context, arg AuditInsertParams) error
	// AuditInsertParams{ID, Ts int64; ActorType string; ActorID sql.NullInt64; ActorName, Action, Target, Ip, Result, Details, PrevHash, Hash string}
func (q *Queries) AuditListAfter(ctx context.Context, arg AuditListAfterParams) ([]AuditLog, error) // {ID int64; Limit int64}
func (q *Queries) AuditList(ctx context.Context, arg AuditListParams) ([]AuditLog, error)
	// AuditListParams{ActorGlob, ActionGlob, ResultGlob; FromTs, ToTs, BeforeID, MaxRows} (types inferred by sqlc: string / int64)
```
- Produces (Code):
```go
func Canonical(row store.AuditLog) []byte
func ComputeHash(prevHash string, row store.AuditLog) string
func CanonicalDetails(d map[string]any) (string, error)
type SQLRecorder struct{ /* unexported */ }
func NewSQLRecorder(db *sql.DB, c clock.Clock) *SQLRecorder
func (r *SQLRecorder) Record(ctx context.Context, e Entry) error
```

- [ ] **Step 1: `internal/store/queries/audit.sql` schreiben**

```sql
-- Stream D: audit log. All query names start with "Audit".

-- name: AuditLast :one
SELECT id, hash FROM audit_log ORDER BY id DESC LIMIT 1;

-- name: AuditInsert :exec
INSERT INTO audit_log (id, ts, actor_type, actor_id, actor_name, action, target, ip, result, details, prev_hash, hash)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: AuditListAfter :many
SELECT * FROM audit_log WHERE id > ? ORDER BY id LIMIT ?;

-- name: AuditList :many
SELECT * FROM audit_log
WHERE actor_name GLOB sqlc.arg(actor_glob)
  AND action GLOB sqlc.arg(action_glob)
  AND result GLOB sqlc.arg(result_glob)
  AND ts >= sqlc.arg(from_ts)
  AND ts <= sqlc.arg(to_ts)
  AND id < sqlc.arg(before_id)
ORDER BY id DESC
LIMIT sqlc.arg(max_rows);
```

- [ ] **Step 2: Generieren**

```bash
sqlc generate
git status --short internal/store
grep -n "^func (q \*Queries) Audit" internal/store/audit.sql.go
go build ./...
```
Expected: neu nur `internal/store/queries/audit.sql` und `internal/store/audit.sql.go`; vier Methoden `AuditInsert`, `AuditLast`, `AuditList`, `AuditListAfter`; Build ok. Hinweis: Leitet sqlc für die `GLOB`-/`LIMIT`-Parameter `interface{}` statt `string`/`int64` ab, kompiliert der Code in Task 5 trotzdem, weil dort immer bereits typisierte lokale Variablen zugewiesen werden.

- [ ] **Step 3: Failing test `internal/audit/canonical_test.go`**

```go
package audit_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store"
)

func TestCanonicalAndHashVector(t *testing.T) {
	row := store.AuditLog{
		ID: 1, Ts: 1760000000000, ActorType: "user", ActorID: sql.NullInt64{Int64: 7, Valid: true},
		ActorName: "jannes", Action: "user.create", Target: "user:8", Ip: "203.0.113.7", Result: "success",
		Details: `{"role":"user","username":"alice"}`,
		// PrevHash and Hash are not part of the canonical form.
		PrevHash: "ignored", Hash: "ignored",
	}
	wantCanon := `[1,1760000000000,"user",7,"jannes","user.create","user:8","203.0.113.7","success","{\"role\":\"user\",\"username\":\"alice\"}"]`
	if got := string(audit.Canonical(row)); got != wantCanon {
		t.Fatalf("Canonical =\n%s\nwant\n%s", got, wantCanon)
	}
	first := sha256.Sum256([]byte(wantCanon))
	if got := audit.ComputeHash("", row); got != hex.EncodeToString(first[:]) {
		t.Fatalf("ComputeHash(\"\") = %s", got)
	}
	prev := strings.Repeat("ab", 32)
	second := sha256.Sum256([]byte(prev + wantCanon))
	if got := audit.ComputeHash(prev, row); got != hex.EncodeToString(second[:]) || len(got) != 64 {
		t.Fatalf("ComputeHash(prev) = %s", got)
	}

	sys := row
	sys.ActorType, sys.ActorID, sys.ActorName = "system", sql.NullInt64{}, "system"
	if !strings.Contains(string(audit.Canonical(sys)), `,"system",null,"system",`) {
		t.Fatalf("NULL actor_id must encode as null: %s", audit.Canonical(sys))
	}
}

func TestCanonicalDetails(t *testing.T) {
	got, err := audit.CanonicalDetails(map[string]any{"b": map[string]any{"d": "x", "c": true}, "a": 1})
	if err != nil || got != `{"a":1,"b":{"c":true,"d":"x"}}` {
		t.Fatalf("CanonicalDetails = %q, %v", got, err)
	}
	if got, err := audit.CanonicalDetails(nil); err != nil || got != "{}" {
		t.Fatalf("nil details = %q, %v", got, err)
	}
	if _, err := audit.CanonicalDetails(map[string]any{"c": make(chan int)}); err == nil {
		t.Fatal("unencodable details must fail")
	}
}
```

- [ ] **Step 4: Run** `go test ./internal/audit/ -run 'TestCanonical' -v`
Expected: FAIL – `undefined: audit.Canonical`.

- [ ] **Step 5: Implementation `internal/audit/canonical.go`**

```go
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/store"
)

// Canonical returns the exact bytes hashed for one audit row: the compact JSON array
//
//	[id, ts, actor_type, actor_id|null, actor_name, action, target, ip, result, details]
//
// encoded with encoding/json (default HTML escaping). details is the stored TEXT,
// embedded as a JSON string, so verification never re-encodes it.
func Canonical(row store.AuditLog) []byte {
	var actorID any
	if row.ActorID.Valid {
		actorID = row.ActorID.Int64
	}
	b, err := json.Marshal([]any{
		row.ID, row.Ts, row.ActorType, actorID, row.ActorName,
		row.Action, row.Target, row.Ip, row.Result, row.Details,
	})
	if err != nil {
		// Marshalling int64, string and nil values cannot fail.
		panic(fmt.Sprintf("audit: canonical: %v", err))
	}
	return b
}

// ComputeHash returns hex(SHA-256(prevHash || Canonical(row))).
func ComputeHash(prevHash string, row store.AuditLog) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write(Canonical(row))
	return hex.EncodeToString(h.Sum(nil))
}

// CanonicalDetails encodes details with encoding/json, which sorts map keys at every
// level. nil or empty details are stored as "{}".
func CanonicalDetails(d map[string]any) (string, error) {
	if len(d) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return "", fmt.Errorf("audit: details: %w", err)
	}
	return string(b), nil
}
```

- [ ] **Step 6: Run** `go test ./internal/audit/ -run 'TestCanonical' -v` → PASS.

- [ ] **Step 7: Test-Helfer `internal/audit/helpers_test.go`**

```go
package audit_test

import (
	"context"
	"database/sql"
	"net/netip"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// userCtx returns a request-like context with an authenticated principal and client IP 203.0.113.7.
func userCtx(id int64, name string) context.Context {
	ctx := principal.With(context.Background(), principal.Principal{
		UserID: id, Username: name, Role: principal.RoleAdmin, Stage: principal.StageFull,
	})
	return httpx.WithClientIP(ctx, netip.MustParseAddr("203.0.113.7"))
}

// readAll returns every audit row in id order.
func readAll(t *testing.T, db *sql.DB) []store.AuditLog {
	t.Helper()
	rows, err := store.New(db).AuditListAfter(context.Background(), store.AuditListAfterParams{ID: 0, Limit: 100000})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// assertChain checks that ids are 1..n without gaps and every hash links to its predecessor.
func assertChain(t *testing.T, rows []store.AuditLog) {
	t.Helper()
	prev := ""
	for i, row := range rows {
		if row.ID != int64(i+1) {
			t.Fatalf("row %d has id %d, want %d", i, row.ID, i+1)
		}
		if row.PrevHash != prev {
			t.Fatalf("row %d prev_hash = %q, want %q", row.ID, row.PrevHash, prev)
		}
		if want := audit.ComputeHash(prev, row); row.Hash != want {
			t.Fatalf("row %d hash = %q, want %q", row.ID, row.Hash, want)
		}
		prev = row.Hash
	}
}
```

- [ ] **Step 8: Failing test `internal/audit/sqlrecorder_test.go`**

```go
package audit_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestRecordFillsActorAndChains(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	rec := audit.NewSQLRecorder(db, clk)

	if err := rec.Record(userCtx(7, "jannes"), audit.Entry{
		Action: audit.ActionUserCreate, Target: "user:8", Result: audit.ResultSuccess,
		Details: map[string]any{"username": "alice"},
	}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	if err := rec.Record(context.Background(), audit.Entry{
		Action: audit.ActionJailAuto, Target: "203.0.113.9/32", Result: audit.ResultSuccess,
	}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Record(context.Background(), audit.Entry{
		Action: audit.ActionUserCreate, Target: "user:9", Result: audit.ResultSuccess, ActorType: audit.ActorCLI,
	}); err != nil {
		t.Fatal(err)
	}

	rows := readAll(t, db)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	assertChain(t, rows)
	u, s, c := rows[0], rows[1], rows[2]
	if u.Ts != t0.UnixMilli() || u.ActorType != "user" || u.ActorID != (sql.NullInt64{Int64: 7, Valid: true}) ||
		u.ActorName != "jannes" || u.Ip != "203.0.113.7" || u.Details != `{"username":"alice"}` ||
		u.PrevHash != "" || len(u.Hash) != 64 || u.Result != "success" || u.Target != "user:8" {
		t.Fatalf("user row = %+v", u)
	}
	if s.Ts != t0.Add(time.Second).UnixMilli() || s.ActorType != "system" || s.ActorID.Valid ||
		s.ActorName != "system" || s.Ip != "" || s.Details != "{}" {
		t.Fatalf("system row = %+v", s)
	}
	if c.ActorType != "cli" || c.ActorName != "cli" || c.ActorID.Valid {
		t.Fatalf("cli row = %+v", c)
	}
}

func TestRecordRejectsInvalidEntries(t *testing.T) {
	db := storetest.Open(t)
	rec := audit.NewSQLRecorder(db, clock.NewFake(t0))
	cases := map[string]audit.Entry{
		"missing action":      {Result: audit.ResultSuccess},
		"empty result":        {Action: "x.y"},
		"invalid result":      {Action: "x.y", Result: "ok"},
		"unknown actor type":  {Action: "x.y", Result: audit.ResultSuccess, ActorType: "robot"},
		"user without name":   {Action: "x.y", Result: audit.ResultSuccess, ActorType: audit.ActorUser},
		"unencodable details": {Action: "x.y", Result: audit.ResultSuccess, Details: map[string]any{"c": make(chan int)}},
	}
	for name, e := range cases {
		if err := rec.Record(context.Background(), e); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if n := len(readAll(t, db)); n != 0 {
		t.Fatalf("invalid entries were written: %d rows", n)
	}
}

func TestRecordSurvivesCancelledContext(t *testing.T) {
	db := storetest.Open(t)
	rec := audit.NewSQLRecorder(db, clock.NewFake(t0))
	ctx, cancel := context.WithCancel(userCtx(1, "root"))
	cancel() // client went away after the action was performed
	if err := rec.Record(ctx, audit.Entry{Action: audit.ActionUserDelete, Target: "user:2", Result: audit.ResultSuccess}); err != nil {
		t.Fatalf("Record with cancelled context: %v", err)
	}
	if n := len(readAll(t, db)); n != 1 {
		t.Fatalf("got %d rows, want 1", n)
	}
}

// Review Focus 5: 50 concurrent writers must produce a gap-free, valid chain.
func TestRecordConcurrent(t *testing.T) {
	db := storetest.Open(t)
	rec := audit.NewSQLRecorder(db, clock.NewFake(t0))
	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- rec.Record(userCtx(int64(i+1), fmt.Sprintf("user%d", i)), audit.Entry{
				Action: audit.ActionServerPower, Target: fmt.Sprintf("server:%d", i), Result: audit.ResultSuccess,
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows := readAll(t, db)
	if len(rows) != n {
		t.Fatalf("got %d rows, want %d", len(rows), n)
	}
	assertChain(t, rows)
}

// Review Focus (stream): panel and CLI are separate processes with separate mutexes.
// Only BEGIN IMMEDIATE (store.Open sets _txlock=immediate) keeps the chain linear.
func TestRecordConcurrentAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	open := func() *sql.DB {
		db, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	panelDB := open()
	if err := store.Migrate(context.Background(), panelDB); err != nil {
		t.Fatal(err)
	}
	cliDB := open()
	recorders := []*audit.SQLRecorder{
		audit.NewSQLRecorder(panelDB, clock.NewFake(t0)),
		audit.NewSQLRecorder(cliDB, clock.NewFake(t0)),
	}
	const perRecorder = 25
	var wg sync.WaitGroup
	errs := make(chan error, len(recorders)*perRecorder)
	for ri, rec := range recorders {
		for i := range perRecorder {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- rec.Record(context.Background(), audit.Entry{
					Action: audit.ActionUserCreate, Target: fmt.Sprintf("r%d:%d", ri, i),
					Result: audit.ResultSuccess, ActorType: audit.ActorCLI,
				})
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows := readAll(t, panelDB)
	if len(rows) != len(recorders)*perRecorder {
		t.Fatalf("got %d rows, want %d", len(rows), len(recorders)*perRecorder)
	}
	assertChain(t, rows)
}
```

- [ ] **Step 9: Run** `go test ./internal/audit/ -run 'TestRecord' -v`
Expected: FAIL – `undefined: audit.NewSQLRecorder`.

- [ ] **Step 10: Implementation `internal/audit/sqlrecorder.go`**

```go
package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// SQLRecorder appends entries to audit_log and links them with a SHA-256 hash chain.
// db must come from store.Open (it sets _txlock=immediate, so BeginTx issues
// BEGIN IMMEDIATE). Use exactly one SQLRecorder per *sql.DB and process.
type SQLRecorder struct {
	db    *sql.DB
	clock clock.Clock
	mu    sync.Mutex
}

var _ Recorder = (*SQLRecorder)(nil)

func NewSQLRecorder(db *sql.DB, c clock.Clock) *SQLRecorder {
	return &SQLRecorder{db: db, clock: c}
}

// Record writes one entry. It must not be called while the caller holds an open write
// transaction on the same database (it would wait for busy_timeout and fail).
func (r *SQLRecorder) Record(ctx context.Context, e Entry) error {
	e, err := normalize(ctx, e)
	if err != nil {
		return err
	}
	details, err := CanonicalDetails(e.Details)
	if err != nil {
		return err
	}
	// The action already happened; the entry must be written even if the request is cancelled.
	ctx = context.WithoutCancel(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()
	// BEGIN IMMEDIATE: reading the last hash and inserting the next row is serialised
	// across processes (panel + CLI); the mutex avoids lock contention inside this process.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("audit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := store.New(tx)

	last, err := q.AuditLast(ctx)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		last = store.AuditLastRow{}
	case err != nil:
		return fmt.Errorf("audit: read last entry: %w", err)
	}

	row := store.AuditLog{
		ID:        last.ID + 1,
		Ts:        r.clock.Now().UnixMilli(),
		ActorType: string(e.ActorType),
		ActorName: e.ActorName,
		Action:    e.Action,
		Target:    e.Target,
		Ip:        e.IP,
		Result:    string(e.Result),
		Details:   details,
		PrevHash:  last.Hash,
	}
	if e.ActorID != nil {
		row.ActorID = sql.NullInt64{Int64: *e.ActorID, Valid: true}
	}
	row.Hash = ComputeHash(row.PrevHash, row)

	if err := q.AuditInsert(ctx, store.AuditInsertParams{
		ID: row.ID, Ts: row.Ts, ActorType: row.ActorType, ActorID: row.ActorID, ActorName: row.ActorName,
		Action: row.Action, Target: row.Target, Ip: row.Ip, Result: row.Result, Details: row.Details,
		PrevHash: row.PrevHash, Hash: row.Hash,
	}); err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("audit: commit: %w", err)
	}
	return nil
}

// normalize validates e and fills actor and IP fields from the context.
func normalize(ctx context.Context, e Entry) (Entry, error) {
	if e.Action == "" {
		return e, errors.New("audit: action is required")
	}
	switch e.Result {
	case ResultSuccess, ResultFailure, ResultDenied:
	default:
		return e, fmt.Errorf("audit: invalid result %q", e.Result)
	}
	switch e.ActorType {
	case "":
		if p, ok := principal.From(ctx); ok {
			id := p.UserID
			e.ActorType, e.ActorID, e.ActorName = ActorUser, &id, p.Username
		} else {
			e.ActorType, e.ActorID, e.ActorName = ActorSystem, nil, string(ActorSystem)
		}
	case ActorUser:
		if e.ActorName == "" {
			return e, errors.New("audit: user actor requires a name")
		}
	case ActorSystem, ActorCLI:
		if e.ActorName == "" {
			e.ActorName = string(e.ActorType)
		}
	default:
		return e, fmt.Errorf("audit: invalid actor type %q", e.ActorType)
	}
	if e.IP == "" {
		if ip := httpx.ClientIP(ctx); ip.IsValid() {
			e.IP = ip.String()
		}
	}
	return e, nil
}
```

- [ ] **Step 11: Run** `go test ./internal/audit/ -v`
Expected: PASS (`TestCanonicalAndHashVector`, `TestCanonicalDetails`, `TestRecordFillsActorAndChains`, `TestRecordRejectsInvalidEntries`, `TestRecordSurvivesCancelledContext`, `TestRecordConcurrent`, `TestRecordConcurrentAcrossConnections`).

- [ ] **Step 12: Race-Detector** `CGO_ENABLED=1 go test -race ./internal/audit/`
Expected: `ok  	github.com/Bernotese/mc-panel/internal/audit` ohne `DATA RACE`.

- [ ] **Step 13: Commit**

```bash
git add internal/store/queries/audit.sql internal/store/audit.sql.go internal/audit/canonical.go internal/audit/canonical_test.go internal/audit/sqlrecorder.go internal/audit/helpers_test.go internal/audit/sqlrecorder_test.go
git commit -m "feat(audit): add hash-chained SQLite recorder" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 4: Ketten-Prüfung `Verify` und CLI `RunAuditVerify`

**Files:**
- Create: `internal/audit/verify.go`, `internal/audit/cli.go`
- Test: `internal/audit/verify_test.go`, `internal/audit/cli_test.go`

**Interfaces:**
- Consumes: `ComputeHash`, `NewSQLRecorder` (Task 3); `store.AuditListAfter`, `store.AuditInsert` (Task 3); Test-Helfer `t0`, `userCtx`, `readAll` (Task 3, `helpers_test.go`).
- Produces:
```go
type VerifyReport struct {
	OK         bool
	Checked    int   // entries verified successfully (before FirstBadID, if any)
	FirstBadID int64 // 0 when OK
}
func Verify(ctx context.Context, db *sql.DB) (VerifyReport, error)
var ErrChainBroken = errors.New("audit: hash chain broken")
func RunAuditVerify(ctx context.Context, db *sql.DB, stdout io.Writer) error // nil = OK; errors.Is(err, ErrChainBroken) when broken
```
- Test-Helfer (Paket `audit_test`, von Task 5 mitbenutzt): `seed(t, db, n)`, `insertChain(t, db, n)`, `mustExec(t, db, query, args...)`.

- [ ] **Step 1: Failing test `internal/audit/verify_test.go`**

```go
package audit_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// seed records n entries through the real recorder (one second apart).
func seed(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	clk := clock.NewFake(t0)
	rec := audit.NewSQLRecorder(db, clk)
	for i := range n {
		if err := rec.Record(userCtx(1, "root"), audit.Entry{
			Action: audit.ActionUserCreate, Target: fmt.Sprintf("user:%d", i+2),
			Result: audit.ResultSuccess, Details: map[string]any{"i": i},
		}); err != nil {
			t.Fatal(err)
		}
		clk.Advance(time.Second)
	}
}

// insertChain writes n valid entries in one transaction (fast path for large chains).
func insertChain(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	q := store.New(tx)
	prev := ""
	for i := 1; i <= n; i++ {
		row := store.AuditLog{
			ID: int64(i), Ts: t0.UnixMilli() + int64(i), ActorType: "system", ActorName: "system",
			Action: audit.ActionJailAuto, Result: "success", Details: "{}", PrevHash: prev,
		}
		row.Hash = audit.ComputeHash(prev, row)
		if err := q.AuditInsert(ctx, store.AuditInsertParams{
			ID: row.ID, Ts: row.Ts, ActorType: row.ActorType, ActorID: row.ActorID, ActorName: row.ActorName,
			Action: row.Action, Target: row.Target, Ip: row.Ip, Result: row.Result, Details: row.Details,
			PrevHash: row.PrevHash, Hash: row.Hash,
		}); err != nil {
			t.Fatal(err)
		}
		prev = row.Hash
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func verify(t *testing.T, db *sql.DB) audit.VerifyReport {
	t.Helper()
	rep, err := audit.Verify(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestVerifyEmptyLog(t *testing.T) {
	db := storetest.Open(t)
	if rep := verify(t, db); !rep.OK || rep.Checked != 0 || rep.FirstBadID != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyValidChain(t *testing.T) {
	db := storetest.Open(t)
	seed(t, db, 5)
	if rep := verify(t, db); !rep.OK || rep.Checked != 5 || rep.FirstBadID != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyLargeChainAcrossBatches(t *testing.T) {
	db := storetest.Open(t)
	insertChain(t, db, 2500)
	if rep := verify(t, db); !rep.OK || rep.Checked != 2500 {
		t.Fatalf("report = %+v", rep)
	}
}

// Spec §12.6: manual manipulation of a row must make verify fail at that row.
func TestVerifyDetectsModifiedRow(t *testing.T) {
	db := storetest.Open(t)
	seed(t, db, 3)
	mustExec(t, db, "DROP TRIGGER audit_log_no_update")
	mustExec(t, db, "UPDATE audit_log SET target = 'user:999' WHERE id = 2")
	if rep := verify(t, db); rep.OK || rep.Checked != 1 || rep.FirstBadID != 2 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyDetectsRecomputedRow(t *testing.T) {
	db := storetest.Open(t)
	seed(t, db, 3)
	row := readAll(t, db)[1]
	row.Action = audit.ActionUserDelete
	newHash := audit.ComputeHash(row.PrevHash, row) // attacker fixes the row's own hash ...
	mustExec(t, db, "DROP TRIGGER audit_log_no_update")
	mustExec(t, db, "UPDATE audit_log SET action = ?, hash = ? WHERE id = 2", row.Action, newHash)
	// ... but the successor still points to the old hash.
	if rep := verify(t, db); rep.OK || rep.Checked != 2 || rep.FirstBadID != 3 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyDetectsDeletedRow(t *testing.T) {
	db := storetest.Open(t)
	seed(t, db, 3)
	mustExec(t, db, "DROP TRIGGER audit_log_no_delete")
	mustExec(t, db, "DELETE FROM audit_log WHERE id = 2")
	if rep := verify(t, db); rep.OK || rep.Checked != 1 || rep.FirstBadID != 3 {
		t.Fatalf("report = %+v", rep)
	}
}
```

- [ ] **Step 2: Failing test `internal/audit/cli_test.go`**

```go
package audit_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestRunAuditVerify(t *testing.T) {
	db := storetest.Open(t)
	seed(t, db, 2)
	var out bytes.Buffer
	if err := audit.RunAuditVerify(context.Background(), db, &out); err != nil {
		t.Fatalf("RunAuditVerify on valid chain: %v", err)
	}
	if !strings.Contains(out.String(), "audit chain OK (2 entries)") {
		t.Fatalf("output = %q", out.String())
	}

	mustExec(t, db, "DROP TRIGGER audit_log_no_update")
	mustExec(t, db, "UPDATE audit_log SET ip = '198.51.100.1' WHERE id = 1")
	out.Reset()
	err := audit.RunAuditVerify(context.Background(), db, &out)
	if !errors.Is(err, audit.ErrChainBroken) {
		t.Fatalf("err = %v, want ErrChainBroken", err)
	}
	if !strings.Contains(out.String(), "audit chain BROKEN at entry id 1") {
		t.Fatalf("output = %q", out.String())
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/audit/ -run 'TestVerify|TestRunAuditVerify' -v`
Expected: FAIL – `undefined: audit.VerifyReport`, `undefined: audit.Verify`, `undefined: audit.RunAuditVerify`.

- [ ] **Step 4: Implementation `internal/audit/verify.go`**

```go
package audit

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/store"
)

const verifyBatch = 1000

// VerifyReport is the result of checking the whole hash chain.
type VerifyReport struct {
	OK         bool
	Checked    int   // entries verified successfully (before FirstBadID, if any)
	FirstBadID int64 // 0 when OK
}

// Verify recomputes the chain from the first to the last entry. An entry is bad if its id
// is not the predecessor's id + 1, its prev_hash differs from the predecessor's hash, or its
// hash differs from ComputeHash. Removing the newest entries cannot be detected without an
// external anchor; that is out of scope for sub-project 1.
func Verify(ctx context.Context, db *sql.DB) (VerifyReport, error) {
	q := store.New(db)
	var (
		prevHash string
		lastID   int64
		checked  int
	)
	for {
		rows, err := q.AuditListAfter(ctx, store.AuditListAfterParams{ID: lastID, Limit: verifyBatch})
		if err != nil {
			return VerifyReport{}, fmt.Errorf("audit: verify: %w", err)
		}
		if len(rows) == 0 {
			return VerifyReport{OK: true, Checked: checked}, nil
		}
		for _, row := range rows {
			if row.ID != lastID+1 || row.PrevHash != prevHash || row.Hash != ComputeHash(prevHash, row) {
				return VerifyReport{Checked: checked, FirstBadID: row.ID}, nil
			}
			prevHash, lastID = row.Hash, row.ID
			checked++
		}
	}
}
```

- [ ] **Step 5: Implementation `internal/audit/cli.go`**

```go
package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
)

// ErrChainBroken is returned by RunAuditVerify when the hash chain is invalid.
var ErrChainBroken = errors.New("audit: hash chain broken")

// RunAuditVerify implements `panel audit verify`. It prints a one-line result to stdout and
// returns nil (exit 0) for a valid chain, an error wrapping ErrChainBroken (exit 1) otherwise.
func RunAuditVerify(ctx context.Context, db *sql.DB, stdout io.Writer) error {
	rep, err := Verify(ctx, db)
	if err != nil {
		return fmt.Errorf("audit verify: %w", err)
	}
	if rep.OK {
		fmt.Fprintf(stdout, "audit chain OK (%d entries)\n", rep.Checked)
		return nil
	}
	fmt.Fprintf(stdout, "audit chain BROKEN at entry id %d (%d entries verified before it)\n", rep.FirstBadID, rep.Checked)
	return fmt.Errorf("%w at entry id %d", ErrChainBroken, rep.FirstBadID)
}
```

- [ ] **Step 6: Run** `go test ./internal/audit/ -v`
Expected: PASS (alle Tests aus Task 3 und 4).

- [ ] **Step 7: Commit**

```bash
git add internal/audit/verify.go internal/audit/verify_test.go internal/audit/cli.go internal/audit/cli_test.go
git commit -m "feat(audit): add chain verification and audit verify CLI" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 5: Audit-Endpunkte `GET /audit` und `GET /audit/verify`

**Files:**
- Create: `internal/audit/handler.go`
- Test: `internal/audit/handler_test.go`

**Interfaces:**
- Consumes: `Verify` (Task 4); `store.AuditList`, `store.AuditListParams` (Task 3); `rbac.Router`, `rbac.Admin()` (Phase 0); `rbac.NewDBChecker` (Task 1); `notify.Notifier`, `notify.EventAuditChainBroken`, `notify.Recording`; `httpx.WriteJSON`, `httpx.WriteError`, `httpx.Internal`, `httpx.CodeValidation`; Test-Helfer `t0`, `userCtx` (Task 3), `mustExec` (Task 4).
- Produces:
```go
type Handler struct{ /* unexported */ }
func NewHandler(db *sql.DB, c clock.Clock, n notify.Notifier) *Handler
func (h *Handler) Routes(api *rbac.Router)
	// GET /audit         rbac.Admin()  → 200 AuditPage
	// GET /audit/verify  rbac.Admin()  → 200 AuditVerify
```
JSON: `AuditEntry{id, ts, actor_type, actor_id|null, actor_name, action, target, ip, result, details(object), hash}`, `AuditPage{items, next_cursor|null}`, `AuditVerify{ok, checked, first_bad_id|null}`.

- [ ] **Step 1: Failing test `internal/audit/handler_test.go`**

```go
package audit_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var rootAdmin = principal.Principal{UserID: 1, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull}

type auditEnv struct {
	db    *sql.DB
	notes *notify.Recording
	mux   http.Handler
}

// newAuditEnv seeds five entries, one minute apart starting at t0:
//
//	1 root  user.create        success  2 root  user.delete  success
//	3 system security.jail_auto success 4 alice server.power denied {"action":"start"}
//	5 cli   user.create        success
func newAuditEnv(t *testing.T) *auditEnv {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	notes := &notify.Recording{}
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	audit.NewHandler(db, clk, notes).Routes(rt)

	rec := audit.NewSQLRecorder(db, clk)
	entries := []struct {
		ctx context.Context
		e   audit.Entry
	}{
		{userCtx(1, "root"), audit.Entry{Action: audit.ActionUserCreate, Target: "user:2", Result: audit.ResultSuccess, Details: map[string]any{"username": "alice"}}},
		{userCtx(1, "root"), audit.Entry{Action: audit.ActionUserDelete, Target: "user:3", Result: audit.ResultSuccess}},
		{context.Background(), audit.Entry{Action: audit.ActionJailAuto, Target: "198.51.100.1/32", Result: audit.ResultSuccess}},
		{userCtx(2, "alice"), audit.Entry{Action: audit.ActionServerPower, Target: "server:1", Result: audit.ResultDenied, Details: map[string]any{"action": "start"}}},
		{context.Background(), audit.Entry{Action: audit.ActionUserCreate, Target: "user:4", Result: audit.ResultSuccess, ActorType: audit.ActorCLI}},
	}
	for i, en := range entries {
		if i > 0 {
			clk.Advance(time.Minute)
		}
		if err := rec.Record(en.ctx, en.e); err != nil {
			t.Fatal(err)
		}
	}
	return &auditEnv{db: db, notes: notes, mux: mux}
}

func (e *auditEnv) get(path string, p principal.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(principal.With(req.Context(), p))
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

type entryResp struct {
	ID        int64          `json:"id"`
	Ts        int64          `json:"ts"`
	ActorType string         `json:"actor_type"`
	ActorID   *int64         `json:"actor_id"`
	ActorName string         `json:"actor_name"`
	Action    string         `json:"action"`
	Target    string         `json:"target"`
	IP        string         `json:"ip"`
	Result    string         `json:"result"`
	Details   map[string]any `json:"details"`
	Hash      string         `json:"hash"`
}

type pageResp struct {
	Items      []entryResp `json:"items"`
	NextCursor *string     `json:"next_cursor"`
}

func decodePage(t *testing.T, rec *httptest.ResponseRecorder) pageResp {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	var p pageResp
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func pageIDs(p pageResp) []int64 {
	out := make([]int64, 0, len(p.Items))
	for _, it := range p.Items {
		out = append(out, it.ID)
	}
	return out
}

func TestListAuditFilters(t *testing.T) {
	e := newAuditEnv(t)
	ms := func(d time.Duration) int64 { return t0.Add(d).UnixMilli() }
	cases := []struct {
		query string
		want  []int64
	}{
		{"", []int64{5, 4, 3, 2, 1}},
		{"?actor=root", []int64{2, 1}},
		{"?actor=cli", []int64{5}},
		{"?action=user.", []int64{5, 2, 1}},
		{"?action=user.create", []int64{5, 1}},
		{"?action=user*", []int64{}}, // GLOB metacharacters are matched literally
		{"?result=denied", []int64{4}},
		{fmt.Sprintf("?from=%d&to=%d", ms(time.Minute), ms(3*time.Minute)), []int64{4, 3, 2}},
		{"?actor=alice&result=success", []int64{}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			p := decodePage(t, e.get("/audit"+tc.query, rootAdmin))
			if got := pageIDs(p); !slices.Equal(got, tc.want) {
				t.Fatalf("ids = %v, want %v", got, tc.want)
			}
			if p.NextCursor != nil {
				t.Fatalf("next_cursor = %q, want null", *p.NextCursor)
			}
		})
	}
}

func TestListAuditPagination(t *testing.T) {
	e := newAuditEnv(t)
	p := decodePage(t, e.get("/audit?limit=2", rootAdmin))
	if got := pageIDs(p); !slices.Equal(got, []int64{5, 4}) || p.NextCursor == nil || *p.NextCursor != "4" {
		t.Fatalf("page 1 = %v cursor %v", got, p.NextCursor)
	}
	p = decodePage(t, e.get("/audit?limit=2&cursor=4", rootAdmin))
	if got := pageIDs(p); !slices.Equal(got, []int64{3, 2}) || p.NextCursor == nil || *p.NextCursor != "2" {
		t.Fatalf("page 2 = %v cursor %v", got, p.NextCursor)
	}
	p = decodePage(t, e.get("/audit?limit=2&cursor=2", rootAdmin))
	if got := pageIDs(p); !slices.Equal(got, []int64{1}) || p.NextCursor != nil {
		t.Fatalf("page 3 = %v cursor %v", got, p.NextCursor)
	}
	rec := e.get("/audit?actor=nobody", rootAdmin)
	if !strings.Contains(rec.Body.String(), `"items":[]`) || !strings.Contains(rec.Body.String(), `"next_cursor":null`) {
		t.Fatalf("empty page body = %s", rec.Body)
	}
}

func TestListAuditInvalidQuery(t *testing.T) {
	e := newAuditEnv(t)
	for _, q := range []string{
		"?result=bogus", "?limit=0", "?limit=201", "?limit=abc", "?cursor=abc", "?cursor=0",
		"?from=yesterday", "?to=1.5", "?actor=" + strings.Repeat("a", 65), "?action=" + strings.Repeat("a", 65),
	} {
		rec := e.get("/audit"+q, rootAdmin)
		var body httpx.ErrorBody
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusBadRequest || body.Error != httpx.CodeValidation {
			t.Errorf("%s: code=%d error=%q", q, rec.Code, body.Error)
		}
	}
}

func TestListAuditEntryFields(t *testing.T) {
	e := newAuditEnv(t)
	p := decodePage(t, e.get("/audit", rootAdmin))
	byID := map[int64]entryResp{}
	for _, it := range p.Items {
		byID[it.ID] = it
	}
	denied := byID[4]
	if denied.ActorType != "user" || denied.ActorID == nil || *denied.ActorID != 2 || denied.ActorName != "alice" ||
		denied.IP != "203.0.113.7" || denied.Result != "denied" || denied.Details["action"] != "start" ||
		denied.Ts != t0.Add(3*time.Minute).UnixMilli() || len(denied.Hash) != 64 || denied.Target != "server:1" {
		t.Fatalf("entry 4 = %+v", denied)
	}
	sys := byID[3]
	if sys.ActorType != "system" || sys.ActorID != nil || sys.ActorName != "system" || sys.IP != "" || len(sys.Details) != 0 {
		t.Fatalf("entry 3 = %+v", sys)
	}
}

func TestVerifyEndpoint(t *testing.T) {
	e := newAuditEnv(t)
	type verifyResp struct {
		OK         bool   `json:"ok"`
		Checked    int    `json:"checked"`
		FirstBadID *int64 `json:"first_bad_id"`
	}
	rec := e.get("/audit/verify", rootAdmin)
	var v verifyResp
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("code=%d err=%v", rec.Code, err)
	}
	if !v.OK || v.Checked != 5 || v.FirstBadID != nil || !strings.Contains(rec.Body.String(), `"first_bad_id":null`) {
		t.Fatalf("valid chain = %s", rec.Body)
	}
	if n := len(e.notes.Types()); n != 0 {
		t.Fatalf("valid chain must not notify, got %d events", n)
	}

	mustExec(t, e.db, "DROP TRIGGER audit_log_no_update")
	mustExec(t, e.db, "UPDATE audit_log SET target = 'tampered' WHERE id = 3")
	rec = e.get("/audit/verify", rootAdmin)
	v = verifyResp{}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.OK || v.Checked != 2 || v.FirstBadID == nil || *v.FirstBadID != 3 {
		t.Fatalf("broken chain = %s", rec.Body)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.EventAuditChainBroken}) {
		t.Fatalf("notifications = %v", got)
	}
}

func TestAuditEndpointsRequireAdmin(t *testing.T) {
	e := newAuditEnv(t)
	user := principal.Principal{UserID: 2, Username: "alice", Role: principal.RoleUser, Stage: principal.StageFull}
	for _, path := range []string{"/audit", "/audit/verify"} {
		if rec := e.get(path, user); rec.Code != http.StatusForbidden {
			t.Errorf("%s as user: code = %d, want 403", path, rec.Code)
		}
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/audit/ -run 'TestListAudit|TestVerifyEndpoint|TestAuditEndpoints' -v`
Expected: FAIL – `undefined: audit.NewHandler`.

- [ ] **Step 3: Implementation `internal/audit/handler.go`**

```go
package audit

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	maxFilterLen = 64
)

// Handler serves the read-only audit API. There is deliberately no update or delete endpoint.
type Handler struct {
	db       *sql.DB
	q        *store.Queries
	clock    clock.Clock
	notifier notify.Notifier
}

func NewHandler(db *sql.DB, c clock.Clock, n notify.Notifier) *Handler {
	return &Handler{db: db, q: store.New(db), clock: c, notifier: n}
}

// Routes registers the audit routes (paths relative to /api) with the rules from the OpenAPI table.
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/audit", rbac.Admin(), h.handleList)
	api.Get("/audit/verify", rbac.Admin(), h.handleVerify)
}

type entryJSON struct {
	ID        int64           `json:"id"`
	Ts        int64           `json:"ts"`
	ActorType string          `json:"actor_type"`
	ActorID   *int64          `json:"actor_id"`
	ActorName string          `json:"actor_name"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	IP        string          `json:"ip"`
	Result    string          `json:"result"`
	Details   json.RawMessage `json:"details"`
	Hash      string          `json:"hash"`
}

type pageJSON struct {
	Items      []entryJSON `json:"items"`
	NextCursor *string     `json:"next_cursor"`
}

type verifyJSON struct {
	OK         bool   `json:"ok"`
	Checked    int    `json:"checked"`
	FirstBadID *int64 `json:"first_bad_id"`
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	params, limit, msg := parseListQuery(r.URL.Query())
	if msg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
		return
	}
	rows, err := h.q.AuditList(r.Context(), params)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	page := pageJSON{Items: make([]entryJSON, 0, len(rows))}
	if len(rows) > limit {
		rows = rows[:limit]
		next := strconv.FormatInt(rows[limit-1].ID, 10)
		page.NextCursor = &next
	}
	for _, row := range rows {
		page.Items = append(page.Items, toEntryJSON(row))
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) handleVerify(w http.ResponseWriter, r *http.Request) {
	rep, err := Verify(r.Context(), h.db)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := verifyJSON{OK: rep.OK, Checked: rep.Checked}
	if !rep.OK {
		id := rep.FirstBadID
		out.FirstBadID = &id
		h.notifier.Notify(r.Context(), notify.Event{
			Type:    notify.EventAuditChainBroken,
			Title:   "Audit chain broken",
			Message: fmt.Sprintf("The audit log hash chain is broken at entry %d (%d entries verified before it).", rep.FirstBadID, rep.Checked),
			Time:    h.clock.Now(),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func toEntryJSON(row store.AuditLog) entryJSON {
	e := entryJSON{
		ID: row.ID, Ts: row.Ts, ActorType: row.ActorType, ActorName: row.ActorName, Action: row.Action,
		Target: row.Target, IP: row.Ip, Result: row.Result, Details: json.RawMessage(row.Details), Hash: row.Hash,
	}
	if row.ActorID.Valid {
		id := row.ActorID.Int64
		e.ActorID = &id
	}
	if !json.Valid(e.Details) {
		// Only possible after manual tampering; keep the response well-formed.
		e.Details = json.RawMessage("{}")
	}
	return e
}

// globLiteral escapes SQLite GLOB metacharacters so s matches literally.
func globLiteral(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '*', '?', '[':
			b.WriteByte('[')
			b.WriteRune(r)
			b.WriteByte(']')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// parseListQuery returns the query parameters, the page size and a validation message ("" = ok).
func parseListQuery(v url.Values) (store.AuditListParams, int, string) {
	actor, action, result := "*", "*", "*"
	if s := v.Get("actor"); s != "" {
		if len(s) > maxFilterLen {
			return store.AuditListParams{}, 0, fmt.Sprintf("actor must be at most %d characters", maxFilterLen)
		}
		actor = globLiteral(s)
	}
	if s := v.Get("action"); s != "" {
		if len(s) > maxFilterLen {
			return store.AuditListParams{}, 0, fmt.Sprintf("action must be at most %d characters", maxFilterLen)
		}
		action = globLiteral(s) + "*" // prefix match
	}
	if s := v.Get("result"); s != "" {
		switch Result(s) {
		case ResultSuccess, ResultFailure, ResultDenied:
			result = s
		default:
			return store.AuditListParams{}, 0, "result must be success, failure or denied"
		}
	}
	from, to, cursor := int64(math.MinInt64), int64(math.MaxInt64), int64(math.MaxInt64)
	for _, f := range []struct {
		name string
		dst  *int64
	}{{"from", &from}, {"to", &to}, {"cursor", &cursor}} {
		s := v.Get(f.name)
		if s == "" {
			continue
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return store.AuditListParams{}, 0, f.name + " must be an integer"
		}
		*f.dst = n
	}
	if v.Get("cursor") != "" && cursor <= 0 {
		return store.AuditListParams{}, 0, "cursor must be a positive integer"
	}
	limit := defaultLimit
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxLimit {
			return store.AuditListParams{}, 0, fmt.Sprintf("limit must be between 1 and %d", maxLimit)
		}
		limit = n
	}
	return store.AuditListParams{
		ActorGlob:  actor,
		ActionGlob: action,
		ResultGlob: result,
		FromTs:     from,
		ToTs:       to,
		BeforeID:   cursor,
		MaxRows:    int64(limit + 1),
	}, limit, ""
}
```

- [ ] **Step 4: Run** `go test ./internal/audit/ -v`
Expected: PASS (alle Audit-Tests); `ok  	github.com/Bernotese/mc-panel/internal/audit`.

- [ ] **Step 5: Commit**

```bash
git add internal/audit/handler.go internal/audit/handler_test.go
git commit -m "feat(audit): add audit list and verify endpoints" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 6: Benutzer anlegen, auflisten, abrufen

**Files:**
- Create: `internal/users/handler.go`, `internal/users/service.go`, `internal/users/users.go`
- Test: `internal/users/helpers_test.go`, `internal/users/users_test.go`

**Interfaces:**
- Consumes: `store.Rbac*` (Task 1); `rbac.NewDBChecker` (Task 1); `rbac.Router`, `rbac.Admin()`, `rbac.NewRouter` (Phase 0); `password.Validate`, `password.Hash`, `password.Verify`, `password.Err*` (Phase 0); `audit.Recorder`, `audit.MemoryRecorder`, `audit.ActionUserCreate`; `notify.Notifier`, `notify.Recording`, `notify.EventUserCreated`; `principal.*`; `httpx.*`; `clock.*`; `storetest.*`.
- Produces (von Tasks 7–11 genutzt):
```go
type Handler struct{ /* db, q, clock, rec, notifier */ }
func NewHandler(db *sql.DB, c clock.Clock, rec audit.Recorder, n notify.Notifier) *Handler
func (h *Handler) Routes(api *rbac.Router)
var ErrInvalidUsername, ErrInvalidRole, ErrUsernameTaken error
// unexported, used by later tasks in package users:
func newUser(ctx context.Context, q *store.Queries, now time.Time, username, pw, role string) (store.User, error)
func validRole(role string) bool
func isValidationError(err error) bool
func isUniqueViolation(err error) bool
func toUserJSON(ctx context.Context, q *store.Queries, u store.User) (userJSON, error)
func pathID(r *http.Request) (int64, bool)
func (h *Handler) record(ctx context.Context, action, target string, result audit.Result, details map[string]any)
func (h *Handler) emit(r *http.Request, t notify.EventType, title, msg, username string)
func callerName(r *http.Request) string
func userTarget(id int64) string
func writeValidation(w http.ResponseWriter, r *http.Request, msg string)
func writeNotFound(w http.ResponseWriter, r *http.Request, what string)
func writeConflict(w http.ResponseWriter, r *http.Request, msg string)
```
- Test-Helfer (Paket `users_test`, von Tasks 7–11 genutzt): `t0`, `goodPassword`, `env`, `newEnv(t)`, `newEnvWithRecorder(t, mk)`, `(*env).admin()`, `userP(id, name)`, `(*env).do(method, path, body, p)`, `decode[T]`, `errCode`, `mustExec`, `countRows`, `userResp`.

Routen dieses Tasks (exakt laut OpenAPI-Tabelle): `GET /users` `rbac.Admin()`, `POST /users` `rbac.Admin().WithSudo()`, `GET /users/{id}` `rbac.Admin()`.

- [ ] **Step 1: Test-Helfer `internal/users/helpers_test.go`**

```go
package users_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
	"github.com/Bernotese/mc-panel/internal/users"
)

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// goodPassword passes password.Validate for every username used in these tests.
const goodPassword = "correct horse battery"

// env is a real database plus the users handler mounted on a real rbac.Router
// (with the DB-backed checker), so every request also exercises the route rule.
type env struct {
	t      *testing.T
	db     *sql.DB
	clk    *clock.Fake
	rec    *audit.MemoryRecorder
	notes  *notify.Recording
	mux    http.Handler
	rootID int64
}

func newEnv(t *testing.T) *env { return newEnvWithRecorder(t, nil) }

// newEnvWithRecorder uses mk to build the audit recorder; nil means e.rec (MemoryRecorder).
func newEnvWithRecorder(t *testing.T, mk func(*sql.DB, clock.Clock) audit.Recorder) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{}, notes: &notify.Recording{}}
	var rec audit.Recorder = e.rec
	if mk != nil {
		rec = mk(db, e.clk)
	}
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk)
	users.NewHandler(db, e.clk, rec, e.notes).Routes(rt)
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

// admin returns the principal of the seeded admin "root" with a valid sudo window.
func (e *env) admin() principal.Principal {
	return principal.Principal{
		UserID: e.rootID, Username: "root", Role: principal.RoleAdmin,
		Stage: principal.StageFull, SudoUntil: t0.Add(5 * time.Minute),
	}
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull}
}

// do sends a request with client IP 203.0.113.7 and the given principal (nil = anonymous).
func (e *env) do(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	ctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("203.0.113.7"))
	if p != nil {
		ctx = principal.With(ctx, *p)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[httpx.ErrorBody](t, rec).Error
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

type userResp struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	TotpEnabled  bool   `json:"totp_enabled"`
	PasskeyCount int    `json:"passkey_count"`
	CreatedAt    int64  `json:"created_at"`
	LastLoginAt  *int64 `json:"last_login_at"`
}
```

- [ ] **Step 2: Failing test `internal/users/users_test.go`**

```go
package users_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestCreateUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	rec := e.do(http.MethodPost, "/users", map[string]string{"username": "alice", "password": goodPassword, "role": "user"}, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	u := decode[userResp](t, rec)
	if u.ID == 0 || u.Username != "alice" || u.Role != "user" || u.TotpEnabled || u.PasskeyCount != 0 ||
		u.LastLoginAt != nil || u.CreatedAt != t0.UnixMilli() {
		t.Fatalf("user = %+v", u)
	}
	if !strings.Contains(rec.Body.String(), `"last_login_at":null`) {
		t.Fatalf("last_login_at must be null: %s", rec.Body)
	}
	var hash string
	var handle []byte
	if err := e.db.QueryRow(`SELECT password_hash, webauthn_handle FROM users WHERE id = ?`, u.ID).Scan(&hash, &handle); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := password.Verify(goodPassword, hash); !ok || err != nil {
		t.Fatalf("stored hash does not verify: %v", err)
	}
	if len(handle) != 32 {
		t.Fatalf("webauthn_handle has %d bytes, want 32", len(handle))
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionUserCreate}) {
		t.Fatalf("audit actions = %v", got)
	}
	en := e.rec.Entries[0]
	if en.Target != fmt.Sprintf("user:%d", u.ID) || en.Result != audit.ResultSuccess ||
		en.Details["username"] != "alice" || en.Details["role"] != "user" {
		t.Fatalf("audit entry = %+v", en)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.EventUserCreated}) {
		t.Fatalf("notifications = %v", got)
	}
	if ev := e.notes.Events[0]; ev.Username != "alice" || ev.IP != "203.0.113.7" || !ev.Time.Equal(t0) {
		t.Fatalf("event = %+v", ev)
	}
}

func TestCreateUserValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	cases := []struct {
		name               string
		username, pw, role string
	}{
		{"username too short", "ab", goodPassword, "user"},
		{"username with space", "bad name", goodPassword, "user"},
		{"username too long", strings.Repeat("a", 33), goodPassword, "user"},
		{"unknown role", "alice", goodPassword, "root"},
		{"password too short", "alice", "short", "user"},
		{"common password", "alice", "passwordpassword", "user"},
		{"password contains username", "alice", "alice-correct-horse", "user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do(http.MethodPost, "/users", map[string]string{"username": tc.username, "password": tc.pw, "role": tc.role}, &admin)
			if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
				t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
			}
		})
	}
	rec := e.do(http.MethodPost, "/users", map[string]any{"username": "alice", "password": goodPassword, "role": "user", "admin": true}, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "bad_request" {
		t.Fatalf("unknown field: code = %d, body %s", rec.Code, rec.Body)
	}
	if n := len(e.rec.Entries); n != 0 {
		t.Fatalf("rejected requests must not be audited as success, got %d entries", n)
	}
}

func TestCreateUserDuplicateIsCaseInsensitive(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	storetest.CreateUser(t, e.db, "Alice", "user")
	rec := e.do(http.MethodPost, "/users", map[string]string{"username": "alice", "password": goodPassword, "role": "user"}, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
}

func TestListAndGetUsers(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	mustExec(t, e.db, `UPDATE users SET totp_enabled = 1 WHERE id = ?`, alice)
	mustExec(t, e.db, `INSERT INTO webauthn_credentials (user_id, credential_id, name, credential_json, created_at)
		VALUES (?, x'0102', 'yubikey', '{}', 0)`, alice)
	for _, a := range []struct {
		at      time.Time
		stage   string
		success int
	}{
		{t0.Add(-3 * time.Hour), "password", 1},
		{t0.Add(-2 * time.Hour), "totp", 1},   // newest successful login
		{t0.Add(-1 * time.Hour), "sudo", 1},   // sudo is not a login
		{t0.Add(-30 * time.Minute), "totp", 0}, // failed
	} {
		mustExec(t, e.db, `INSERT INTO login_attempts (ts, ip, ip_key, username, user_id, stage, success)
			VALUES (?, '203.0.113.7', '203.0.113.7/32', 'alice', ?, ?, ?)`, a.at.UnixMilli(), alice, a.stage, a.success)
	}

	rec := e.do(http.MethodGet, "/users", nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code = %d, body %s", rec.Code, rec.Body)
	}
	list := decode[[]userResp](t, rec)
	if len(list) != 2 || list[0].Username != "alice" || list[1].Username != "root" {
		t.Fatalf("list = %+v", list)
	}
	a := list[0]
	if !a.TotpEnabled || a.PasskeyCount != 1 || a.LastLoginAt == nil || *a.LastLoginAt != t0.Add(-2*time.Hour).UnixMilli() {
		t.Fatalf("alice = %+v", a)
	}
	if list[1].LastLoginAt != nil || list[1].Role != "admin" {
		t.Fatalf("root = %+v", list[1])
	}

	rec = e.do(http.MethodGet, fmt.Sprintf("/users/%d", alice), nil, &admin)
	got := decode[userResp](t, rec)
	if rec.Code != http.StatusOK || got.ID != a.ID || got.Username != a.Username || got.TotpEnabled != a.TotpEnabled ||
		got.PasskeyCount != a.PasskeyCount || got.LastLoginAt == nil || *got.LastLoginAt != *a.LastLoginAt {
		t.Fatalf("get: code = %d, got %+v, want %+v", rec.Code, got, a)
	}
	for _, path := range []string{"/users/9999", "/users/abc", "/users/0"} {
		rec = e.do(http.MethodGet, path, nil, &admin)
		if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
			t.Errorf("%s: code = %d, body %s", path, rec.Code, rec.Body)
		}
	}
}

func TestUserRoutesEnforceRules(t *testing.T) {
	e := newEnv(t)
	bob := userP(42, "bob")
	if rec := e.do(http.MethodGet, "/users", nil, &bob); rec.Code != http.StatusForbidden {
		t.Fatalf("user on GET /users: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/users", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous on GET /users: %d", rec.Code)
	}
	noSudo := principal.Principal{UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull}
	rec := e.do(http.MethodPost, "/users", map[string]string{"username": "alice", "password": goodPassword, "role": "user"}, &noSudo)
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("admin without sudo: %d %s", rec.Code, rec.Body)
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/users/ -v`
Expected: FAIL – `no non-test Go files` bzw. `undefined: users.NewHandler`.

- [ ] **Step 4: Implementation `internal/users/handler.go`**

```go
// Package users implements user management, per-server permissions, the server stub API
// and the `panel admin create` CLI command (stream D).
package users

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

type Handler struct {
	db       *sql.DB
	q        *store.Queries
	clock    clock.Clock
	rec      audit.Recorder
	notifier notify.Notifier
}

// NewHandler expects db from store.Open (BEGIN IMMEDIATE transactions).
func NewHandler(db *sql.DB, c clock.Clock, rec audit.Recorder, n notify.Notifier) *Handler {
	return &Handler{db: db, q: store.New(db), clock: c, rec: rec, notifier: n}
}

// Routes registers all stream-D user, permission and server routes (paths relative to /api).
func (h *Handler) Routes(api *rbac.Router) {
	h.userRoutes(api)
}

// pathID parses the {id} URL parameter; false for non-numeric or non-positive values.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

// record writes an audit entry; actor and IP come from the request context. It must only be
// called after any write transaction of the handler has finished (SQLRecorder opens its own).
// The action already happened, so a failing audit write is logged, not returned to the client.
func (h *Handler) record(ctx context.Context, action, target string, result audit.Result, details map[string]any) {
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: target, Result: result, Details: details}); err != nil {
		slog.ErrorContext(ctx, "audit record failed", "action", action, "target", target, "err", err)
	}
}

// emit sends a security notification about the affected user.
func (h *Handler) emit(r *http.Request, t notify.EventType, title, msg, username string) {
	ip := ""
	if a := httpx.ClientIP(r.Context()); a.IsValid() {
		ip = a.String()
	}
	h.notifier.Notify(r.Context(), notify.Event{
		Type: t, Title: title, Message: msg, IP: ip, Username: username, Time: h.clock.Now(),
	})
}

func callerName(r *http.Request) string {
	p, _ := principal.From(r.Context())
	return p.Username
}

func userTarget(id int64) string { return "user:" + strconv.FormatInt(id, 10) }

func writeValidation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func writeNotFound(w http.ResponseWriter, r *http.Request, what string) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, what+" not found")
}

func writeConflict(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, msg)
}
```

- [ ] **Step 5: Implementation `internal/users/service.go`**

```go
package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

var (
	ErrInvalidUsername = errors.New("username must match ^[a-zA-Z0-9_.-]{3,32}$")
	ErrInvalidRole     = errors.New("role must be admin or user")
	ErrUsernameTaken   = errors.New("username already taken")
)

func validRole(role string) bool {
	return role == string(principal.RoleAdmin) || role == string(principal.RoleUser)
}

// isValidationError reports whether err is a client input error (→ 400 validation_failed).
func isValidationError(err error) bool {
	for _, target := range []error{
		ErrInvalidUsername, ErrInvalidRole,
		password.ErrTooShort, password.ErrTooLong, password.ErrCommon, password.ErrContainsUsername,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// newUser validates the input, hashes the password and inserts the user with a fresh 32-byte
// WebAuthn user handle. Shared by POST /users and `panel admin create`.
func newUser(ctx context.Context, q *store.Queries, now time.Time, username, pw, role string) (store.User, error) {
	if !usernameRe.MatchString(username) {
		return store.User{}, ErrInvalidUsername
	}
	if !validRole(role) {
		return store.User{}, ErrInvalidRole
	}
	if err := password.Validate(pw, username); err != nil {
		return store.User{}, err
	}
	if _, err := q.RbacGetUserByUsername(ctx, username); err == nil {
		return store.User{}, ErrUsernameTaken
	} else if !errors.Is(err, sql.ErrNoRows) {
		return store.User{}, err
	}
	hash, err := password.Hash(pw)
	if err != nil {
		return store.User{}, err
	}
	handle := make([]byte, 32)
	if _, err := rand.Read(handle); err != nil {
		return store.User{}, err
	}
	ms := now.UnixMilli()
	u, err := q.RbacCreateUser(ctx, store.RbacCreateUserParams{
		Username: username, PasswordHash: hash, Role: role, WebauthnHandle: handle,
		PasswordChangedAt: ms, CreatedAt: ms, UpdatedAt: ms,
	})
	if isUniqueViolation(err) {
		return store.User{}, ErrUsernameTaken
	}
	return u, err
}
```

- [ ] **Step 6: Implementation `internal/users/users.go`**

```go
package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

func (h *Handler) userRoutes(api *rbac.Router) {
	api.Get("/users", rbac.Admin(), h.handleListUsers)
	api.Post("/users", rbac.Admin().WithSudo(), h.handleCreateUser)
	api.Get("/users/{id}", rbac.Admin(), h.handleGetUser)
}

// userJSON is the OpenAPI schema User.
type userJSON struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	TotpEnabled  bool   `json:"totp_enabled"`
	PasskeyCount int64  `json:"passkey_count"`
	CreatedAt    int64  `json:"created_at"`
	LastLoginAt  *int64 `json:"last_login_at"`
}

func toUserJSON(ctx context.Context, q *store.Queries, u store.User) (userJSON, error) {
	n, err := q.RbacCountUserPasskeys(ctx, u.ID)
	if err != nil {
		return userJSON{}, err
	}
	out := userJSON{
		ID: u.ID, Username: u.Username, Role: u.Role, TotpEnabled: u.TotpEnabled != 0,
		PasskeyCount: n, CreatedAt: u.CreatedAt,
	}
	ts, err := q.RbacLastLogin(ctx, sql.NullInt64{Int64: u.ID, Valid: true})
	switch {
	case err == nil:
		out.LastLoginAt = &ts
	case !errors.Is(err, sql.ErrNoRows):
		return userJSON{}, err
	}
	return out, nil
}

func (h *Handler) handleListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := h.q.RbacListUsers(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]userJSON, 0, len(list))
	for _, u := range list {
		j, err := toUserJSON(ctx, h.q, u)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		out = append(out, j)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (h *Handler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createUserRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	u, err := newUser(ctx, h.q, h.clock.Now(), req.Username, req.Password, req.Role)
	switch {
	case errors.Is(err, ErrUsernameTaken):
		writeConflict(w, r, err.Error())
		return
	case isValidationError(err):
		writeValidation(w, r, err.Error())
		return
	case err != nil:
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionUserCreate, userTarget(u.ID), audit.ResultSuccess,
		map[string]any{"username": u.Username, "role": u.Role})
	h.emit(r, notify.EventUserCreated, "User created",
		fmt.Sprintf("%s created user %s with role %s", callerName(r), u.Username, u.Role), u.Username)
	out, err := toUserJSON(ctx, h.q, u)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleGetUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	u, err := h.q.RbacGetUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, r, "user")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := toUserJSON(ctx, h.q, u)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 7: Run** `go test ./internal/users/ -v`
Expected: PASS (`TestCreateUser`, `TestCreateUserValidation`, `TestCreateUserDuplicateIsCaseInsensitive`, `TestListAndGetUsers`, `TestUserRoutesEnforceRules`).

- [ ] **Step 8: Commit**

```bash
git add internal/users/handler.go internal/users/service.go internal/users/users.go internal/users/helpers_test.go internal/users/users_test.go
git commit -m "feat(users): add user list, create and get endpoints" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 7: Benutzer ändern, löschen, 2FA zurücksetzen – mit Letzter-Admin-Schutz

**Files:**
- Create: `internal/users/manage.go`
- Modify: `internal/users/handler.go` (Methode `Routes`)
- Test: `internal/users/manage_test.go`

**Interfaces:**
- Consumes: alles aus Task 6 (`Handler`, `pathID`, `record`, `emit`, `callerName`, `userTarget`, `writeValidation`, `writeNotFound`, `writeConflict`, `validRole`, `toUserJSON`, `ErrInvalidRole`); `store.RbacGetUser`, `RbacCountAdmins`, `RbacUpdateUserRole`, `RbacUpdateUserPassword`, `RbacRevokeUserSessions`, `RbacDeleteUser`, `RbacClearTOTP`, `RbacDeleteUserPasskeys`, `RbacDeleteUserRecoveryCodes` (Task 1); `(*store.Queries).WithTx(*sql.Tx) *store.Queries` (sqlc); `audit.NewSQLRecorder`, `audit.Verify` (Tasks 3/4, nur im Test); Test-Helfer aus Task 6.
- Produces (von Tasks 8–9 genutzt):
```go
var ErrNotFound, ErrLastAdmin error
func (h *Handler) inTx(ctx context.Context, fn func(q *store.Queries) error) error
func getUser(ctx context.Context, q *store.Queries, id int64) (store.User, error) // ErrNotFound if missing
func requireNotLastAdmin(ctx context.Context, q *store.Queries, u store.User) error
```
Routen dieses Tasks: `PATCH /users/{id}` `rbac.Admin().WithSudo()`, `DELETE /users/{id}` `rbac.Admin().WithSudo()`, `POST /users/{id}/reset-2fa` `rbac.Admin().WithSudo()`.

Entscheidung `reset-2fa` auf den letzten Admin bzw. sich selbst: erlaubt (Vertrag kennt nur `404`). Es sperrt niemanden aus, weil Admins ohne zweiten Faktor beim nächsten Login in den erzwungenen Setup-Flow (Stage `setup`, Stream A) kommen; das Passwort bleibt gültig.

- [ ] **Step 1: Failing test `internal/users/manage_test.go`**

```go
package users_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

const newPassword = "violet tractor 42 umbrella"

func insertSession(t *testing.T, db *sql.DB, userID int64, revoked sql.NullInt64) int64 {
	t.Helper()
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`INSERT INTO sessions (token_hash, user_id, stage, csrf_token, ip, user_agent,
		created_at, last_seen_at, expires_at, revoked_at)
		VALUES (?, ?, 'full', 'csrf', '203.0.113.7', 'test', 0, 0, ?, ?)`,
		tok, userID, t0.Add(time.Hour).UnixMilli(), revoked)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func sessionRevokedAt(t *testing.T, db *sql.DB, id int64) sql.NullInt64 {
	t.Helper()
	var v sql.NullInt64
	if err := db.QueryRow(`SELECT revoked_at FROM sessions WHERE id = ?`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func userRole(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var role string
	if err := db.QueryRow(`SELECT role FROM users WHERE id = ?`, id).Scan(&role); err != nil {
		t.Fatal(err)
	}
	return role
}

// Review Focus 2: the only admin cannot be demoted.
func TestDemoteLastAdminIsRejected(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", e.rootID), map[string]string{"role": "user"}, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	if role := userRole(t, e.db, e.rootID); role != "admin" {
		t.Fatalf("role changed to %q", role)
	}
	if len(e.rec.Entries) != 1 || e.rec.Entries[0].Action != audit.ActionUserUpdate || e.rec.Entries[0].Result != audit.ResultDenied {
		t.Fatalf("audit = %+v", e.rec.Entries)
	}
	if n := len(e.notes.Events); n != 0 {
		t.Fatalf("denied change must not notify, got %d", n)
	}
}

func TestChangeRole(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	second := storetest.CreateUser(t, e.db, "second", "admin")

	rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", second), map[string]string{"role": "user"}, &admin)
	if rec.Code != http.StatusOK || decode[userResp](t, rec).Role != "user" {
		t.Fatalf("demote second: %d %s", rec.Code, rec.Body)
	}
	en := e.rec.Entries[0]
	if en.Action != audit.ActionUserUpdate || en.Result != audit.ResultSuccess ||
		en.Details["role_from"] != "admin" || en.Details["role_to"] != "user" || en.Target != fmt.Sprintf("user:%d", second) {
		t.Fatalf("audit = %+v", en)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.EventPermissionsChanged}) {
		t.Fatalf("notifications = %v", got)
	}

	// Review Focus 2: root is the only admin again.
	rec = e.do(http.MethodPatch, fmt.Sprintf("/users/%d", e.rootID), map[string]string{"role": "user"}, &admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("demote last admin: %d", rec.Code)
	}

	rec = e.do(http.MethodPatch, fmt.Sprintf("/users/%d", second), map[string]string{"role": "admin"}, &admin)
	if rec.Code != http.StatusOK || userRole(t, e.db, second) != "admin" {
		t.Fatalf("promote: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(http.MethodPatch, fmt.Sprintf("/users/%d", second), map[string]string{"role": "admin"}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-op role change: %d", rec.Code)
	}
	if n := len(e.notes.Events); n != 2 {
		t.Fatalf("expected 2 role notifications (demote, promote), got %d", n)
	}
}

func TestUpdatePassword(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	sess := insertSession(t, e.db, alice, sql.NullInt64{})

	rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", alice), map[string]string{"password": newPassword}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	var hash string
	var changedAt int64
	if err := e.db.QueryRow(`SELECT password_hash, password_changed_at FROM users WHERE id = ?`, alice).Scan(&hash, &changedAt); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := password.Verify(newPassword, hash); !ok || err != nil || changedAt != t0.UnixMilli() {
		t.Fatalf("password not updated: ok=%v err=%v changedAt=%d", ok, err, changedAt)
	}
	if got := sessionRevokedAt(t, e.db, sess); got != (sql.NullInt64{Int64: t0.UnixMilli(), Valid: true}) {
		t.Fatalf("session revoked_at = %+v", got)
	}
	if e.rec.Entries[0].Details["password_changed"] != true {
		t.Fatalf("audit details = %+v", e.rec.Entries[0].Details)
	}
	if n := len(e.notes.Events); n != 0 {
		t.Fatalf("password change by admin must not send a role notification, got %d", n)
	}

	for _, body := range []map[string]string{{"password": "short"}, {}, {"role": "root"}} {
		rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", alice), body, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("body %v: code = %d, body %s", body, rec.Code, rec.Body)
		}
	}
	if rec := e.do(http.MethodPatch, "/users/9999", map[string]string{"role": "user"}, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user: %d", rec.Code)
	}
}

// Review Focus 2: an admin cannot delete their own account.
func TestDeleteSelfIsRejected(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	storetest.CreateUser(t, e.db, "second", "admin")
	rec := e.do(http.MethodDelete, fmt.Sprintf("/users/%d", e.rootID), nil, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	if n := countRows(t, e.db, `SELECT count(*) FROM users WHERE id = ?`, e.rootID); n != 1 {
		t.Fatal("root was deleted")
	}
	if e.rec.Entries[0].Result != audit.ResultDenied {
		t.Fatalf("audit = %+v", e.rec.Entries)
	}
}

// Review Focus 2: the last admin cannot be deleted, even by a principal that is not that admin.
func TestDeleteLastAdminIsRejected(t *testing.T) {
	e := newEnv(t)
	ghost := principal.Principal{UserID: 9999, Username: "ghost", Role: principal.RoleAdmin,
		Stage: principal.StageFull, SudoUntil: t0.Add(time.Minute)}
	rec := e.do(http.MethodDelete, fmt.Sprintf("/users/%d", e.rootID), nil, &ghost)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	if n := countRows(t, e.db, `SELECT count(*) FROM users WHERE id = ?`, e.rootID); n != 1 {
		t.Fatal("last admin was deleted")
	}
}

func TestDeleteUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	srv := storetest.CreateServer(t, e.db, "alpha")
	mustExec(t, e.db, `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, 'server.view')`, alice, srv)
	insertSession(t, e.db, alice, sql.NullInt64{})

	rec := e.do(http.MethodDelete, fmt.Sprintf("/users/%d", alice), nil, &admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	for _, q := range []string{
		`SELECT count(*) FROM users WHERE id = ?`,
		`SELECT count(*) FROM permissions WHERE user_id = ?`,
		`SELECT count(*) FROM sessions WHERE user_id = ?`,
	} {
		if n := countRows(t, e.db, q, alice); n != 0 {
			t.Errorf("%s → %d, want 0", q, n)
		}
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionUserDelete}) || e.rec.Entries[0].Details["username"] != "alice" {
		t.Fatalf("audit = %+v", e.rec.Entries)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.EventUserDeleted}) {
		t.Fatalf("notifications = %v", got)
	}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/users/%d", alice), nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
}

func TestReset2FA(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	mustExec(t, e.db, `UPDATE users SET totp_secret_enc = x'01', totp_enabled = 1, totp_last_step = 99 WHERE id = ?`, alice)
	mustExec(t, e.db, `INSERT INTO webauthn_credentials (user_id, credential_id, name, credential_json, created_at)
		VALUES (?, x'0102', 'key', '{}', 0)`, alice)
	mustExec(t, e.db, `INSERT INTO recovery_codes (user_id, code_hash, created_at) VALUES (?, x'aa', 0)`, alice)
	active := insertSession(t, e.db, alice, sql.NullInt64{})
	old := insertSession(t, e.db, alice, sql.NullInt64{Int64: 5, Valid: true})
	rootSession := insertSession(t, e.db, e.rootID, sql.NullInt64{})

	rec := e.do(http.MethodPost, fmt.Sprintf("/users/%d/reset-2fa", alice), nil, &admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	var secret []byte
	var enabled, step int64
	if err := e.db.QueryRow(`SELECT totp_secret_enc, totp_enabled, totp_last_step FROM users WHERE id = ?`, alice).
		Scan(&secret, &enabled, &step); err != nil {
		t.Fatal(err)
	}
	if secret != nil || enabled != 0 || step != 0 {
		t.Fatalf("totp not cleared: secret=%v enabled=%d step=%d", secret, enabled, step)
	}
	if n := countRows(t, e.db, `SELECT count(*) FROM webauthn_credentials WHERE user_id = ?`, alice); n != 0 {
		t.Fatalf("passkeys left: %d", n)
	}
	if n := countRows(t, e.db, `SELECT count(*) FROM recovery_codes WHERE user_id = ?`, alice); n != 0 {
		t.Fatalf("recovery codes left: %d", n)
	}
	if got := sessionRevokedAt(t, e.db, active); got != (sql.NullInt64{Int64: t0.UnixMilli(), Valid: true}) {
		t.Fatalf("active session revoked_at = %+v", got)
	}
	if got := sessionRevokedAt(t, e.db, old); got != (sql.NullInt64{Int64: 5, Valid: true}) {
		t.Fatalf("already revoked session changed: %+v", got)
	}
	if got := sessionRevokedAt(t, e.db, rootSession); got.Valid {
		t.Fatalf("other user's session revoked: %+v", got)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionUserReset2FA}) {
		t.Fatalf("audit = %v", got)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.Event2FAChanged}) {
		t.Fatalf("notifications = %v", got)
	}
	if rec := e.do(http.MethodPost, "/users/9999/reset-2fa", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user: %d", rec.Code)
	}
}

// Review Focus (stream): handlers must write audit entries after their own BEGIN IMMEDIATE
// transaction has committed; otherwise the SQLRecorder blocks for busy_timeout and fails.
func TestHandlersWithSQLRecorder(t *testing.T) {
	e := newEnvWithRecorder(t, func(db *sql.DB, c clock.Clock) audit.Recorder { return audit.NewSQLRecorder(db, c) })
	admin := e.admin()
	start := time.Now()

	rec := e.do(http.MethodPost, "/users", map[string]string{"username": "alice", "password": goodPassword, "role": "user"}, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	alice := decode[userResp](t, rec).ID
	if rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", alice), map[string]string{"role": "admin"}, &admin); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodPost, fmt.Sprintf("/users/%d/reset-2fa", alice), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/users/%d", alice), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("requests took %v – audit write probably waited for the handler's transaction", d)
	}
	rep, err := audit.Verify(context.Background(), e.db)
	if err != nil || !rep.OK || rep.Checked != 4 {
		t.Fatalf("audit chain = %+v, %v (want 4 valid entries)", rep, err)
	}
	var actor, ip string
	if err := e.db.QueryRow(`SELECT actor_name, ip FROM audit_log WHERE id = 4`).Scan(&actor, &ip); err != nil {
		t.Fatal(err)
	}
	if actor != "root" || ip != "203.0.113.7" {
		t.Fatalf("actor/ip = %q/%q", actor, ip)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/users/ -run 'TestDemote|TestChangeRole|TestUpdatePassword|TestDelete|TestReset2FA|TestHandlersWithSQLRecorder' -v`
Expected: FAIL – `405`/`404` statt der erwarteten Codes, weil die Routen noch nicht registriert sind (z. B. `code = 405`).

- [ ] **Step 3: Implementation `internal/users/manage.go`**

```go
package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

var (
	ErrNotFound  = errors.New("user not found")
	ErrLastAdmin = errors.New("cannot remove or demote the last admin")
)

func (h *Handler) manageRoutes(api *rbac.Router) {
	api.Patch("/users/{id}", rbac.Admin().WithSudo(), h.handleUpdateUser)
	api.Delete("/users/{id}", rbac.Admin().WithSudo(), h.handleDeleteUser)
	api.Post("/users/{id}/reset-2fa", rbac.Admin().WithSudo(), h.handleReset2FA)
}

// inTx runs fn in a write transaction. store.Open sets _txlock=immediate, so this is
// BEGIN IMMEDIATE: checks such as "is this the last admin?" cannot race.
// Never call h.record inside fn.
func (h *Handler) inTx(ctx context.Context, fn func(q *store.Queries) error) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(h.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

func getUser(ctx context.Context, q *store.Queries, id int64) (store.User, error) {
	u, err := q.RbacGetUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// requireNotLastAdmin returns ErrLastAdmin if u is an admin and no other admin exists.
func requireNotLastAdmin(ctx context.Context, q *store.Queries, u store.User) error {
	if u.Role != string(principal.RoleAdmin) {
		return nil
	}
	n, err := q.RbacCountAdmins(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

type updateUserRequest struct {
	Role     *string `json:"role"`
	Password *string `json:"password"`
}

func (h *Handler) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	var req updateUserRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.Role == nil && req.Password == nil {
		writeValidation(w, r, "nothing to update: set role and/or password")
		return
	}
	if req.Role != nil && !validRole(*req.Role) {
		writeValidation(w, r, ErrInvalidRole.Error())
		return
	}
	u, err := getUser(ctx, h.q, id)
	if errors.Is(err, ErrNotFound) {
		writeNotFound(w, r, "user")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var newHash string
	if req.Password != nil {
		if err := password.Validate(*req.Password, u.Username); err != nil {
			writeValidation(w, r, err.Error())
			return
		}
		if newHash, err = password.Hash(*req.Password); err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}

	now := h.clock.Now().UnixMilli()
	oldRole := u.Role
	err = h.inTx(ctx, func(q *store.Queries) error {
		cur, err := getUser(ctx, q, id)
		if err != nil {
			return err
		}
		oldRole = cur.Role
		if req.Role != nil && *req.Role != cur.Role {
			if err := requireNotLastAdmin(ctx, q, cur); err != nil {
				return err
			}
			if err := q.RbacUpdateUserRole(ctx, store.RbacUpdateUserRoleParams{Role: *req.Role, UpdatedAt: now, ID: id}); err != nil {
				return err
			}
		}
		if req.Password != nil {
			if err := q.RbacUpdateUserPassword(ctx, store.RbacUpdateUserPasswordParams{
				PasswordHash: newHash, PasswordChangedAt: now, UpdatedAt: now, ID: id,
			}); err != nil {
				return err
			}
			// A password set by an admin invalidates every existing session of that user.
			if err := q.RbacRevokeUserSessions(ctx, store.RbacRevokeUserSessionsParams{
				RevokedAt: sql.NullInt64{Int64: now, Valid: true}, UserID: id,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r, "user")
		return
	case errors.Is(err, ErrLastAdmin):
		h.record(ctx, audit.ActionUserUpdate, userTarget(id), audit.ResultDenied,
			map[string]any{"username": u.Username, "reason": "last_admin"})
		writeConflict(w, r, ErrLastAdmin.Error())
		return
	case err != nil:
		httpx.Internal(w, r, err)
		return
	}

	roleChanged := req.Role != nil && *req.Role != oldRole
	details := map[string]any{"username": u.Username}
	if roleChanged {
		details["role_from"] = oldRole
		details["role_to"] = *req.Role
	}
	if req.Password != nil {
		details["password_changed"] = true
	}
	h.record(ctx, audit.ActionUserUpdate, userTarget(id), audit.ResultSuccess, details)
	if roleChanged {
		h.emit(r, notify.EventPermissionsChanged, "Role changed",
			fmt.Sprintf("%s changed the role of %s from %s to %s", callerName(r), u.Username, oldRole, *req.Role), u.Username)
	}

	updated, err := getUser(ctx, h.q, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := toUserJSON(ctx, h.q, updated)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	if p, _ := principal.From(ctx); p.UserID == id {
		h.record(ctx, audit.ActionUserDelete, userTarget(id), audit.ResultDenied, map[string]any{"reason": "self"})
		writeConflict(w, r, "you cannot delete your own account")
		return
	}
	var deleted store.User
	err := h.inTx(ctx, func(q *store.Queries) error {
		u, err := getUser(ctx, q, id)
		if err != nil {
			return err
		}
		if err := requireNotLastAdmin(ctx, q, u); err != nil {
			return err
		}
		if _, err := q.RbacDeleteUser(ctx, id); err != nil {
			return err
		}
		deleted = u
		return nil
	})
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r, "user")
		return
	case errors.Is(err, ErrLastAdmin):
		h.record(ctx, audit.ActionUserDelete, userTarget(id), audit.ResultDenied, map[string]any{"reason": "last_admin"})
		writeConflict(w, r, ErrLastAdmin.Error())
		return
	case err != nil:
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionUserDelete, userTarget(id), audit.ResultSuccess,
		map[string]any{"username": deleted.Username, "role": deleted.Role})
	h.emit(r, notify.EventUserDeleted, "User deleted",
		fmt.Sprintf("%s deleted user %s", callerName(r), deleted.Username), deleted.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleReset2FA(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	now := h.clock.Now().UnixMilli()
	var u store.User
	err := h.inTx(ctx, func(q *store.Queries) error {
		var err error
		if u, err = getUser(ctx, q, id); err != nil {
			return err
		}
		if err := q.RbacClearTOTP(ctx, store.RbacClearTOTPParams{UpdatedAt: now, ID: id}); err != nil {
			return err
		}
		if err := q.RbacDeleteUserPasskeys(ctx, id); err != nil {
			return err
		}
		if err := q.RbacDeleteUserRecoveryCodes(ctx, id); err != nil {
			return err
		}
		return q.RbacRevokeUserSessions(ctx, store.RbacRevokeUserSessionsParams{
			RevokedAt: sql.NullInt64{Int64: now, Valid: true}, UserID: id,
		})
	})
	if errors.Is(err, ErrNotFound) {
		writeNotFound(w, r, "user")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionUserReset2FA, userTarget(id), audit.ResultSuccess, map[string]any{"username": u.Username})
	h.emit(r, notify.Event2FAChanged, "Two-factor authentication reset",
		fmt.Sprintf("%s reset all second factors of %s; all sessions were revoked", callerName(r), u.Username), u.Username)
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: `Routes` in `internal/users/handler.go` erweitern**

Ersetze

```go
func (h *Handler) Routes(api *rbac.Router) {
	h.userRoutes(api)
}
```

durch

```go
func (h *Handler) Routes(api *rbac.Router) {
	h.userRoutes(api)
	h.manageRoutes(api)
}
```

- [ ] **Step 5: Run** `go test ./internal/users/ -v`
Expected: PASS (Tests aus Task 6 und 7).

- [ ] **Step 6: Commit**

```bash
git add internal/users/manage.go internal/users/manage_test.go internal/users/handler.go
git commit -m "feat(users): add user update, delete and 2FA reset with last-admin protection" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 8: Rechte pro Server – `GET/PUT /users/{id}/permissions`

**Files:**
- Create: `internal/users/permissions.go`
- Modify: `internal/users/handler.go` (Methode `Routes`)
- Test: `internal/users/permissions_test.go`

**Interfaces:**
- Consumes: `Handler`, `pathID`, `record`, `emit`, `callerName`, `userTarget`, `writeValidation`, `writeNotFound` (Task 6); `inTx`, `getUser`, `ErrNotFound` (Task 7); `store.RbacListUserPermissions`, `RbacGetServer`, `RbacDeleteUserPermissions`, `RbacInsertPermission` (Task 1); `rbac.Permission`, `rbac.AllPermissions`, `rbac.PermServerView`, `(rbac.Permission).Valid()` (Phase 0); `rbac.NewDBChecker` (Task 1, im Test).
- Produces (von Task 9 genutzt):
```go
type grantJSON struct {
	ServerID    int64             `json:"server_id"`
	Permissions []rbac.Permission `json:"permissions"`
}
func orderedPerms(set map[rbac.Permission]bool) []rbac.Permission          // rbac.AllPermissions order, never nil
func loadGrants(ctx context.Context, q *store.Queries, userID int64) ([]grantJSON, error) // ascending server_id, never nil
```
Routen dieses Tasks: `GET /users/{id}/permissions` `rbac.Admin()`, `PUT /users/{id}/permissions` `rbac.Admin().WithSudo()`.

- [ ] **Step 1: Failing test `internal/users/permissions_test.go`**

```go
package users_test

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type grantResp struct {
	ServerID    int64    `json:"server_id"`
	Permissions []string `json:"permissions"`
}

type permsResp struct {
	Grants []grantResp `json:"grants"`
}

func grantsBody(grants ...map[string]any) map[string]any {
	if grants == nil {
		grants = []map[string]any{}
	}
	return map[string]any{"grants": grants}
}

func grant(serverID int64, perms ...string) map[string]any {
	if perms == nil {
		perms = []string{}
	}
	return map[string]any{"server_id": serverID, "permissions": perms}
}

func TestSetAndGetPermissions(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	a := storetest.CreateServer(t, e.db, "alpha")
	b := storetest.CreateServer(t, e.db, "beta")
	path := fmt.Sprintf("/users/%d/permissions", alice)

	rec := e.do(http.MethodPut, path, grantsBody(grant(b, "server.view"), grant(a, "server.power")), &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	// server.power implies server.view; grants sorted by server id, permissions in rbac order.
	want := permsResp{Grants: []grantResp{
		{ServerID: a, Permissions: []string{"server.view", "server.power"}},
		{ServerID: b, Permissions: []string{"server.view"}},
	}}
	if got := decode[permsResp](t, rec); !reflect.DeepEqual(got, want) {
		t.Fatalf("PUT response = %+v, want %+v", got, want)
	}
	rec = e.do(http.MethodGet, path, nil, &admin)
	if got := decode[permsResp](t, rec); rec.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("GET = %d %+v", rec.Code, got)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionPermissionsSet}) || e.rec.Entries[0].Target != fmt.Sprintf("user:%d", alice) {
		t.Fatalf("audit = %+v", e.rec.Entries)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.EventPermissionsChanged}) {
		t.Fatalf("notifications = %v", got)
	}
	ok, err := rbac.NewDBChecker(e.db).HasServerPermission(context.Background(), alice, a, rbac.PermServerView)
	if err != nil || !ok {
		t.Fatalf("implied server.view not enforceable: %v %v", ok, err)
	}

	// PUT replaces the full set.
	rec = e.do(http.MethodPut, path, grantsBody(), &admin)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"grants":[]`) {
		t.Fatalf("PUT empty: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(http.MethodGet, path, nil, &admin)
	if !strings.Contains(rec.Body.String(), `"grants":[]`) {
		t.Fatalf("GET after clear: %s", rec.Body)
	}
}

func TestSetPermissionsMergesAndDropsEmpty(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	a := storetest.CreateServer(t, e.db, "alpha")
	b := storetest.CreateServer(t, e.db, "beta")
	rec := e.do(http.MethodPut, fmt.Sprintf("/users/%d/permissions", alice),
		grantsBody(grant(a, "server.console"), grant(a, "server.power"), grant(b)), &admin)
	want := permsResp{Grants: []grantResp{{ServerID: a, Permissions: []string{"server.view", "server.power", "server.console"}}}}
	if got := decode[permsResp](t, rec); rec.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %d %+v, want %+v", rec.Code, got, want)
	}
}

// Review Focus (stream): invalid input leaves the previous permission set untouched.
func TestSetPermissionsValidationIsAtomic(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	a := storetest.CreateServer(t, e.db, "alpha")
	path := fmt.Sprintf("/users/%d/permissions", alice)
	if rec := e.do(http.MethodPut, path, grantsBody(grant(a, "server.power")), &admin); rec.Code != http.StatusOK {
		t.Fatalf("initial PUT: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]map[string]any{
		"unknown server":     grantsBody(grant(a, "server.view"), grant(9999, "server.view")),
		"unknown permission": grantsBody(grant(a, "server.root")),
		"server id zero":     grantsBody(grant(0, "server.view")),
	} {
		rec := e.do(http.MethodPut, path, body, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	want := permsResp{Grants: []grantResp{{ServerID: a, Permissions: []string{"server.view", "server.power"}}}}
	if got := decode[permsResp](t, e.do(http.MethodGet, path, nil, &admin)); !reflect.DeepEqual(got, want) {
		t.Fatalf("permissions changed by rejected PUTs: %+v", got)
	}
	if n := len(e.rec.Entries); n != 1 {
		t.Fatalf("rejected PUTs must not be audited as success: %d entries", n)
	}
}

func TestPermissionsUnknownUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	if rec := e.do(http.MethodGet, "/users/9999/permissions", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("GET: %d", rec.Code)
	}
	if rec := e.do(http.MethodPut, "/users/9999/permissions", grantsBody(), &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("PUT: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/users/ -run 'Permissions' -v`
Expected: FAIL (`code = 405` bzw. `404`, Routen fehlen).

- [ ] **Step 3: Implementation `internal/users/permissions.go`**

```go
package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// grantJSON is the OpenAPI schema PermissionGrant.
type grantJSON struct {
	ServerID    int64             `json:"server_id"`
	Permissions []rbac.Permission `json:"permissions"`
}

// userPermissionsJSON is the OpenAPI schema UserPermissions.
type userPermissionsJSON struct {
	Grants []grantJSON `json:"grants"`
}

type unknownServerError struct{ id int64 }

func (e unknownServerError) Error() string { return fmt.Sprintf("unknown server_id %d", e.id) }

func (h *Handler) permissionRoutes(api *rbac.Router) {
	api.Get("/users/{id}/permissions", rbac.Admin(), h.handleGetPermissions)
	api.Put("/users/{id}/permissions", rbac.Admin().WithSudo(), h.handleSetPermissions)
}

// orderedPerms returns the permissions in set in rbac.AllPermissions order (never nil).
func orderedPerms(set map[rbac.Permission]bool) []rbac.Permission {
	out := make([]rbac.Permission, 0, len(set))
	for _, p := range rbac.AllPermissions {
		if set[p] {
			out = append(out, p)
		}
	}
	return out
}

// loadGrants returns the stored grants of a user, ascending by server id (never nil).
func loadGrants(ctx context.Context, q *store.Queries, userID int64) ([]grantJSON, error) {
	rows, err := q.RbacListUserPermissions(ctx, userID)
	if err != nil {
		return nil, err
	}
	sets := map[int64]map[rbac.Permission]bool{}
	var ids []int64 // rows are ordered by server_id
	for _, row := range rows {
		if sets[row.ServerID] == nil {
			sets[row.ServerID] = map[rbac.Permission]bool{}
			ids = append(ids, row.ServerID)
		}
		sets[row.ServerID][rbac.Permission(row.Permission)] = true
	}
	grants := make([]grantJSON, 0, len(ids))
	for _, id := range ids {
		grants = append(grants, grantJSON{ServerID: id, Permissions: orderedPerms(sets[id])})
	}
	return grants, nil
}

// normalizeGrants validates the request, merges duplicate server entries, drops servers
// without permissions and adds the implied server.view to every remaining server.
func normalizeGrants(in []grantJSON) (map[int64]map[rbac.Permission]bool, error) {
	want := map[int64]map[rbac.Permission]bool{}
	for _, g := range in {
		if g.ServerID <= 0 {
			return nil, fmt.Errorf("invalid server_id %d", g.ServerID)
		}
		set := want[g.ServerID]
		if set == nil {
			set = map[rbac.Permission]bool{}
			want[g.ServerID] = set
		}
		for _, p := range g.Permissions {
			if !p.Valid() {
				return nil, fmt.Errorf("unknown permission %q", p)
			}
			set[p] = true
		}
	}
	for id, set := range want {
		if len(set) == 0 {
			delete(want, id)
			continue
		}
		// Any server permission is useless without seeing the server.
		set[rbac.PermServerView] = true
	}
	return want, nil
}

func (h *Handler) handleGetPermissions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	if _, err := getUser(ctx, h.q, id); errors.Is(err, ErrNotFound) {
		writeNotFound(w, r, "user")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	grants, err := loadGrants(ctx, h.q, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, userPermissionsJSON{Grants: grants})
}

func (h *Handler) handleSetPermissions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	var req userPermissionsJSON
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	want, err := normalizeGrants(req.Grants)
	if err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	var (
		u      store.User
		grants []grantJSON
	)
	// Validation of server ids and the replacement run in one transaction: all or nothing.
	err = h.inTx(ctx, func(q *store.Queries) error {
		var err error
		if u, err = getUser(ctx, q, id); err != nil {
			return err
		}
		serverIDs := slices.Sorted(maps.Keys(want))
		for _, sid := range serverIDs {
			if _, err := q.RbacGetServer(ctx, sid); errors.Is(err, sql.ErrNoRows) {
				return unknownServerError{id: sid}
			} else if err != nil {
				return err
			}
		}
		if err := q.RbacDeleteUserPermissions(ctx, id); err != nil {
			return err
		}
		for _, sid := range serverIDs {
			for _, p := range orderedPerms(want[sid]) {
				if err := q.RbacInsertPermission(ctx, store.RbacInsertPermissionParams{
					UserID: id, ServerID: sid, Permission: string(p),
				}); err != nil {
					return err
				}
			}
		}
		grants, err = loadGrants(ctx, q, id)
		return err
	})
	var use unknownServerError
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r, "user")
		return
	case errors.As(err, &use):
		writeValidation(w, r, use.Error())
		return
	case err != nil:
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionPermissionsSet, userTarget(id), audit.ResultSuccess,
		map[string]any{"username": u.Username, "grants": grants})
	h.emit(r, notify.EventPermissionsChanged, "Permissions changed",
		fmt.Sprintf("%s changed the server permissions of %s", callerName(r), u.Username), u.Username)
	httpx.WriteJSON(w, http.StatusOK, userPermissionsJSON{Grants: grants})
}
```

- [ ] **Step 4: `Routes` in `internal/users/handler.go` erweitern**

Ersetze

```go
func (h *Handler) Routes(api *rbac.Router) {
	h.userRoutes(api)
	h.manageRoutes(api)
}
```

durch

```go
func (h *Handler) Routes(api *rbac.Router) {
	h.userRoutes(api)
	h.manageRoutes(api)
	h.permissionRoutes(api)
}
```

- [ ] **Step 5: Run** `go test ./internal/users/ -v`
Expected: PASS (Tasks 6–8).

- [ ] **Step 6: Commit**

```bash
git add internal/users/permissions.go internal/users/permissions_test.go internal/users/handler.go
git commit -m "feat(users): add per-server permission endpoints" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 9: Server-Stub – `/servers`, `/servers/{id}`, `/servers/{id}/power`

**Files:**
- Create: `internal/users/servers.go`
- Modify: `internal/users/handler.go` (Methode `Routes`)
- Test: `internal/users/servers_test.go`

**Interfaces:**
- Consumes: `Handler`, `pathID`, `record`, `writeValidation`, `writeNotFound`, `writeConflict`, `isUniqueViolation` (Task 6); `orderedPerms`, `loadGrants` (Task 8); `store.RbacListServers`, `RbacListViewableServers`, `RbacGetServer`, `RbacGetServerByName`, `RbacCreateServer`, `RbacDeleteServer`, `RbacListUserServerPermissions` (Task 1); `principal.From`; `rbac.Authenticated()`, `rbac.Admin()`, `rbac.Server(...)`, `rbac.AllPermissions`; Test-Helfer `grantsBody`, `grant` (Task 8).
- Produces: `serverJSON{id, name, permissions}` (OpenAPI `Server`), `func serverTarget(id int64) string`.

Routen dieses Tasks (exakt laut OpenAPI-Tabelle): `GET /servers` `rbac.Authenticated()`, `POST /servers` `rbac.Admin().WithSudo()`, `GET /servers/{id}` `rbac.Server(rbac.PermServerView, "id")`, `DELETE /servers/{id}` `rbac.Admin().WithSudo()`, `POST /servers/{id}/power` `rbac.Server(rbac.PermServerPower, "id")`.

Sichtbarkeit: Admins sehen alle Server mit allen Rechten; Benutzer sehen nur Server, auf denen sie `server.view` haben, jeweils mit ihren effektiven Rechten. Nicht-Admins erhalten für fremde oder nicht existierende Server `403` (vom Router; verrät keine Existenz), Admins für nicht existierende Server `404`.

- [ ] **Step 1: Failing test `internal/users/servers_test.go`**

```go
package users_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type serverResp struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

var allPermStrings = []string{"server.view", "server.power", "server.console", "server.files", "server.plugins", "server.backups"}

func TestServerCRUDAsAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	rec := e.do(http.MethodPost, "/servers", map[string]string{"name": "survival"}, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	s := decode[serverResp](t, rec)
	if s.ID == 0 || s.Name != "survival" || !slices.Equal(s.Permissions, allPermStrings) {
		t.Fatalf("created = %+v", s)
	}
	if rec := e.do(http.MethodPost, "/servers", map[string]string{"name": "survival"}, &admin); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d", rec.Code)
	}
	for _, bad := range []string{"-bad", "a", "Upper", "has_underscore", "with space", strings.Repeat("a", 33)} {
		rec := e.do(http.MethodPost, "/servers", map[string]string{"name": bad}, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("name %q: %d %s", bad, rec.Code, rec.Body)
		}
	}
	list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &admin))
	if len(list) != 1 || list[0].ID != s.ID || !slices.Equal(list[0].Permissions, allPermStrings) {
		t.Fatalf("list = %+v", list)
	}
	path := fmt.Sprintf("/servers/%d", s.ID)
	if rec := e.do(http.MethodGet, path, nil, &admin); rec.Code != http.StatusOK || decode[serverResp](t, rec).Name != "survival" {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, path, nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted: %d", rec.Code)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionServerCreate, audit.ActionServerDelete}) {
		t.Fatalf("audit = %v", got)
	}
	if en := e.rec.Entries[0]; en.Target != fmt.Sprintf("server:%d", s.ID) || en.Details["name"] != "survival" {
		t.Fatalf("audit entry = %+v", en)
	}
}

// Spec §12.4: a restricted user with server.power on server A sees only A; B → 403.
func TestServerVisibilityForRestrictedUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	a := storetest.CreateServer(t, e.db, "alpha")
	b := storetest.CreateServer(t, e.db, "beta")
	if rec := e.do(http.MethodPut, fmt.Sprintf("/users/%d/permissions", alice), grantsBody(grant(a, "server.power")), &admin); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}
	ap := userP(alice, "alice")

	list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &ap))
	want := []string{"server.view", "server.power"}
	if len(list) != 1 || list[0].ID != a || list[0].Name != "alpha" || !slices.Equal(list[0].Permissions, want) {
		t.Fatalf("list = %+v", list)
	}
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", a), nil, &ap)
	if rec.Code != http.StatusOK || !slices.Equal(decode[serverResp](t, rec).Permissions, want) {
		t.Fatalf("get A: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, fmt.Sprintf("/servers/%d", b), nil},
		{http.MethodPost, fmt.Sprintf("/servers/%d/power", b), map[string]string{"action": "start"}},
		{http.MethodPost, "/servers", map[string]string{"name": "gamma"}},
		{http.MethodDelete, fmt.Sprintf("/servers/%d", a), nil},
	} {
		if rec := e.do(tc.method, tc.path, tc.body, &ap); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d, want 403", tc.method, tc.path, rec.Code)
		}
	}

	rec = e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "start"}, &ap)
	if rec.Code != http.StatusAccepted || strings.TrimSpace(rec.Body.String()) != `{"status":"accepted"}` {
		t.Fatalf("power A: %d %s", rec.Code, rec.Body)
	}
	last := e.rec.Entries[len(e.rec.Entries)-1]
	if last.Action != audit.ActionServerPower || last.Target != fmt.Sprintf("server:%d", a) ||
		last.Details["action"] != "start" || last.Result != audit.ResultSuccess {
		t.Fatalf("power audit = %+v", last)
	}
	rec = e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "explode"}, &ap)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("invalid action: %d %s", rec.Code, rec.Body)
	}
}

func TestServerListWithoutPermissionsIsEmptyArray(t *testing.T) {
	e := newEnv(t)
	storetest.CreateServer(t, e.db, "alpha")
	bob := userP(storetest.CreateUser(t, e.db, "bob", "user"), "bob")
	rec := e.do(http.MethodGet, "/servers", nil, &bob)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("list: %d %q", rec.Code, rec.Body)
	}
}

func TestServerPowerUnknownServerAsAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	if rec := e.do(http.MethodPost, "/servers/9999/power", map[string]string{"action": "stop"}, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("power: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/servers/9999", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("get: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/users/ -run 'TestServer' -v`
Expected: FAIL (`404`/`405` – Routen fehlen).

- [ ] **Step 3: Implementation `internal/users/servers.go`**

```go
package users

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

var serverNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

var powerActions = map[string]bool{"start": true, "stop": true, "restart": true}

// serverJSON is the OpenAPI schema Server; permissions are the caller's effective permissions.
type serverJSON struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Permissions []rbac.Permission `json:"permissions"`
}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

func allPerms() []rbac.Permission { return slices.Clone(rbac.AllPermissions) }

func (h *Handler) serverRoutes(api *rbac.Router) {
	api.Get("/servers", rbac.Authenticated(), h.handleListServers)
	api.Post("/servers", rbac.Admin().WithSudo(), h.handleCreateServer)
	api.Get("/servers/{id}", rbac.Server(rbac.PermServerView, "id"), h.handleGetServer)
	api.Delete("/servers/{id}", rbac.Admin().WithSudo(), h.handleDeleteServer)
	api.Post("/servers/{id}/power", rbac.Server(rbac.PermServerPower, "id"), h.handleServerPower)
}

func (h *Handler) handleListServers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := principal.From(ctx)
	out := []serverJSON{}
	if p.IsAdmin() {
		list, err := h.q.RbacListServers(ctx)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		for _, s := range list {
			out = append(out, serverJSON{ID: s.ID, Name: s.Name, Permissions: allPerms()})
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	list, err := h.q.RbacListViewableServers(ctx, p.UserID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	grants, err := loadGrants(ctx, h.q, p.UserID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	perms := make(map[int64][]rbac.Permission, len(grants))
	for _, g := range grants {
		perms[g.ServerID] = g.Permissions
	}
	for _, s := range list {
		out = append(out, serverJSON{ID: s.ID, Name: s.Name, Permissions: perms[s.ID]})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// loadServer resolves {id}; it writes 404 itself and returns false if the server is missing.
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request) (store.Server, bool) {
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "server")
		return store.Server{}, false
	}
	s, err := h.q.RbacGetServer(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, r, "server")
		return store.Server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Server{}, false
	}
	return s, true
}

func (h *Handler) handleGetServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	perms := allPerms()
	if p, _ := principal.From(ctx); !p.IsAdmin() {
		rows, err := h.q.RbacListUserServerPermissions(ctx, store.RbacListUserServerPermissionsParams{UserID: p.UserID, ServerID: s.ID})
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		set := make(map[rbac.Permission]bool, len(rows))
		for _, row := range rows {
			set[rbac.Permission(row)] = true
		}
		perms = orderedPerms(set)
	}
	httpx.WriteJSON(w, http.StatusOK, serverJSON{ID: s.ID, Name: s.Name, Permissions: perms})
}

type createServerRequest struct {
	Name string `json:"name"`
}

func (h *Handler) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createServerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if !serverNameRe.MatchString(req.Name) {
		writeValidation(w, r, "name must match ^[a-z0-9][a-z0-9-]{1,31}$")
		return
	}
	if _, err := h.q.RbacGetServerByName(ctx, req.Name); err == nil {
		writeConflict(w, r, "server name already taken")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return
	}
	s, err := h.q.RbacCreateServer(ctx, store.RbacCreateServerParams{Name: req.Name, CreatedAt: h.clock.Now().UnixMilli()})
	if isUniqueViolation(err) {
		writeConflict(w, r, "server name already taken")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionServerCreate, serverTarget(s.ID), audit.ResultSuccess, map[string]any{"name": s.Name})
	httpx.WriteJSON(w, http.StatusCreated, serverJSON{ID: s.ID, Name: s.Name, Permissions: allPerms()})
}

func (h *Handler) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	if _, err := h.q.RbacDeleteServer(ctx, s.ID); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionServerDelete, serverTarget(s.ID), audit.ResultSuccess, map[string]any{"name": s.Name})
	w.WriteHeader(http.StatusNoContent)
}

type powerRequest struct {
	Action string `json:"action"`
}

// handleServerPower is a stub in sub-project 1: it validates, audits and accepts.
func (h *Handler) handleServerPower(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req powerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if !powerActions[req.Action] {
		writeValidation(w, r, "action must be start, stop or restart")
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	h.record(ctx, audit.ActionServerPower, serverTarget(s.ID), audit.ResultSuccess,
		map[string]any{"action": req.Action, "name": s.Name})
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}
```

- [ ] **Step 4: `Routes` in `internal/users/handler.go` erweitern**

Ersetze

```go
func (h *Handler) Routes(api *rbac.Router) {
	h.userRoutes(api)
	h.manageRoutes(api)
	h.permissionRoutes(api)
}
```

durch

```go
func (h *Handler) Routes(api *rbac.Router) {
	h.userRoutes(api)
	h.manageRoutes(api)
	h.permissionRoutes(api)
	h.serverRoutes(api)
}
```

- [ ] **Step 5: Run** `go test ./internal/users/ -v`
Expected: PASS (Tasks 6–9).

- [ ] **Step 6: Commit**

```bash
git add internal/users/servers.go internal/users/servers_test.go internal/users/handler.go
git commit -m "feat(users): add server stub endpoints" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 10: CLI `panel admin create` – `RunAdminCreate`

**Files:**
- Create: `internal/users/cli.go`
- Test: `internal/users/cli_test.go`

**Interfaces:**
- Consumes: `newUser`, `userTarget`, `ErrUsernameTaken`, `ErrInvalidUsername` (Task 6); `audit.Recorder`, `audit.Entry`, `audit.ActorCLI`, `audit.ActionUserCreate`; `password.ErrTooShort`, `password.Verify`; Test-Helfer `t0`, `goodPassword` (Task 6).
- Produces:
```go
// Flags: -username <name> (required). Password: first line of stdin (trailing \r\n removed).
func RunAdminCreate(ctx context.Context, db *sql.DB, c clock.Clock, rec audit.Recorder,
	args []string, stdin io.Reader, stdout io.Writer) error
```

- [ ] **Step 1: Failing test `internal/users/cli_test.go`**

```go
package users_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
	"github.com/Bernotese/mc-panel/internal/users"
)

func TestRunAdminCreate(t *testing.T) {
	db := storetest.Open(t)
	rec := &audit.MemoryRecorder{}
	var out bytes.Buffer
	err := users.RunAdminCreate(context.Background(), db, clock.NewFake(t0), rec,
		[]string{"-username", "jannes"}, strings.NewReader(goodPassword+"\r\n"), &out)
	if err != nil {
		t.Fatalf("RunAdminCreate: %v", err)
	}
	var id int64
	var role, hash string
	var handle []byte
	if err := db.QueryRow(`SELECT id, role, password_hash, webauthn_handle FROM users WHERE username = 'jannes'`).
		Scan(&id, &role, &hash, &handle); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := password.Verify(goodPassword, hash); role != "admin" || !ok || err != nil || len(handle) != 32 {
		t.Fatalf("role=%q verify=%v err=%v handle=%d", role, ok, err, len(handle))
	}
	if !strings.Contains(out.String(), `created admin user "jannes"`) {
		t.Fatalf("stdout = %q", out.String())
	}
	if len(rec.Entries) != 1 {
		t.Fatalf("audit entries = %d", len(rec.Entries))
	}
	en := rec.Entries[0]
	if en.Action != audit.ActionUserCreate || en.ActorType != audit.ActorCLI || en.ActorName != "cli" ||
		en.Result != audit.ResultSuccess || en.Target != fmt.Sprintf("user:%d", id) || en.Details["role"] != "admin" {
		t.Fatalf("audit entry = %+v", en)
	}
}

func TestRunAdminCreateErrors(t *testing.T) {
	db := storetest.Open(t)
	storetest.CreateUser(t, db, "taken", "admin")
	cases := []struct {
		name   string
		args   []string
		stdin  string
		target error  // checked with errors.Is when non-nil
		substr string // checked in err.Error() when non-empty
	}{
		{"missing username", nil, goodPassword + "\n", nil, "-username"},
		{"unknown flag", []string{"-bogus"}, goodPassword + "\n", nil, "bogus"},
		{"extra argument", []string{"-username", "jannes", "extra"}, goodPassword + "\n", nil, "unexpected arguments"},
		{"empty stdin", []string{"-username", "jannes"}, "", nil, "empty password"},
		{"weak password", []string{"-username", "jannes"}, "short\n", password.ErrTooShort, ""},
		{"duplicate username", []string{"-username", "Taken"}, goodPassword + "\n", users.ErrUsernameTaken, ""},
		{"invalid username", []string{"-username", "x y"}, goodPassword + "\n", users.ErrInvalidUsername, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &audit.MemoryRecorder{}
			err := users.RunAdminCreate(context.Background(), db, clock.NewFake(t0), rec, tc.args, strings.NewReader(tc.stdin), &bytes.Buffer{})
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.target != nil && !errors.Is(err, tc.target) {
				t.Fatalf("err = %v, want %v", err, tc.target)
			}
			if tc.substr != "" && !strings.Contains(err.Error(), tc.substr) {
				t.Fatalf("err = %v, want substring %q", err, tc.substr)
			}
			if len(rec.Entries) != 0 {
				t.Fatalf("failed run must not audit, got %+v", rec.Entries)
			}
		})
	}
	if n := countRows(t, db, `SELECT count(*) FROM users WHERE username = 'jannes'`); n != 0 {
		t.Fatalf("user created despite errors")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/users/ -run TestRunAdminCreate -v`
Expected: FAIL – `undefined: users.RunAdminCreate`.

- [ ] **Step 3: Implementation `internal/users/cli.go`**

```go
package users

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// RunAdminCreate implements `panel admin create -username <name>`. The password is the first
// line of stdin; prompting without echo is the caller's (cmd/panel) job. The new user gets the
// admin role and must set up 2FA at first login (enforced by stream A). db must be migrated.
func RunAdminCreate(ctx context.Context, db *sql.DB, c clock.Clock, rec audit.Recorder,
	args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	username := fs.String("username", "", "username of the new admin")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("admin create: %w", err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("admin create: unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *username == "" {
		return errors.New("admin create: -username is required")
	}
	pw, err := readPasswordLine(stdin)
	if err != nil {
		return fmt.Errorf("admin create: %w", err)
	}
	u, err := newUser(ctx, store.New(db), c.Now(), *username, pw, string(principal.RoleAdmin))
	if err != nil {
		return fmt.Errorf("admin create: %w", err)
	}
	if err := rec.Record(ctx, audit.Entry{
		Action: audit.ActionUserCreate, Target: userTarget(u.ID), Result: audit.ResultSuccess,
		ActorType: audit.ActorCLI, ActorName: string(audit.ActorCLI),
		Details: map[string]any{"username": u.Username, "role": u.Role},
	}); err != nil {
		return fmt.Errorf("admin create: user %q created, but writing the audit entry failed: %w", u.Username, err)
	}
	fmt.Fprintf(stdout, "created admin user %q (id %d); two-factor setup is required at first login\n", u.Username, u.ID)
	return nil
}

// readPasswordLine returns the first line of r without the trailing line terminator.
// Spaces are significant and kept.
func readPasswordLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("empty password on stdin")
	}
	return line, nil
}
```

- [ ] **Step 4: Run** `go test ./internal/users/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/users/cli.go internal/users/cli_test.go
git commit -m "feat(users): add admin create CLI command" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Routen-Vertrag und Coverage aller 15 Stream-D-Endpunkte

**Files:**
- Test: `internal/users/routes_test.go`

**Interfaces:**
- Consumes: `users.NewHandler`, `(*users.Handler).Routes` (Tasks 6–9); `audit.NewHandler`, `(*audit.Handler).Routes` (Task 5); `rbac.NewDBChecker`, `rbac.Uncovered` (Tasks 1–2); `(*rbac.Router).Route`, `Routes()`, `Rule.String()` (Phase 0); Test-Helfer `t0` (Task 6).
- Produces: nur Test (pinnt die OpenAPI-Regeln; schlägt fehl, wenn eine Route fehlt, eine Regel abweicht oder eine Route an `rbac.Router` vorbei registriert wird).

Der Test mountet wie Phase 2 unter `/api` (im Test ist `Route()` erlaubt – das Verbot gilt für Produktivcode der Streams).

- [ ] **Step 1: Test `internal/users/routes_test.go`**

```go
package users_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
	"github.com/Bernotese/mc-panel/internal/users"
)

func TestRouteContract(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		users.NewHandler(db, clk, &audit.MemoryRecorder{}, notify.Nop{}).Routes(api)
		audit.NewHandler(db, clk, notify.Nop{}).Routes(api)
	})
	mux.Handle("/*", http.NotFoundHandler())

	// Exactly the rules from the OpenAPI table in the Phase-0 plan (rows marked "D").
	want := map[string]rbac.Rule{
		"GET /api/users":                  rbac.Admin(),
		"POST /api/users":                 rbac.Admin().WithSudo(),
		"GET /api/users/{id}":             rbac.Admin(),
		"PATCH /api/users/{id}":           rbac.Admin().WithSudo(),
		"DELETE /api/users/{id}":          rbac.Admin().WithSudo(),
		"POST /api/users/{id}/reset-2fa":  rbac.Admin().WithSudo(),
		"GET /api/users/{id}/permissions": rbac.Admin(),
		"PUT /api/users/{id}/permissions": rbac.Admin().WithSudo(),
		"GET /api/servers":                rbac.Authenticated(),
		"POST /api/servers":               rbac.Admin().WithSudo(),
		"GET /api/servers/{id}":           rbac.Server(rbac.PermServerView, "id"),
		"DELETE /api/servers/{id}":        rbac.Admin().WithSudo(),
		"POST /api/servers/{id}/power":    rbac.Server(rbac.PermServerPower, "id"),
		"GET /api/audit":                  rbac.Admin(),
		"GET /api/audit/verify":           rbac.Admin(),
	}
	got := rt.Routes()
	if len(got) != len(want) {
		t.Errorf("registered %d routes, want %d", len(got), len(want))
	}
	seen := map[string]bool{}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		seen[key] = true
		w, ok := want[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
	for key := range want {
		if !seen[key] {
			t.Errorf("missing route %s", key)
		}
	}
	if un := rbac.Uncovered(mux, got, func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}

	// Path parameters must still resolve below the /api prefix.
	rootID := storetest.CreateUser(t, db, "root", "admin")
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/users/%d", rootID), nil)
	req = req.WithContext(principal.With(req.Context(), principal.Principal{
		UserID: rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SudoUntil: t0.Add(time.Minute),
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/users/{id}: %d %s", rec.Code, rec.Body)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/users/ -run TestRouteContract -v`
Expected: PASS (alle 15 Routen mit exakter Regel, `Uncovered` leer, `200` für `/api/users/{id}`). Schlägt der Test fehl, ist der Produktivcode falsch, nicht der Test – die Regeln stammen aus dem verbindlichen OpenAPI-Vertrag.

- [ ] **Step 3: Gesamtlauf**

```bash
go vet ./...
go test ./internal/rbac/ ./internal/audit/ ./internal/users/ ./internal/store/...
CGO_ENABLED=1 go test -race ./internal/audit/ ./internal/users/
golangci-lint run ./internal/rbac/... ./internal/audit/... ./internal/users/...
```
Expected: `go vet` ohne Ausgabe; alle Pakete `ok`; Race-Detector ohne `DATA RACE`; `golangci-lint` ohne Befunde (vor dem Merge zusätzlich `make test` und `make lint` laut Übersicht).

- [ ] **Step 4: Commit**

```bash
git add internal/users/routes_test.go
git commit -m "test(users): pin stream D route rules and rbac coverage" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
## Self-Review

**1. Spec-Abdeckung**

| Anforderung | Task |
|---|---|
| §5 Rollen `admin`/`user`, Deny-by-default, Rechte pro Server (`server.view`, `server.power`, …) | 1 (`DBChecker`), 8 (PUT/GET Rechte), 9 (Sichtbarkeit) |
| §5 jede Route deklariert ihr Recht; Test „keine Route ohne Rechte-Deklaration“ | 2 (`Uncovered`), 11 (`TestRouteContract`) |
| §5 Tabelle `servers` als Stub | 9 |
| §4.2/Übersicht RF 2: letzter Admin nicht löschbar/herabstufbar | 7 |
| §4.5 Sudo für Benutzer anlegen/löschen, Rollen/Rechte ändern | Regeln `Admin().WithSudo()` in 6–9, gepinnt in 11; `sudo_required` getestet in 6 |
| §4.1 Passwort-Policy und argon2id beim Anlegen/Ändern | 6 (`newUser`), 7 (PATCH), 10 (CLI) |
| §6 Audit append-only, Hash-Kette, serialisiertes Schreiben in Transaktion | 3 |
| §6 `panel audit verify` meldet ersten fehlerhaften Eintrag | 4 |
| §6 kein Update/Delete über API | 5 (nur GET-Routen) |
| §7.4 Audit-Log mit Filtern (Akteur, Aktion, Zeitraum) und Kettenstatus | 5 |
| §7.5 Events Benutzer angelegt/gelöscht, Rolle/Rechte geändert, 2FA geändert, Audit-Kette ungültig | 6, 7, 8, 5 |
| §12.3 `panel admin create` | 10 (+ Wiring-Vertrag) |
| §12.4 eingeschränkter Benutzer sieht nur Server A, B → 403 | 9 (`TestServerVisibilityForRestrictedUser`) |
| §12.6 Manipulation → `verify` schlägt fehl | 4 (`TestVerifyDetectsModifiedRow`, `…RecomputedRow`, `…DeletedRow`), 5 (Endpoint) |
| Übersicht RF 5: 50 parallele `Record` | 3 (`TestRecordConcurrent`) |

Nicht in diesem Stream (bewusst): Auth/Sessions/2FA-Flows (A), Abwehr/Notifier-Versand (B), Frontend (E), Verdrahtung in `cmd/panel` (Phase 2).

**2. Platzhalter-Scan:** Kein „TBD“, „TODO“, „analog zu Task N“. Jeder Code-Schritt enthält vollständigen Code; jeder Lauf hat Befehl und erwartetes Ergebnis. Die einzigen bedingten Hinweise betreffen sqlc-Typinferenz (Task 1 Step 2, Task 3 Step 2) und nennen die exakte Anpassung.

**3. Typ-Konsistenz:** `newUser(ctx, q, now, username, pw, role string)` wird in Task 6 (Handler) und Task 10 (CLI) identisch aufgerufen. `getUser`/`ErrNotFound`/`inTx` (Task 7) werden in Task 8 genutzt; `orderedPerms`/`loadGrants`/`grantJSON` (Task 8) in Task 9. `userTarget` (Task 6) in Tasks 7, 8, 10; `serverTarget` nur in Task 9. `Routes` wird in Tasks 7–9 jeweils um genau eine Zeile erweitert; der Endstand ruft `userRoutes`, `manageRoutes`, `permissionRoutes`, `serverRoutes`. Test-Helfer: `t0`/`userCtx`/`readAll`/`assertChain` (audit, Task 3), `seed`/`insertChain`/`mustExec`/`verify` (audit, Task 4) – keine Doppeldeklaration im Paket `audit_test`; `t0`/`goodPassword`/`env`/`mustExec`/`countRows`/`decode`/`errCode`/`userResp` (users, Task 6), `insertSession`/`sessionRevokedAt`/`userRole`/`newPassword` (Task 7), `grant`/`grantsBody`/`grantResp`/`permsResp` (Task 8), `serverResp`/`allPermStrings` (Task 9) – keine Kollisionen im Paket `users_test`. Im Paket `rbac_test` werden die Phase-0-Namen `fakeChecker`, `now`, `setup`, `do` nicht neu deklariert (`fakeChecker` wird in Task 2 wiederverwendet).

**4. Review Focus:** Fünf Einträge, jeder mit Test im besitzenden Task (Tasks 3, 7, 8). Weitere geprüfte Eingabeklassen mit Tests: GLOB-Metazeichen im Filter (Task 5, `?action=user*`), abgebrochener Request-Kontext beim Audit (Task 3), Groß-/Kleinschreibung bei Benutzernamen (Task 6), Tail-Truncation der Kette (dokumentiert als nicht erkennbar ohne externen Anker).

**Bewusste Entscheidungen / Abweichungen:**
- `server.view` wird beim Schreiben impliziert (nicht im Checker), damit gespeicherte Daten und Prüfung identisch sind.
- `POST /users/{id}/reset-2fa` ist auch für den letzten Admin erlaubt (Vertrag kennt nur `404`; kein Aussperren wegen erzwungenem Setup-Flow).
- Abgelehnte Letzter-Admin-/Selbstlösch-Versuche werden mit `result=denied` auditiert; andere Validierungsfehler nicht.
- Admin-gesetztes Passwort widerruft alle Sessions des Benutzers.
- Audit-IDs werden explizit vergeben und gehen in den Hash ein; dadurch erkennt `Verify` gelöschte Zeilen in der Mitte.
- `GET /users` macht pro Benutzer zwei kleine Zusatzabfragen (Passkeys, Last-Login) – bei wenigen Benutzern unkritisch, vermeidet unsichere sqlc-Inferenz für Aggregat-Subqueries.

## Wiring-Vertrag

Phase 2 verdrahtet in `cmd/panel` (Middleware-Reihenfolge laut Phase-0-Plan). Exakte Signaturen:

```go
// internal/rbac
func NewDBChecker(db store.DBTX) *rbac.DBChecker                       // ersetzt noChecker aus Phase 0
func Uncovered(mux chi.Routes, registered []rbac.RouteInfo, ignore func(method, pattern string) bool) []string

// internal/audit
func NewSQLRecorder(db *sql.DB, c clock.Clock) *audit.SQLRecorder        // implementiert audit.Recorder
func NewHandler(db *sql.DB, c clock.Clock, n notify.Notifier) *audit.Handler
func (h *audit.Handler) Routes(api *rbac.Router)                         // GET /audit, GET /audit/verify
func Verify(ctx context.Context, db *sql.DB) (audit.VerifyReport, error)
func RunAuditVerify(ctx context.Context, db *sql.DB, stdout io.Writer) error
var ErrChainBroken error

// internal/users
func NewHandler(db *sql.DB, c clock.Clock, rec audit.Recorder, n notify.Notifier) *users.Handler
func (h *users.Handler) Routes(api *rbac.Router)                         // 13 Routen: /users…, /servers…
func RunAdminCreate(ctx context.Context, db *sql.DB, c clock.Clock, rec audit.Recorder,
	args []string, stdin io.Reader, stdout io.Writer) error
```

Verdrahtung `serve` (Ausschnitt):

```go
db, err := store.Open(cfg.DBPath())          // Pflicht: store.Open (setzt _txlock=immediate)
// ... store.Migrate(ctx, db)
clk := clock.Real{}
auditRec := audit.NewSQLRecorder(db, clk)   // genau EINE Instanz pro Prozess; auch an Stream A und B übergeben
mux := chi.NewRouter()
// ... Middleware 1–7 laut Phase-0-Plan
rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
usersH := users.NewHandler(db, clk, auditRec, notifier)
auditH := audit.NewHandler(db, clk, notifier)
rt.Route("/api", func(api *rbac.Router) {
	// health (Phase 0), authH.Routes(api), defenseH.Routes(api) ...
	usersH.Routes(api)
	auditH.Routes(api)
})
mux.Handle("/*", webui.Handler())
```

Route-Coverage-Test in Phase 2 (z. B. `cmd/panel/routes_test.go`, gegen den vollständig verdrahteten Router):

```go
if un := rbac.Uncovered(mux, rt.Routes(), func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
	t.Fatalf("routes registered without rbac rule: %v", un)
}
```

CLI-Subcommands (Flag-Parsing der Subcommands liegt in den Funktionen; `main.go` wählt nur den Befehl, öffnet und migriert die DB):

```go
// panel admin create -username <name>
//   main.go: ist stdin ein Terminal, "Password: " auf stderr ausgeben und Echo über
//   golang.org/x/sys/unix (IoctlGetTermios/IoctlSetTermios, ECHO-Flag) abschalten;
//   sonst stdin unverändert durchreichen (Pipe). Danach:
err := users.RunAdminCreate(ctx, db, clock.Real{}, audit.NewSQLRecorder(db, clock.Real{}), args, passwordReader, os.Stdout)
// err != nil → Meldung auf stderr, Exit-Code 1

// panel audit verify
err := audit.RunAuditVerify(ctx, db, os.Stdout)
// nil → Exit 0; errors.Is(err, audit.ErrChainBroken) → Exit 1 (Meldung steht bereits auf stdout);
// anderer Fehler → Meldung auf stderr, Exit 1
```

Hinweise für Phase 2:
- Notifier: Stream-B-Dispatcher (`notify.Notifier`); in Tests `notify.Nop{}` oder `&notify.Recording{}`.
- Stream D registriert 15 Routen (13 über `usersH`, 2 über `auditH`); Abweichungen meldet `internal/users/routes_test.go`.
- Ein Stream-A-Handler darf `auditRec.Record` nicht innerhalb einer eigenen offenen Schreibtransaktion aufrufen (siehe Review Focus 4).
