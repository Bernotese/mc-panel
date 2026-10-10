# Stream A – Auth & Sessions – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das Paket `internal/auth` liefert Login (Passwort → Pre-Auth/Setup/Full), TOTP, WebAuthn/Passkeys, Recovery-Codes, serverseitige Sessions mit Rotation, CSRF-Schutz, Sudo-Modus und alle 25 `A`-Endpunkte aus dem OpenAPI-Vertrag.

**Architecture:** `Sessions` (DB-gestützte Sessions + Cookie + Session-Loader-Middleware) und `CSRF` sind globale Middleware; `Handler` bündelt alle Endpunkte und registriert sie über `Routes(api *rbac.Router)` mit genau den Regeln aus der Phase-0-Tabelle. Alle DB-Zugriffe laufen über sqlc-Queries mit Präfix `Auth` aus `internal/store/queries/auth.sql`. Abwehr (Rate-Limit, Jail, Admin-Netz) wird ausschließlich über das Phase-0-Interface `defense.LoginGuard` angesprochen, Audit über `audit.Recorder`, Benachrichtigungen über `notify.Notifier`.

**Tech Stack:** Go, chi v5 (nur `chi.URLParam`), sqlc/SQLite, `github.com/pquerna/otp` v1.5.0 (`totp.Generate`, `totp.GenerateCodeCustom`), `github.com/go-webauthn/webauthn` v0.18.x (`webauthn.New`, `BeginRegistration`, `CreateCredential`, `BeginLogin`, `ValidateLogin`, `protocol.ParseCredentialCreationResponseBytes`, `protocol.ParseCredentialRequestResponseBytes`), `internal/password`, `internal/secretbox`.

**Spec:** `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md` (§4, §7.5, §9, §10)

## Global Constraints

Es gelten **alle** Global Constraints aus `docs/superpowers/plans/2026-10-08-foundation-00-overview.md` sowie die „Regeln für Streams“ aus dem Phase-0-Plan. Stream-spezifisch und verbindlich:

- Eigene Pfade: `internal/auth/**`, `internal/store/queries/auth.sql`, `internal/store/auth.sql.go` (generiert, wird committet). Alle anderen Dateien sind schreibgeschützt.
- sqlc-Querynamen beginnen mit `Auth`; Parameter immer per `sqlc.arg(name)`. Nach jeder Änderung an `auth.sql`: `sqlc generate`.
- Routen nur über den übergebenen `*rbac.Router`, voller Pfad relativ zu `/api`, nie `Route()` im Produktivcode. Tests mounten den Router per `chi.Router.Route("/api", …)` + `rbac.NewRouter(sub, …)`.
- Tests nur mit Standardbibliothek; DB über `storetest.Open`/`storetest.CreateUser`; Zeit über `clock.Fake`; Doubles `audit.MemoryRecorder`, `notify.Recording`, `defense.FakeGuard` (im Test eingebettet in `testGuard`, der nur `last()` ergänzt).
- Session-Token: 32 Byte aus `crypto/rand`, base64url ohne Padding (43 Zeichen); DB speichert `SHA-256(token)` in `sessions.token_hash`.
- Cookie: `__Host-sid`, `HttpOnly`, `Secure`, `SameSite=Strict`, `Path=/`, ohne `Max-Age` (Browser-Session-Cookie; Ablauf erzwingt der Server). Löschen per `Max-Age=-1`.
- Laufzeiten aus `config.Session`: Pre-Auth `expires_at = created_at + PreAuth` (5 min); Setup/Full `expires_at = created_at + Absolute` (12 h) plus Idle (30 min, gemessen an `last_seen_at`, gilt nicht für Pre-Auth); Sudo `sudo_until = now + Sudo` (10 min). `last_seen_at` wird höchstens einmal pro Minute geschrieben.
- Token-Rotation: bei **jedem** Stage-Wechsel (preauth→full, setup→full) und bei jedem Sudo. CSRF-Token wird nur bei Login und preauth→full neu erzeugt (beide Antworten liefern `Me.csrf_token`); bei setup→full und Sudo bleibt er gleich.
- CSRF: gilt für alle Methoden außer GET/HEAD/OPTIONS. (1) Mindestens einer der Header `Sec-Fetch-Site`/`Origin` muss vorhanden sein; `Sec-Fetch-Site` muss – falls vorhanden – `same-origin` oder `none` sein; `Origin` muss – falls vorhanden – exakt `cfg.Origin()` sein. (2) Existiert eine Session (Principal im Kontext) und ist der Pfad nicht `/api/auth/login`, muss `X-CSRF-Token` per `subtle.ConstantTimeCompare` dem Session-Token gleichen. Fehler → `403 csrf_invalid`.
- TOTP: SHA1, 30 s, 6 Ziffern, Toleranz ±1 Schritt, Issuer `mc-panel`, Secret 20 Byte; Secret verschlüsselt mit `secretbox` und AAD `totp:<userID>`. Replay-Schutz über `users.totp_last_step` (atomar `UPDATE … WHERE totp_last_step < step`), gemeinsam für Login, Sudo und Aktivierung.
- **Ausstehendes TOTP-Setup:** Das neue Secret wird verschlüsselt in `users.totp_secret_enc` bei `totp_enabled = 0` abgelegt („pending“). `POST /me/totp/enable` aktiviert genau dieses Secret. Ist TOTP bereits aktiv, antwortet `POST /me/totp/setup` mit `409 conflict` (erst deaktivieren) – damit wird ein aktives Secret nie überschrieben. *Vertragsergänzung für den Lead:* `Conflict` bei `setupTotp` in `api/openapi.yaml` aufnehmen.
- Recovery-Codes: 10 Stück, je 10 Zeichen aus `abcdefghjkmnpqrstuvwxyz23456789`, Anzeige `xxxxx-xxxxx`, gespeichert `SHA-256(normalisiert)` (Kleinbuchstaben, ohne `-`/Leerzeichen). Automatische Ausgabe, wenn beim Aktivieren einer 2FA-Methode keine unbenutzten Codes existieren; sonst `codes: []`. Wird die letzte 2FA-Methode entfernt, werden alle Recovery-Codes gelöscht.
- 2FA-Pflicht („mandatory“): Rolle `admin` **oder** (`user` und `store.SecuritySettings.Enforce2FAForUsers`). Dieselbe Regel bestimmt die Setup-Stage beim Login und blockiert (`409 conflict`) das Entfernen der letzten Methode – geprüft in einer `BEGIN IMMEDIATE`-Transaktion.
- Login-Versuche: jeder Passwort-, 2FA- und Sudo-Versuch → `LoginGuard.RecordAttempt`. `Reason`-Werte (Vertrag für Stream B): Fehler `unknown_user`, `bad_password`, `admin_network`, `bad_code`, `replayed_code`, `method_not_enabled`, `bad_assertion`, `clone_warning`, `no_ceremony`, `bad_current_password`; Erfolg `complete` (Session erreicht setup/full), `second_factor_pending` (Pre-Auth), `sudo`.
- Neues Gerät: **Stream A erkennt keine neuen Geräte und sendet kein `notify.EventNewDeviceLogin`.** Das übernimmt Stream B in `defense.LoginGuard.RecordAttempt` (erster Versuch mit `success = 1 AND reason = 'complete'` für `user_id` + `ip_key`). Stream A meldet dafür beim Erreichen von setup/full genau einen Erfolgsversuch mit `Reason: "complete"` (Passwort-Stage beim direkten Login, sonst die Stage des zweiten Faktors).
- Audit: Erfolgreiche Logins (`auth.login`), Logout, Passwortänderung, TOTP/Passkey an/aus, Recovery benutzt/neu, Session-Widerruf, Sudo (Erfolg und Fehler). Fehlgeschlagene Logins landen **nur** in `login_attempts` (kein Audit-Flooding der Hash-Kette durch Angreifer). Audit-/Record-Fehler werden geloggt (`slog`), brechen die Anfrage aber nicht ab. **`audit.Recorder.Record` nie innerhalb einer eigenen offenen DB-Transaktion aufrufen** (Stream Ds `audit.SQLRecorder` öffnet selbst eine `BEGIN IMMEDIATE`-Transaktion und würde 5 s blockieren und den Eintrag verlieren) – Audit immer erst nach `Commit`. Phase 2 übergibt einen gemeinsamen Recorder pro Prozess.
- Guard-Fehler-Mapping: `*defense.RateLimitError` → `429 rate_limited` + `Retry-After` (aufgerundete Sekunden, min. 1); `defense.ErrJailed` → `403 jailed`; `defense.ErrIPBlocked` → `403 ip_blocked`; `defense.ErrAdminNetwork` → `403 forbidden` („login not allowed“); sonst `500`.
- WebAuthn: `RPID = cfg.RPID()`, `RPOrigins = [cfg.Origin()]`, `RPDisplayName = "mc-panel"`, ResidentKey `preferred`, UserVerification `preferred`. Zeremonie-Zustand als JSON `{"kind","created_at","data"}` in `sessions.webauthn_state`, einmalig (atomar per `UPDATE … RETURNING` entnommen), Gültigkeit 5 min nach `clock`, an `kind` (`login`|`sudo`|`register`) gebunden.

## Review Focus

Aus der Übersicht gehören Stream A:

1. **Übersicht #2 – Letzter Admin / letzte 2FA-Methode:** `DELETE /me/totp` und `DELETE /me/passkeys/{id}` antworten `409 conflict`, wenn danach ein Admin (oder ein User bei aktivierter Erzwingung) keine 2FA-Methode mehr hätte; die Prüfung läuft in einer IMMEDIATE-Transaktion. → Tests `TestDisableTOTPLastFactorAdmin`, `TestDisableTOTPEnforcedUser` (Task 8), `TestDeleteLastPasskeyOfAdmin`, `TestConcurrentFactorRemovalKeepsOne` (Task 10).
2. **Übersicht #3 – TOTP-Replay über Kontexte:** Ein beim Login verwendeter Code ist für Sudo ungültig (gemeinsames `totp_last_step`). → `TestTOTPCodeNotReusableForSudo` (Task 9), `TestTOTPReplayRejectedOnLogin` (Task 7).

Stream-spezifisch:

3. **Session Fixation:** Nach jedem Stage-Wechsel und nach Sudo ist das alte Token ungültig; ein beim Login mitgesendetes Cookie wird widerrufen. → `TestTOTPLoginFlow` (Task 7), `TestLoginRevokesPresentedSession` (Task 6), `TestSudoTOTPGrantsSudo` (Task 9).
4. **Benutzer-Enumeration:** Unbekannter Benutzer und falsches Passwort liefern byte-identische 401-Antworten; für unbekannte Benutzer läuft `password.DummyVerify`. → `TestLoginInvalidCredentialsIdentical` (Task 6).
5. **WebAuthn-Zeremonie-Replay und geklonte Authenticatoren:** Zustand ist einmalig, an die Zeremonieart gebunden und läuft nach 5 min ab; ein nicht steigender Sign-Counter wird abgelehnt. → `TestWebAuthnCeremonyIsOneShotAndKindBound`, `TestWebAuthnCloneDetected`, `TestSudoWebAuthnCeremonyExpires` (Task 11).

## Dateistruktur

```
internal/store/queries/auth.sql        sqlc-Queries (Präfix Auth)
internal/store/auth.sql.go             generiert von sqlc (committet)
internal/auth/doc.go                   Paketdoku
internal/auth/token.go                 Zufallstoken, SHA-256-Hash, Formatprüfung
internal/auth/recovery.go              Recovery-Codes erzeugen, normalisieren, hashen
internal/auth/totp.go                  TOTP-Schlüssel, Schritt-Abgleich, Secret ver-/entschlüsseln
internal/auth/session.go               Sessions: Create/Lookup/Touch/Rotate/Revoke, Cookie-Helfer
internal/auth/middleware.go            Session-Loader-Middleware, CSRF-Middleware
internal/auth/handler.go               Deps, New, Routes, gemeinsame Helfer, Me-Antwort, Guard-Mapping
internal/auth/login.go                 POST /auth/login, GET /auth/me, POST /auth/logout
internal/auth/twofa.go                 POST /auth/2fa/totp|recovery, Login-Abschluss (preauth → full)
internal/auth/totp_setup.go            POST /me/totp/setup|enable, DELETE /me/totp, Recovery-Ausgabe, Faktor-Entfernung
internal/auth/sudo.go                  POST /auth/sudo/totp, Sudo-Vergabe
internal/auth/webauthn.go              WebAuthn-Konfiguration, User-Adapter, Zeremonie-Zustand
internal/auth/passkeys.go              POST /me/passkeys/begin|finish, GET /me/passkeys, DELETE /me/passkeys/{id}
internal/auth/webauthn_login.go        POST /auth/2fa/webauthn/*, POST /auth/sudo/webauthn/*
internal/auth/me.go                    PATCH /me, POST /me/password, POST /me/recovery-codes
internal/auth/sessions_api.go          /me/sessions*, /admin/sessions*
internal/auth/*_test.go                Tests je Datei (+ testenv_test.go, softauthn_test.go)
```

---

### Task 1: sqlc-Queries für Auth

**Files:**
- Create: `internal/store/queries/auth.sql`, `internal/auth/doc.go`
- Generated: `internal/store/auth.sql.go`
- Test: `internal/auth/queries_test.go`

**Interfaces:**
- Consumes: Schema aus `internal/store/migrations/00001_init.sql`; `store.New(db DBTX) *Queries`; `(*Queries).WithTx(tx *sql.Tx) *Queries` (sqlc-Standard); Modelle `store.User`, `store.Session`, `store.WebauthnCredential`; `storetest.Open`, `storetest.CreateUser`.
- Produces (erwartete sqlc-Signaturen; nach `sqlc generate` gegen `internal/store/auth.sql.go` prüfen):
```go
func (q *Queries) AuthGetUserByUsername(ctx context.Context, username string) (User, error)
func (q *Queries) AuthGetUserByID(ctx context.Context, id int64) (User, error)
func (q *Queries) AuthRehashPassword(ctx context.Context, arg AuthRehashPasswordParams) error            // PasswordHash string, Now int64, ID int64
func (q *Queries) AuthChangePassword(ctx context.Context, arg AuthChangePasswordParams) error            // PasswordHash, Now, ID
func (q *Queries) AuthUpdateLocale(ctx context.Context, arg AuthUpdateLocaleParams) error                // Locale string, Now, ID
func (q *Queries) AuthSetPendingTOTP(ctx context.Context, arg AuthSetPendingTOTPParams) (int64, error)   // TotpSecretEnc []byte, Now, ID
func (q *Queries) AuthEnableTOTP(ctx context.Context, arg AuthEnableTOTPParams) (int64, error)           // Step, Now, ID
func (q *Queries) AuthDisableTOTP(ctx context.Context, arg AuthDisableTOTPParams) (int64, error)         // Now, ID
func (q *Queries) AuthAdvanceTOTPStep(ctx context.Context, arg AuthAdvanceTOTPStepParams) (int64, error) // Step, ID
func (q *Queries) AuthListCredentials(ctx context.Context, userID int64) ([]WebauthnCredential, error)
func (q *Queries) AuthCountCredentials(ctx context.Context, userID int64) (int64, error)
func (q *Queries) AuthInsertCredential(ctx context.Context, arg AuthInsertCredentialParams) (WebauthnCredential, error) // UserID, CredentialID []byte, Name, CredentialJson string, CreatedAt
func (q *Queries) AuthUpdateCredentialUse(ctx context.Context, arg AuthUpdateCredentialUseParams) error  // CredentialJson, LastUsedAt sql.NullInt64, ID
func (q *Queries) AuthGetCredential(ctx context.Context, arg AuthGetCredentialParams) (WebauthnCredential, error) // ID, UserID
func (q *Queries) AuthDeleteCredential(ctx context.Context, arg AuthDeleteCredentialParams) (int64, error)        // ID, UserID
func (q *Queries) AuthDeleteRecoveryCodes(ctx context.Context, userID int64) error
func (q *Queries) AuthInsertRecoveryCode(ctx context.Context, arg AuthInsertRecoveryCodeParams) error    // UserID, CodeHash []byte, CreatedAt
func (q *Queries) AuthUseRecoveryCode(ctx context.Context, arg AuthUseRecoveryCodeParams) (int64, error) // UsedAt sql.NullInt64, UserID, CodeHash
func (q *Queries) AuthCountUnusedRecoveryCodes(ctx context.Context, userID int64) (int64, error)
func (q *Queries) AuthCreateSession(ctx context.Context, arg AuthCreateSessionParams) (Session, error)
	// TokenHash []byte, UserID int64, Stage, CsrfToken, Ip, UserAgent, Country string, CreatedAt, LastSeenAt, ExpiresAt int64
func (q *Queries) AuthGetSessionByTokenHash(ctx context.Context, tokenHash []byte) (Session, error)
func (q *Queries) AuthGetSessionByID(ctx context.Context, id int64) (Session, error)
func (q *Queries) AuthRotateSession(ctx context.Context, arg AuthRotateSessionParams) (int64, error)
	// TokenHash, Stage, CsrfToken, SudoUntil, ExpiresAt, LastSeenAt, ID
func (q *Queries) AuthTouchSession(ctx context.Context, arg AuthTouchSessionParams) error                // LastSeenAt, ID
func (q *Queries) AuthSetWebAuthnState(ctx context.Context, arg AuthSetWebAuthnStateParams) error        // WebauthnState sql.NullString, ID
func (q *Queries) AuthTakeWebAuthnState(ctx context.Context, id int64) (sql.NullString, error)
func (q *Queries) AuthRevokeSession(ctx context.Context, arg AuthRevokeSessionParams) (int64, error)     // RevokedAt sql.NullInt64, ID
func (q *Queries) AuthRevokeUserSession(ctx context.Context, arg AuthRevokeUserSessionParams) (int64, error) // RevokedAt, ID, UserID
func (q *Queries) AuthRevokeOtherSessions(ctx context.Context, arg AuthRevokeOtherSessionsParams) (int64, error) // RevokedAt, UserID, KeepID
func (q *Queries) AuthListActiveSessionsForUser(ctx context.Context, arg AuthListActiveSessionsForUserParams) ([]AuthListActiveSessionsForUserRow, error) // Now, IdleCutoff, UserID
func (q *Queries) AuthListAllActiveSessions(ctx context.Context, arg AuthListAllActiveSessionsParams) ([]AuthListAllActiveSessionsRow, error)             // Now, IdleCutoff
	// Row-Felder beider: ID, UserID int64, Username, Ip, Country, UserAgent string, CreatedAt, LastSeenAt, ExpiresAt int64
```

- [ ] **Step 1: Paketdoku `internal/auth/doc.go`**

```go
// Package auth implements login (password, TOTP, WebAuthn, recovery codes),
// server-side sessions, CSRF protection and sudo mode for mc-panel.
package auth
```

- [ ] **Step 2: Failing test `internal/auth/queries_test.go`**

```go
package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestAuthQueries(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := context.Background()
	uid := storetest.CreateUser(t, db, "alice", "user")

	t.Run("username lookup is case-insensitive", func(t *testing.T) {
		u, err := q.AuthGetUserByUsername(ctx, "ALICE")
		if err != nil || u.ID != uid {
			t.Fatalf("got %+v, %v", u, err)
		}
	})

	t.Run("totp step only advances", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE users SET totp_enabled = 1 WHERE id = ?`, uid); err != nil {
			t.Fatal(err)
		}
		n, err := q.AuthAdvanceTOTPStep(ctx, store.AuthAdvanceTOTPStepParams{Step: 100, ID: uid})
		if err != nil || n != 1 {
			t.Fatalf("first advance: n=%d err=%v", n, err)
		}
		for _, step := range []int64{100, 99} {
			if n, _ := q.AuthAdvanceTOTPStep(ctx, store.AuthAdvanceTOTPStepParams{Step: step, ID: uid}); n != 0 {
				t.Fatalf("step %d must not be accepted again", step)
			}
		}
		u, _ := q.AuthGetUserByID(ctx, uid)
		if u.TotpLastStep != 100 {
			t.Fatalf("TotpLastStep = %d", u.TotpLastStep)
		}
	})

	t.Run("recovery code is one-time", func(t *testing.T) {
		h := []byte("0123456789abcdef0123456789abcdef")
		if err := q.AuthInsertRecoveryCode(ctx, store.AuthInsertRecoveryCodeParams{UserID: uid, CodeHash: h, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		if c, _ := q.AuthCountUnusedRecoveryCodes(ctx, uid); c != 1 {
			t.Fatalf("unused = %d", c)
		}
		use := store.AuthUseRecoveryCodeParams{UsedAt: sql.NullInt64{Int64: 2, Valid: true}, UserID: uid, CodeHash: h}
		if n, err := q.AuthUseRecoveryCode(ctx, use); err != nil || n != 1 {
			t.Fatalf("first use n=%d err=%v", n, err)
		}
		if n, _ := q.AuthUseRecoveryCode(ctx, use); n != 0 {
			t.Fatal("second use must fail")
		}
		if c, _ := q.AuthCountUnusedRecoveryCodes(ctx, uid); c != 0 {
			t.Fatalf("unused = %d", c)
		}
	})

	t.Run("webauthn state is taken once", func(t *testing.T) {
		s, err := q.AuthCreateSession(ctx, store.AuthCreateSessionParams{
			TokenHash: []byte("hash-1"), UserID: uid, Stage: "full", CsrfToken: "c", Ip: "203.0.113.7",
			UserAgent: "ua", Country: "", CreatedAt: 1000, LastSeenAt: 1000, ExpiresAt: 99999,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.AuthSetWebAuthnState(ctx, store.AuthSetWebAuthnStateParams{
			WebauthnState: sql.NullString{String: `{"kind":"login"}`, Valid: true}, ID: s.ID,
		}); err != nil {
			t.Fatal(err)
		}
		got, err := q.AuthTakeWebAuthnState(ctx, s.ID)
		if err != nil || got.String != `{"kind":"login"}` {
			t.Fatalf("take = %v, %v", got, err)
		}
		if _, err := q.AuthTakeWebAuthnState(ctx, s.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("second take must be ErrNoRows, got %v", err)
		}
	})

	t.Run("active sessions exclude revoked, expired and idle", func(t *testing.T) {
		mk := func(hash string, lastSeen, expires int64) int64 {
			s, err := q.AuthCreateSession(ctx, store.AuthCreateSessionParams{
				TokenHash: []byte(hash), UserID: uid, Stage: "full", CsrfToken: "c", Ip: "203.0.113.7",
				UserAgent: "ua", CreatedAt: 0, LastSeenAt: lastSeen, ExpiresAt: expires,
			})
			if err != nil {
				t.Fatal(err)
			}
			return s.ID
		}
		active := mk("a", 5000, 10000)
		revoked := mk("r", 5000, 10000)
		mk("e", 5000, 4000) // expired
		mk("i", 100, 10000) // idle
		if _, err := q.AuthRevokeSession(ctx, store.AuthRevokeSessionParams{RevokedAt: sql.NullInt64{Int64: 1, Valid: true}, ID: revoked}); err != nil {
			t.Fatal(err)
		}
		rows, err := q.AuthListActiveSessionsForUser(ctx, store.AuthListActiveSessionsForUserParams{Now: 4500, IdleCutoff: 1000, UserID: uid})
		if err != nil {
			t.Fatal(err)
		}
		// The session from "webauthn state is taken once" has last_seen_at = 1000, which is
		// not > IdleCutoff (1000), so exactly one session ("a") is active.
		if len(rows) != 1 || rows[0].ID != active || rows[0].Username != "alice" {
			t.Fatalf("unexpected active sessions: %+v", rows)
		}
		all, err := q.AuthListAllActiveSessions(ctx, store.AuthListAllActiveSessionsParams{Now: 4500, IdleCutoff: 1000})
		if err != nil || len(all) != 1 || all[0].ID != active {
			t.Fatalf("all = %+v, err=%v", all, err)
		}
	})

}
```

- [ ] **Step 3: Run** `go test ./internal/auth/ -run TestAuthQueries` → FAIL (`undefined: store.AuthAdvanceTOTPStepParams` u. a.).

- [ ] **Step 4: `internal/store/queries/auth.sql`**

```sql
-- Stream A (auth). All query names carry the prefix "Auth".

-- name: AuthGetUserByUsername :one
SELECT * FROM users WHERE username = sqlc.arg(username);

-- name: AuthGetUserByID :one
SELECT * FROM users WHERE id = sqlc.arg(id);

-- name: AuthRehashPassword :exec
UPDATE users SET password_hash = sqlc.arg(password_hash), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: AuthChangePassword :exec
UPDATE users SET password_hash = sqlc.arg(password_hash), password_changed_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: AuthUpdateLocale :exec
UPDATE users SET locale = sqlc.arg(locale), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: AuthSetPendingTOTP :execrows
UPDATE users SET totp_secret_enc = sqlc.arg(totp_secret_enc), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND totp_enabled = 0;

-- name: AuthEnableTOTP :execrows
UPDATE users SET totp_enabled = 1, totp_last_step = sqlc.arg(step), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND totp_enabled = 0 AND totp_secret_enc IS NOT NULL AND totp_last_step < sqlc.arg(step);

-- name: AuthDisableTOTP :execrows
UPDATE users SET totp_enabled = 0, totp_secret_enc = NULL, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND totp_enabled = 1;

-- name: AuthAdvanceTOTPStep :execrows
UPDATE users SET totp_last_step = sqlc.arg(step)
WHERE id = sqlc.arg(id) AND totp_enabled = 1 AND totp_last_step < sqlc.arg(step);

-- name: AuthListCredentials :many
SELECT * FROM webauthn_credentials WHERE user_id = sqlc.arg(user_id) ORDER BY id;

-- name: AuthCountCredentials :one
SELECT count(*) FROM webauthn_credentials WHERE user_id = sqlc.arg(user_id);

-- name: AuthInsertCredential :one
INSERT INTO webauthn_credentials (user_id, credential_id, name, credential_json, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(credential_id), sqlc.arg(name), sqlc.arg(credential_json), sqlc.arg(created_at))
RETURNING *;

-- name: AuthUpdateCredentialUse :exec
UPDATE webauthn_credentials SET credential_json = sqlc.arg(credential_json), last_used_at = sqlc.arg(last_used_at)
WHERE id = sqlc.arg(id);

-- name: AuthGetCredential :one
SELECT * FROM webauthn_credentials WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: AuthDeleteCredential :execrows
DELETE FROM webauthn_credentials WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: AuthDeleteRecoveryCodes :exec
DELETE FROM recovery_codes WHERE user_id = sqlc.arg(user_id);

-- name: AuthInsertRecoveryCode :exec
INSERT INTO recovery_codes (user_id, code_hash, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(code_hash), sqlc.arg(created_at));

-- name: AuthUseRecoveryCode :execrows
UPDATE recovery_codes SET used_at = sqlc.arg(used_at)
WHERE user_id = sqlc.arg(user_id) AND code_hash = sqlc.arg(code_hash) AND used_at IS NULL;

-- name: AuthCountUnusedRecoveryCodes :one
SELECT count(*) FROM recovery_codes WHERE user_id = sqlc.arg(user_id) AND used_at IS NULL;

-- name: AuthCreateSession :one
INSERT INTO sessions (token_hash, user_id, stage, csrf_token, ip, user_agent, country, created_at, last_seen_at, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(user_id), sqlc.arg(stage), sqlc.arg(csrf_token), sqlc.arg(ip), sqlc.arg(user_agent),
        sqlc.arg(country), sqlc.arg(created_at), sqlc.arg(last_seen_at), sqlc.arg(expires_at))
RETURNING *;

-- name: AuthGetSessionByTokenHash :one
SELECT * FROM sessions WHERE token_hash = sqlc.arg(token_hash);

-- name: AuthGetSessionByID :one
SELECT * FROM sessions WHERE id = sqlc.arg(id);

-- name: AuthRotateSession :execrows
UPDATE sessions
SET token_hash = sqlc.arg(token_hash), stage = sqlc.arg(stage), csrf_token = sqlc.arg(csrf_token),
    sudo_until = sqlc.arg(sudo_until), expires_at = sqlc.arg(expires_at), last_seen_at = sqlc.arg(last_seen_at),
    webauthn_state = NULL
WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: AuthTouchSession :exec
UPDATE sessions SET last_seen_at = sqlc.arg(last_seen_at) WHERE id = sqlc.arg(id);

-- name: AuthSetWebAuthnState :exec
UPDATE sessions SET webauthn_state = sqlc.arg(webauthn_state) WHERE id = sqlc.arg(id);

-- name: AuthTakeWebAuthnState :one
UPDATE sessions SET webauthn_state = NULL
WHERE id = sqlc.arg(id) AND webauthn_state IS NOT NULL
RETURNING webauthn_state;

-- name: AuthRevokeSession :execrows
UPDATE sessions SET revoked_at = sqlc.arg(revoked_at) WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: AuthRevokeUserSession :execrows
UPDATE sessions SET revoked_at = sqlc.arg(revoked_at)
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- name: AuthRevokeOtherSessions :execrows
UPDATE sessions SET revoked_at = sqlc.arg(revoked_at)
WHERE user_id = sqlc.arg(user_id) AND id != sqlc.arg(keep_id) AND revoked_at IS NULL;

-- name: AuthListActiveSessionsForUser :many
SELECT s.id, s.user_id, u.username, s.ip, s.country, s.user_agent, s.created_at, s.last_seen_at, s.expires_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.revoked_at IS NULL AND s.expires_at > sqlc.arg(now) AND s.last_seen_at > sqlc.arg(idle_cutoff)
  AND s.user_id = sqlc.arg(user_id)
ORDER BY s.last_seen_at DESC, s.id DESC;

-- name: AuthListAllActiveSessions :many
SELECT s.id, s.user_id, u.username, s.ip, s.country, s.user_agent, s.created_at, s.last_seen_at, s.expires_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.revoked_at IS NULL AND s.expires_at > sqlc.arg(now) AND s.last_seen_at > sqlc.arg(idle_cutoff)
ORDER BY s.last_seen_at DESC, s.id DESC;
```

- [ ] **Step 5: Generieren und Signaturen prüfen**

```bash
sqlc generate
grep -n "^func (q \*Queries) Auth" internal/store/auth.sql.go | wc -l
grep -n "type AuthListActiveSessionsForUserRow\|type AuthRotateSessionParams\|IdleCutoff\|KeepID" internal/store/auth.sql.go
```
Expected: `31` Methoden; Typen/Felder vorhanden. Weicht ein Parametertyp von der Liste unter **Interfaces** ab (z. B. `interface{}` statt `int64` bei `Now`/`IdleCutoff`/`Step`), den betroffenen Ausdruck in `auth.sql` als `CAST(sqlc.arg(x) AS INTEGER)` schreiben und neu generieren.

- [ ] **Step 6: Run** `go test ./internal/auth/ -run TestAuthQueries -v` → PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/store/queries/auth.sql internal/store/auth.sql.go internal/auth/doc.go internal/auth/queries_test.go
git commit -m "feat(auth): add auth sqlc queries" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Tokens und Recovery-Codes

**Files:**
- Create: `internal/auth/token.go`, `internal/auth/recovery.go`
- Test: `internal/auth/token_test.go`, `internal/auth/recovery_test.go`

**Interfaces:**
- Consumes: –
- Produces:
```go
func newToken() (string, error)          // 32 random bytes, base64url without padding (43 chars)
func hashToken(tok string) []byte        // SHA-256
func validTokenFormat(tok string) bool
const RecoveryCodeCount = 10
func generateRecoveryCodes() ([]string, error)   // 10 distinct codes "xxxxx-xxxxx"
func normalizeRecoveryCode(s string) string      // lower-case, without '-' and whitespace
func hashRecoveryCode(s string) []byte           // SHA-256(normalizeRecoveryCode(s))
```

- [ ] **Step 1: Failing tests**

`internal/auth/token_test.go`:
```go
package auth

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestNewToken(t *testing.T) {
	a, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newToken()
	if len(a) != 43 || a == b {
		t.Fatalf("tokens %q / %q", a, b)
	}
	if !validTokenFormat(a) {
		t.Fatal("generated token must have valid format")
	}
	for _, bad := range []string{"", "short", a + "x", a[:42] + "!", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="} {
		if validTokenFormat(bad) {
			t.Errorf("validTokenFormat(%q) = true", bad)
		}
	}
	want := sha256.Sum256([]byte(a))
	if !bytes.Equal(hashToken(a), want[:]) {
		t.Fatal("hashToken must be SHA-256 of the token string")
	}
}
```

`internal/auth/recovery_test.go`:
```go
package auth

import (
	"bytes"
	"regexp"
	"testing"
)

var recoveryFormat = regexp.MustCompile(`^[abcdefghjkmnpqrstuvwxyz23456789]{5}-[abcdefghjkmnpqrstuvwxyz23456789]{5}$`)

func TestGenerateRecoveryCodes(t *testing.T) {
	codes, err := generateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount || RecoveryCodeCount != 10 {
		t.Fatalf("len = %d", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if !recoveryFormat.MatchString(c) {
			t.Errorf("bad format %q", c)
		}
		if seen[c] {
			t.Errorf("duplicate %q", c)
		}
		seen[c] = true
	}
	other, _ := generateRecoveryCodes()
	if other[0] == codes[0] && other[1] == codes[1] {
		t.Fatal("codes must be random")
	}
}

func TestRecoveryCodeNormalization(t *testing.T) {
	if got := normalizeRecoveryCode(" ABCDE-fghjk \t"); got != "abcdefghjk" {
		t.Fatalf("normalize = %q", got)
	}
	if !bytes.Equal(hashRecoveryCode("abcde-fghjk"), hashRecoveryCode("ABCDE FGHJK")) {
		t.Fatal("hash must be computed over the normalized code")
	}
	if bytes.Equal(hashRecoveryCode("abcde-fghjk"), hashRecoveryCode("abcde-fghjm")) {
		t.Fatal("different codes must hash differently")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/auth/ -run 'TestNewToken|TestGenerateRecoveryCodes|TestRecoveryCodeNormalization'` → FAIL (`undefined: newToken`).

- [ ] **Step 3: `internal/auth/token.go`**

```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const tokenBytes = 32

// newToken returns 256 random bits encoded as unpadded base64url (43 characters).
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns the SHA-256 of the token; only this hash is stored.
func hashToken(tok string) []byte {
	sum := sha256.Sum256([]byte(tok))
	return sum[:]
}

// validTokenFormat rejects garbage cookies before they reach the database.
func validTokenFormat(tok string) bool {
	if len(tok) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(tok)
	return err == nil && len(b) == tokenBytes
}
```

- [ ] **Step 4: `internal/auth/recovery.go`**

```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"math/big"
	"strings"
	"unicode"
)

const (
	RecoveryCodeCount = 10
	recoveryCodeLen   = 10
	recoveryAlphabet  = "abcdefghjkmnpqrstuvwxyz23456789"
)

// generateRecoveryCodes returns RecoveryCodeCount distinct codes formatted as "xxxxx-xxxxx".
func generateRecoveryCodes() ([]string, error) {
	alphabetLen := big.NewInt(int64(len(recoveryAlphabet)))
	seen := make(map[string]bool, RecoveryCodeCount)
	codes := make([]string, 0, RecoveryCodeCount)
	for len(codes) < RecoveryCodeCount {
		var sb strings.Builder
		for i := 0; i < recoveryCodeLen; i++ {
			if i == recoveryCodeLen/2 {
				sb.WriteByte('-')
			}
			n, err := rand.Int(rand.Reader, alphabetLen)
			if err != nil {
				return nil, err
			}
			sb.WriteByte(recoveryAlphabet[n.Int64()])
		}
		code := sb.String()
		if seen[code] {
			continue
		}
		seen[code] = true
		codes = append(codes, code)
	}
	return codes, nil
}

func normalizeRecoveryCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

func hashRecoveryCode(s string) []byte {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(s)))
	return sum[:]
}
```

- [ ] **Step 5: Run** `go test ./internal/auth/ -run 'TestNewToken|TestGenerateRecoveryCodes|TestRecoveryCodeNormalization'` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/token.go internal/auth/token_test.go internal/auth/recovery.go internal/auth/recovery_test.go
git commit -m "feat(auth): add session tokens and recovery codes" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: TOTP-Primitive

**Files:**
- Create: `internal/auth/totp.go`
- Test: `internal/auth/totp_test.go`

**Interfaces:**
- Consumes: `secretbox.New(key []byte) (*secretbox.Box, error)`, `(*Box).Seal(plaintext, aad []byte) ([]byte, error)`, `(*Box).Open(ciphertext, aad []byte) ([]byte, error)`, `secretbox.KeySize`; `totp.Generate(totp.GenerateOpts) (*otp.Key, error)`, `(*otp.Key).Secret() string`, `(*otp.Key).URL() string`, `totp.GenerateCodeCustom(secret string, t time.Time, opts totp.ValidateOpts) (string, error)`, `otp.DigitsSix`, `otp.AlgorithmSHA1` (pquerna/otp v1.5.0).
- Produces:
```go
func newTOTPKey(username string) (secret, url string, err error)
func totpStep(t time.Time) int64
func totpCodeAt(secret string, step int64) (string, error)
func matchTOTP(secret, code string, now time.Time) (step int64, ok bool)   // highest matching step within ±1
func sealTOTPSecret(box *secretbox.Box, userID int64, secret string) ([]byte, error)
func openTOTPSecret(box *secretbox.Box, userID int64, enc []byte) (string, error)
```

- [ ] **Step 1: Failing test `internal/auth/totp_test.go`**

```go
package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/secretbox"
)

func TestTOTPKey(t *testing.T) {
	secret, url, err := newTOTPKey("jannes")
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 { // 20 bytes base32 without padding
		t.Fatalf("secret len = %d (%q)", len(secret), secret)
	}
	for _, part := range []string{"otpauth://totp/", "issuer=mc-panel", "secret=" + secret, "period=30", "digits=6"} {
		if !strings.Contains(url, part) {
			t.Errorf("url %q missing %q", url, part)
		}
	}
}

func TestMatchTOTP(t *testing.T) {
	secret, _, err := newTOTPKey("jannes")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 12, 0, 10, 0, time.UTC)
	cur := totpStep(now)
	if cur != now.Unix()/30 {
		t.Fatalf("step = %d", cur)
	}
	for offset, wantOK := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		code, err := totpCodeAt(secret, cur+offset)
		if err != nil {
			t.Fatal(err)
		}
		step, ok := matchTOTP(secret, code, now)
		if ok != wantOK {
			t.Errorf("offset %d: ok=%v want %v", offset, ok, wantOK)
		}
		if ok && step != cur+offset {
			// a code can collide with a neighbouring step only by chance (1e-6); treat as failure
			t.Errorf("offset %d: step=%d want %d", offset, step, cur+offset)
		}
	}
	code, _ := totpCodeAt(secret, cur)
	if _, ok := matchTOTP(secret, " "+code[:3]+" "+code[3:]+" ", now); !ok {
		t.Error("spaces inside the code must be ignored")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "12345a"} {
		if _, ok := matchTOTP(secret, bad, now); ok {
			t.Errorf("matchTOTP(%q) = ok", bad)
		}
	}
}

func TestSealTOTPSecret(t *testing.T) {
	box, err := secretbox.New(bytes.Repeat([]byte{9}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := sealTOTPSecret(box, 7, "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("JBSWY3DPEHPK3PXP")) {
		t.Fatal("secret stored in plain text")
	}
	if got, err := openTOTPSecret(box, 7, enc); err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := openTOTPSecret(box, 8, enc); err == nil {
		t.Fatal("secret of user 7 must not open for user 8 (AAD)")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/auth/ -run 'TestTOTPKey|TestMatchTOTP|TestSealTOTPSecret'` → FAIL (`undefined: newTOTPKey`).

- [ ] **Step 3: `internal/auth/totp.go`**

```go
package auth

import (
	"crypto/subtle"
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/Bernotese/mc-panel/internal/secretbox"
)

const (
	totpPeriod     = 30
	totpSkew       = 1
	totpDigits     = 6
	totpIssuer     = "mc-panel"
	totpSecretSize = 20
)

var totpOpts = totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// newTOTPKey creates a new random TOTP secret (base32) and its otpauth:// URL.
func newTOTPKey(username string) (secret, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: username,
		Period:      totpPeriod,
		SecretSize:  totpSecretSize,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

func totpStep(t time.Time) int64 { return t.Unix() / totpPeriod }

func totpCodeAt(secret string, step int64) (string, error) {
	return totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriod, 0).UTC(), totpOpts)
}

// matchTOTP checks code against the steps now-1 … now+1 and returns the highest
// matching step. Replay protection (step > last used step) is the caller's job.
func matchTOTP(secret, code string, now time.Time) (int64, bool) {
	code = strings.Join(strings.Fields(code), "")
	if len(code) != totpDigits {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	cur := totpStep(now)
	for s := cur + totpSkew; s >= cur-totpSkew; s-- {
		want, err := totpCodeAt(secret, s)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

func totpAAD(userID int64) []byte { return []byte(fmt.Sprintf("totp:%d", userID)) }

func sealTOTPSecret(box *secretbox.Box, userID int64, secret string) ([]byte, error) {
	return box.Seal([]byte(secret), totpAAD(userID))
}

func openTOTPSecret(box *secretbox.Box, userID int64, enc []byte) (string, error) {
	pt, err := box.Open(enc, totpAAD(userID))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
```

- [ ] **Step 4: Run** `go test ./internal/auth/ -run 'TestTOTPKey|TestMatchTOTP|TestSealTOTPSecret'` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/totp.go internal/auth/totp_test.go
git commit -m "feat(auth): add TOTP key generation, step matching and secret sealing" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Sessions (Erzeugen, Prüfen, Rotieren, Widerrufen, Cookie)

**Files:**
- Create: `internal/auth/session.go`
- Test: `internal/auth/session_test.go`

**Interfaces:**
- Consumes: `clock.Clock`, `clock.NewFake`; `config.Session` (`Idle`, `Absolute`, `PreAuth`, `Sudo` vom Typ `config.Duration` mit eingebettetem `time.Duration`), `config.DefaultPanel()`; `principal.Stage*`; sqlc-Queries aus Task 1 (`AuthCreateSession`, `AuthGetSessionByTokenHash`, `AuthGetSessionByID`, `AuthTouchSession`, `AuthRotateSession`, `AuthRevokeSession`); `newToken`, `hashToken`, `validTokenFormat` (Task 2).
- Produces:
```go
const CookieName = "__Host-sid"
var ErrNoSession = errors.New("auth: no valid session")
type Sessions struct{ /* unexported */ }
func NewSessions(q *store.Queries, c clock.Clock, cfg config.Session) *Sessions
type NewSession struct {
	UserID    int64
	Stage     principal.Stage
	IP        netip.Addr
	UserAgent string
	Country   string
}
type Rotation struct {
	Stage     principal.Stage
	NewCSRF   bool
	SudoUntil int64 // unix ms; pass the current value to keep it
}
func (s *Sessions) Create(ctx context.Context, in NewSession) (token string, sess store.Session, err error)
func (s *Sessions) Lookup(ctx context.Context, token string) (store.Session, error)  // ErrNoSession if missing/revoked/expired/idle
func (s *Sessions) Touch(ctx context.Context, sess store.Session) error              // writes last_seen_at at most once per minute
func (s *Sessions) Rotate(ctx context.Context, sess store.Session, rot Rotation) (token string, updated store.Session, err error)
func (s *Sessions) Revoke(ctx context.Context, id int64) error
func setSessionCookie(w http.ResponseWriter, token string)
func clearSessionCookie(w http.ResponseWriter)
```

- [ ] **Step 1: Failing test `internal/auth/session_test.go`**

```go
package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var sessionStart = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func newTestSessions(t *testing.T) (*Sessions, *store.Queries, *clock.Fake, int64) {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(sessionStart)
	q := store.New(db)
	uid := storetest.CreateUser(t, db, "alice", "user")
	return NewSessions(q, clk, config.DefaultPanel().Session), q, clk, uid
}

func mustCreate(t *testing.T, s *Sessions, uid int64, stage principal.Stage) (string, store.Session) {
	t.Helper()
	tok, sess, err := s.Create(context.Background(), NewSession{
		UserID: uid, Stage: stage, IP: netip.MustParseAddr("203.0.113.7"), UserAgent: "ua", Country: "DE",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok, sess
}

func TestSessionCreateAndLookup(t *testing.T) {
	s, _, _, uid := newTestSessions(t)
	ctx := context.Background()
	tok, sess := mustCreate(t, s, uid, principal.StageFull)
	if !bytes.Equal(sess.TokenHash, hashToken(tok)) || bytes.Contains(sess.TokenHash, []byte(tok)) {
		t.Fatal("DB must store only SHA-256 of the token")
	}
	if sess.CsrfToken == "" || sess.Ip != "203.0.113.7" || sess.Country != "DE" || sess.UserAgent != "ua" ||
		sess.Stage != "full" || sess.ExpiresAt != sessionStart.Add(12*time.Hour).UnixMilli() {
		t.Fatalf("unexpected session %+v", sess)
	}
	got, err := s.Lookup(ctx, tok)
	if err != nil || got.ID != sess.ID {
		t.Fatalf("Lookup = %+v, %v", got, err)
	}
	other, _ := newToken()
	for _, bad := range []string{"", "garbage", other} {
		if _, err := s.Lookup(ctx, bad); !errors.Is(err, ErrNoSession) {
			t.Errorf("Lookup(%q) err = %v, want ErrNoSession", bad, err)
		}
	}
}

func TestPreAuthSessionExpiresAfterFiveMinutes(t *testing.T) {
	s, _, clk, uid := newTestSessions(t)
	tok, sess := mustCreate(t, s, uid, principal.StagePreAuth)
	if sess.ExpiresAt != sessionStart.Add(5*time.Minute).UnixMilli() {
		t.Fatalf("ExpiresAt = %d", sess.ExpiresAt)
	}
	clk.Advance(5*time.Minute - time.Second)
	if _, err := s.Lookup(context.Background(), tok); err != nil {
		t.Fatalf("still valid: %v", err)
	}
	clk.Advance(time.Second)
	if _, err := s.Lookup(context.Background(), tok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("expired preauth: err = %v", err)
	}
}

func TestIdleTimeoutAndTouchThrottling(t *testing.T) {
	s, q, clk, uid := newTestSessions(t)
	ctx := context.Background()
	tok, sess := mustCreate(t, s, uid, principal.StageFull)

	clk.Advance(30 * time.Second)
	if err := s.Touch(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.AuthGetSessionByID(ctx, sess.ID); got.LastSeenAt != sessionStart.UnixMilli() {
		t.Fatalf("touch within a minute must not write, last_seen=%d", got.LastSeenAt)
	}
	clk.Advance(31 * time.Second)
	if err := s.Touch(ctx, sess); err != nil {
		t.Fatal(err)
	}
	touched := clk.Now()
	if got, _ := q.AuthGetSessionByID(ctx, sess.ID); got.LastSeenAt != touched.UnixMilli() {
		t.Fatalf("touch after a minute must write, last_seen=%d", got.LastSeenAt)
	}
	clk.Advance(30*time.Minute - time.Second)
	if _, err := s.Lookup(ctx, tok); err != nil {
		t.Fatalf("29:59 idle must be valid: %v", err)
	}
	clk.Advance(time.Second)
	if _, err := s.Lookup(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("30 min idle must expire, err = %v", err)
	}
}

func TestAbsoluteTimeout(t *testing.T) {
	s, _, clk, uid := newTestSessions(t)
	ctx := context.Background()
	tok, _ := mustCreate(t, s, uid, principal.StageFull)
	for i := 0; i < 24; i++ { // 24 × 29 min = 11h36m, always active
		clk.Advance(29 * time.Minute)
		sess, err := s.Lookup(ctx, tok)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if err := s.Touch(ctx, sess); err != nil {
			t.Fatal(err)
		}
	}
	clk.Advance(29 * time.Minute) // 12h05m
	if _, err := s.Lookup(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("absolute timeout not enforced, err = %v", err)
	}
}

func TestRotateAndRevoke(t *testing.T) {
	s, _, _, uid := newTestSessions(t)
	ctx := context.Background()
	tok, sess := mustCreate(t, s, uid, principal.StagePreAuth)

	tok2, full, err := s.Rotate(ctx, sess, Rotation{Stage: principal.StageFull, NewCSRF: true})
	if err != nil {
		t.Fatal(err)
	}
	if tok2 == tok || full.Stage != "full" || full.CsrfToken == sess.CsrfToken ||
		full.ExpiresAt != sessionStart.Add(12*time.Hour).UnixMilli() || full.ID != sess.ID {
		t.Fatalf("rotated = %+v", full)
	}
	if _, err := s.Lookup(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Fatal("old token must be invalid after rotation")
	}
	if got, err := s.Lookup(ctx, tok2); err != nil || got.Stage != "full" {
		t.Fatalf("new token: %+v, %v", got, err)
	}

	tok3, sudo, err := s.Rotate(ctx, full, Rotation{Stage: principal.StageFull, SudoUntil: 123})
	if err != nil || sudo.CsrfToken != full.CsrfToken || sudo.SudoUntil != 123 {
		t.Fatalf("sudo rotation = %+v, %v", sudo, err)
	}
	if err := s.Revoke(ctx, sudo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, tok3); !errors.Is(err, ErrNoSession) {
		t.Fatal("revoked session must be invalid")
	}
	if _, _, err := s.Rotate(ctx, sudo, Rotation{Stage: principal.StageFull}); !errors.Is(err, ErrNoSession) {
		t.Fatalf("rotating a revoked session must fail with ErrNoSession, got %v", err)
	}
}

func TestSessionCookies(t *testing.T) {
	rec := httptest.NewRecorder()
	setSessionCookie(rec, "tok")
	h := rec.Header().Get("Set-Cookie")
	for _, part := range []string{"__Host-sid=tok", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(h, part) {
			t.Errorf("Set-Cookie %q missing %q", h, part)
		}
	}
	if strings.Contains(h, "Domain") || strings.Contains(h, "Max-Age") {
		t.Errorf("Set-Cookie %q must not contain Domain or Max-Age", h)
	}
	rec = httptest.NewRecorder()
	clearSessionCookie(rec)
	if h := rec.Header().Get("Set-Cookie"); !strings.Contains(h, "__Host-sid=;") || !strings.Contains(h, "Max-Age=0") {
		t.Errorf("clear cookie = %q", h)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/auth/ -run 'TestSession|TestPreAuth|TestIdle|TestAbsolute|TestRotate'` → FAIL (`undefined: Sessions`).

- [ ] **Step 3: `internal/auth/session.go`**

```go
package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/netip"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

const (
	CookieName    = "__Host-sid"
	touchInterval = time.Minute
)

var ErrNoSession = errors.New("auth: no valid session")

// Sessions manages server-side sessions. The cookie carries a random token;
// the database stores only its SHA-256.
type Sessions struct {
	q     *store.Queries
	clock clock.Clock
	cfg   config.Session
}

func NewSessions(q *store.Queries, c clock.Clock, cfg config.Session) *Sessions {
	return &Sessions{q: q, clock: c, cfg: cfg}
}

type NewSession struct {
	UserID    int64
	Stage     principal.Stage
	IP        netip.Addr
	UserAgent string
	Country   string
}

type Rotation struct {
	Stage     principal.Stage
	NewCSRF   bool
	SudoUntil int64
}

func (s *Sessions) expiresAt(stage principal.Stage, createdAt int64) int64 {
	if stage == principal.StagePreAuth {
		return createdAt + s.cfg.PreAuth.Milliseconds()
	}
	return createdAt + s.cfg.Absolute.Milliseconds()
}

func (s *Sessions) Create(ctx context.Context, in NewSession) (string, store.Session, error) {
	tok, err := newToken()
	if err != nil {
		return "", store.Session{}, err
	}
	csrf, err := newToken()
	if err != nil {
		return "", store.Session{}, err
	}
	now := s.clock.Now().UnixMilli()
	ip := ""
	if in.IP.IsValid() {
		ip = in.IP.String()
	}
	sess, err := s.q.AuthCreateSession(ctx, store.AuthCreateSessionParams{
		TokenHash:  hashToken(tok),
		UserID:     in.UserID,
		Stage:      string(in.Stage),
		CsrfToken:  csrf,
		Ip:         ip,
		UserAgent:  in.UserAgent,
		Country:    in.Country,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  s.expiresAt(in.Stage, now),
	})
	if err != nil {
		return "", store.Session{}, err
	}
	return tok, sess, nil
}

func (s *Sessions) Lookup(ctx context.Context, token string) (store.Session, error) {
	if !validTokenFormat(token) {
		return store.Session{}, ErrNoSession
	}
	sess, err := s.q.AuthGetSessionByTokenHash(ctx, hashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Session{}, ErrNoSession
	}
	if err != nil {
		return store.Session{}, err
	}
	if !s.valid(sess) {
		return store.Session{}, ErrNoSession
	}
	return sess, nil
}

func (s *Sessions) valid(sess store.Session) bool {
	now := s.clock.Now().UnixMilli()
	if sess.RevokedAt.Valid || now >= sess.ExpiresAt {
		return false
	}
	if principal.Stage(sess.Stage) != principal.StagePreAuth && now-sess.LastSeenAt >= s.cfg.Idle.Milliseconds() {
		return false
	}
	return true
}

func (s *Sessions) Touch(ctx context.Context, sess store.Session) error {
	now := s.clock.Now().UnixMilli()
	if now-sess.LastSeenAt < touchInterval.Milliseconds() {
		return nil
	}
	return s.q.AuthTouchSession(ctx, store.AuthTouchSessionParams{LastSeenAt: now, ID: sess.ID})
}

// Rotate issues a new token for the same session (fixation protection) and
// applies the new stage / sudo deadline. Pending WebAuthn state is discarded.
func (s *Sessions) Rotate(ctx context.Context, sess store.Session, rot Rotation) (string, store.Session, error) {
	tok, err := newToken()
	if err != nil {
		return "", store.Session{}, err
	}
	csrf := sess.CsrfToken
	if rot.NewCSRF {
		if csrf, err = newToken(); err != nil {
			return "", store.Session{}, err
		}
	}
	now := s.clock.Now().UnixMilli()
	p := store.AuthRotateSessionParams{
		TokenHash:  hashToken(tok),
		Stage:      string(rot.Stage),
		CsrfToken:  csrf,
		SudoUntil:  rot.SudoUntil,
		ExpiresAt:  s.expiresAt(rot.Stage, sess.CreatedAt),
		LastSeenAt: now,
		ID:         sess.ID,
	}
	n, err := s.q.AuthRotateSession(ctx, p)
	if err != nil {
		return "", store.Session{}, err
	}
	if n != 1 {
		return "", store.Session{}, ErrNoSession
	}
	sess.TokenHash, sess.Stage, sess.CsrfToken = p.TokenHash, p.Stage, p.CsrfToken
	sess.SudoUntil, sess.ExpiresAt, sess.LastSeenAt = p.SudoUntil, p.ExpiresAt, p.LastSeenAt
	sess.WebauthnState = sql.NullString{}
	return tok, sess, nil
}

func (s *Sessions) Revoke(ctx context.Context, id int64) error {
	_, err := s.q.AuthRevokeSession(ctx, store.AuthRevokeSessionParams{
		RevokedAt: sql.NullInt64{Int64: s.clock.Now().UnixMilli(), Valid: true}, ID: id,
	})
	return err
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}
```

- [ ] **Step 4: Run** `go test ./internal/auth/ -run 'TestSession|TestPreAuth|TestIdle|TestAbsolute|TestRotate'` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/session.go internal/auth/session_test.go
git commit -m "feat(auth): add server-side sessions with idle, absolute and preauth timeouts" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Session-Loader- und CSRF-Middleware

**Files:**
- Create: `internal/auth/middleware.go`
- Test: `internal/auth/middleware_test.go`

**Interfaces:**
- Consumes: `Sessions.Lookup`, `Sessions.Touch`, `Sessions.Rotate`, `clearSessionCookie`, `ErrNoSession` (Task 4); `AuthGetUserByID` (Task 1); `principal.With`, `principal.From`, `principal.Principal{UserID, Username, Role, Stage, SessionID, SudoUntil, CSRFToken}`; `httpx.WriteError`, `httpx.Internal`, `httpx.CodeCSRF`.
- Produces:
```go
func (s *Sessions) Middleware(next http.Handler) http.Handler   // loads session → principal; invalid cookie → cleared, anonymous
func CSRF(origin string) func(http.Handler) http.Handler       // origin = cfg.Origin()
```

- [ ] **Step 1: Failing test `internal/auth/middleware_test.go`**

```go
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/principal"
)

func TestSessionMiddleware(t *testing.T) {
	s, _, clk, uid := newTestSessions(t)
	var got principal.Principal
	var ok bool
	h := s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = principal.From(r.Context())
	}))
	call := func(cookie string) *httptest.ResponseRecorder {
		got, ok = principal.Principal{}, false
		req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(""); ok || rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("request without cookie must stay anonymous without Set-Cookie")
	}

	_, sess := mustCreate(t, s, uid, principal.StageFull)
	sudoUntil := sessionStart.Add(10 * time.Minute)
	tok, sess, err := s.Rotate(context.Background(), sess, Rotation{Stage: principal.StageFull, SudoUntil: sudoUntil.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	call(tok)
	if !ok || got.UserID != uid || got.Username != "alice" || got.Role != principal.RoleUser ||
		got.Stage != principal.StageFull || got.SessionID != sess.ID || got.CSRFToken != sess.CsrfToken ||
		!got.SudoUntil.Equal(sudoUntil) {
		t.Fatalf("principal = %+v ok=%v", got, ok)
	}

	if rec := call("not-a-valid-token"); ok || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("invalid cookie must be cleared, Set-Cookie=%q", rec.Header().Get("Set-Cookie"))
	}

	clk.Advance(31 * time.Minute)
	if rec := call(tok); ok || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatal("idle-expired session must be anonymous and cleared")
	}
}

func TestCSRF(t *testing.T) {
	const origin = "http://localhost:5173"
	const token = "tok-123"
	called := false
	h := CSRF(origin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	type hdr = map[string]string
	cases := []struct {
		name, method, path string
		session            bool
		headers            hdr
		want               int
	}{
		{"GET needs nothing", http.MethodGet, "/api/auth/me", true, nil, 200},
		{"no fetch metadata and no origin", http.MethodPost, "/api/me/password", true, hdr{"X-CSRF-Token": token}, 403},
		{"cross-site", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "cross-site", "X-CSRF-Token": token}, 403},
		{"same-site is not same-origin", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "same-site", "X-CSRF-Token": token}, 403},
		{"foreign origin", http.MethodPost, "/api/me/password", true, hdr{"Origin": "https://evil.example", "X-CSRF-Token": token}, 403},
		{"same-origin but foreign Origin header", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "same-origin", "Origin": "https://evil.example", "X-CSRF-Token": token}, 403},
		{"wrong token", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "same-origin", "X-CSRF-Token": "nope"}, 403},
		{"missing token", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "same-origin"}, 403},
		{"DELETE needs token", http.MethodDelete, "/api/me/totp", true, hdr{"Sec-Fetch-Site": "same-origin"}, 403},
		{"same-origin with token", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "same-origin", "Origin": origin, "X-CSRF-Token": token}, 200},
		{"origin only with token", http.MethodPatch, "/api/me", true, hdr{"Origin": origin, "X-CSRF-Token": token}, 200},
		{"sec-fetch-site none with token", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "none", "X-CSRF-Token": token}, 200},
		{"login exempt from token", http.MethodPost, "/api/auth/login", true, hdr{"Origin": origin}, 200},
		{"login not exempt from origin", http.MethodPost, "/api/auth/login", false, hdr{"Origin": "https://evil.example"}, 403},
		{"anonymous same-origin POST", http.MethodPost, "/api/auth/logout", false, hdr{"Sec-Fetch-Site": "same-origin"}, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(tc.method, tc.path, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if tc.session {
				req = req.WithContext(principal.With(req.Context(), principal.Principal{UserID: 1, Stage: principal.StageFull, CSRFToken: token}))
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("code = %d, want %d", rec.Code, tc.want)
			}
			if tc.want == 403 {
				var body struct{ Error string }
				_ = json.Unmarshal(rec.Body.Bytes(), &body)
				if body.Error != "csrf_invalid" || called {
					t.Fatalf("error=%q called=%v", body.Error, called)
				}
			} else if !called {
				t.Fatal("next handler not called")
			}
		})
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/auth/ -run 'TestSessionMiddleware|TestCSRF'` → FAIL (`s.Middleware undefined`, `undefined: CSRF`).

- [ ] **Step 3: `internal/auth/middleware.go`**

```go
package auth

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

const (
	csrfHeader = "X-CSRF-Token"
	loginPath  = "/api/auth/login"
)

// Middleware loads the session from the cookie and stores the principal in the
// request context. Invalid or expired cookies are cleared; the request then
// continues anonymously (rbac rejects it if the route needs a session).
func (s *Sessions) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		sess, err := s.Lookup(ctx, c.Value)
		if errors.Is(err, ErrNoSession) {
			clearSessionCookie(w)
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		user, err := s.q.AuthGetUserByID(ctx, sess.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			clearSessionCookie(w)
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if err := s.Touch(ctx, sess); err != nil {
			httpx.Internal(w, r, err)
			return
		}
		p := principal.Principal{
			UserID:    user.ID,
			Username:  user.Username,
			Role:      principal.Role(user.Role),
			Stage:     principal.Stage(sess.Stage),
			SessionID: sess.ID,
			CSRFToken: sess.CsrfToken,
		}
		if sess.SudoUntil > 0 {
			p.SudoUntil = time.UnixMilli(sess.SudoUntil)
		}
		next.ServeHTTP(w, r.WithContext(principal.With(ctx, p)))
	})
}

// CSRF rejects state-changing cross-origin requests (Fetch Metadata / Origin)
// and, when a session exists, requires the synchronizer token in X-CSRF-Token.
// The login endpoint is exempt from the token check but not from the origin check.
func CSRF(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if !sameOrigin(r, origin) {
				httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeCSRF, "cross-site request rejected")
				return
			}
			if p, ok := principal.From(r.Context()); ok && r.URL.Path != loginPath {
				got := r.Header.Get(csrfHeader)
				if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(p.CSRFToken)) != 1 {
					httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeCSRF, "missing or invalid CSRF token")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func sameOrigin(r *http.Request, origin string) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	o := r.Header.Get("Origin")
	if site == "" && o == "" {
		return false
	}
	if site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if o != "" && o != origin {
		return false
	}
	return true
}
```

- [ ] **Step 4: Run** `go test ./internal/auth/ -run 'TestSessionMiddleware|TestCSRF'` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/middleware.go internal/auth/middleware_test.go
git commit -m "feat(auth): add session loader and CSRF middleware" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Handler-Kern, Login, Me, Logout

**Files:**
- Create: `internal/auth/handler.go`, `internal/auth/login.go`
- Test: `internal/auth/testenv_test.go`, `internal/auth/login_test.go`

**Interfaces:**
- Consumes: `defense.LoginGuard` (`CheckLogin`, `CheckAdminNetwork`, `RecordAttempt`), `defense.Attempt`, `defense.StagePassword`, `defense.ErrJailed`, `defense.ErrIPBlocked`, `defense.ErrAdminNetwork`, `*defense.RateLimitError`, `defense.GeoResolver`, `defense.NopGeo`, `defense.FakeGuard`; `password.Verify`, `password.DummyVerify`, `password.Hash`, `password.HashWithParams`, `password.MaxLength`; `audit.Recorder`, `audit.Entry`, `audit.ActionLogin`, `audit.ActionLogout`, `audit.ActorUser`, `audit.Result*`, `audit.MemoryRecorder`; `notify.Notifier`, `notify.Recording`; `store.LoadSecuritySettings`, `store.SaveSecuritySettings`, `store.DefaultSecuritySettings`; `rbac.Router` (`Get`/`Post`), `rbac.Public()`, `rbac.Authenticated().AllowStages(...)`, `rbac.NewRouter`; `httpx.ClientIP`, `httpx.RealIP`, `httpx.DecodeJSON`, `httpx.WriteJSON`, `httpx.WriteError`, `httpx.Internal`; `webauthn.New(*webauthn.Config) (*webauthn.WebAuthn, error)`, `protocol.AuthenticatorSelection{ResidentKey, UserVerification}`, `protocol.ResidentKeyRequirementPreferred`, `protocol.VerificationPreferred`; Tasks 1–5.
- Produces:
```go
type Deps struct {
	DB       *sql.DB
	Config   config.Panel
	Clock    clock.Clock
	Box      *secretbox.Box
	Guard    defense.LoginGuard
	Geo      defense.GeoResolver // nil → defense.NopGeo{}
	Audit    audit.Recorder
	Notifier notify.Notifier
}
func New(d Deps) (*Handler, error)
func (h *Handler) Sessions() *Sessions
func (h *Handler) Routes(api *rbac.Router)
// internal helpers used by later tasks:
type factors struct{ totp bool; passkeys, recovery int64 }
func (f factors) has2FA() bool
func (h *Handler) factors(ctx context.Context, q *store.Queries, u store.User) (factors, error)
func (h *Handler) twoFactorMandatory(ctx context.Context, q *store.Queries, u store.User) (bool, error)
func (h *Handler) current(w http.ResponseWriter, r *http.Request) (principal.Principal, store.User, bool)
func (h *Handler) writeMe(w http.ResponseWriter, r *http.Request, u store.User, sess store.Session)
func (h *Handler) guardError(w http.ResponseWriter, r *http.Request, err error)
func (h *Handler) recordAttempt(r *http.Request, username string, userID int64, stage defense.AttemptStage, success bool, reason string)
func (h *Handler) auditLog(ctx context.Context, userID int64, username, action, target string, result audit.Result, details map[string]any)
func userAgent(r *http.Request) string
func invalidCredentials(w http.ResponseWriter, r *http.Request)
func invalidCode(w http.ResponseWriter, r *http.Request)
type meResponse struct{ /* JSON schema Me */ }
```
- Test-Helfer (von allen späteren Tests genutzt): `newTestEnv(t) *testEnv` (Felder `db`, `q`, `cfg`, `clock *clock.Fake`, `guard *testGuard`, `audit *audit.MemoryRecorder`, `notes *notify.Recording`, `h`, `router`, `srv`), `(*testEnv).createUser(username, role) int64`, `enableTOTP(userID) string`, `code(secret) string`, `grantSudo(c *client)`, `enforce2FAForUsers()`, `newClient(ip) *client`, `(*client).do(method, path, body) response`, `doWith(method, path, body, mutate func(*http.Request)) response`, `login(username, pw) response`, `sessionID() int64`, `response.errCode() string`, `response.me(t) meResponse`, `(*testGuard).last() defense.Attempt`, `testPassword`, `testNow`.

- [ ] **Step 1: Test-Umgebung `internal/auth/testenv_test.go`**

```go
package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

const testPassword = "correct horse battery staple"

var testNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

var testPasswordHash = sync.OnceValue(func() string {
	h, err := password.Hash(testPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// testGuard is defense.FakeGuard plus a helper for the most recent attempt.
type testGuard struct {
	defense.FakeGuard
}

func (g *testGuard) last() defense.Attempt {
	all := g.Recorded()
	if len(all) == 0 {
		return defense.Attempt{}
	}
	return all[len(all)-1]
}

type fixedGeo string

func (g fixedGeo) Country(netip.Addr) string { return string(g) }

type denyAll struct{}

func (denyAll) HasServerPermission(context.Context, int64, int64, rbac.Permission) (bool, error) {
	return false, nil
}

type testEnv struct {
	t      *testing.T
	db     *sql.DB
	q      *store.Queries
	cfg    config.Panel
	clock  *clock.Fake
	guard  *testGuard
	audit  *audit.MemoryRecorder
	notes  *notify.Recording
	h      *Handler
	router *rbac.Router
	srv    http.Handler
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db := storetest.Open(t)
	cfg := config.DevPanel(t.TempDir())
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(bytes.Repeat([]byte{1}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(testNow)
	e := &testEnv{
		t: t, db: db, q: store.New(db), cfg: cfg, clock: clk,
		guard: &testGuard{}, audit: &audit.MemoryRecorder{}, notes: &notify.Recording{},
	}
	h, err := New(Deps{
		DB: db, Config: cfg, Clock: clk, Box: box, Guard: e.guard,
		Geo: fixedGeo("DE"), Audit: e.audit, Notifier: e.notes,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	mux := chi.NewRouter()
	mux.Use(httpx.RealIP(nil), h.Sessions().Middleware, CSRF(cfg.Origin()))
	mux.Route("/api", func(sub chi.Router) {
		e.router = rbac.NewRouter(sub, denyAll{}, clk)
		h.Routes(e.router)
	})
	e.srv = mux
	return e
}

func (e *testEnv) createUser(username, role string) int64 {
	e.t.Helper()
	id := storetest.CreateUser(e.t, e.db, username, role)
	if _, err := e.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, testPasswordHash(), id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// enableTOTP activates TOTP directly in the database and returns the secret.
func (e *testEnv) enableTOTP(userID int64) string {
	e.t.Helper()
	secret, _, err := newTOTPKey("test")
	if err != nil {
		e.t.Fatal(err)
	}
	enc, err := sealTOTPSecret(e.h.box, userID, secret)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE users SET totp_secret_enc = ?, totp_enabled = 1 WHERE id = ?`, enc, userID); err != nil {
		e.t.Fatal(err)
	}
	return secret
}

// code returns the TOTP code for the current fake-clock step.
func (e *testEnv) code(secret string) string {
	e.t.Helper()
	c, err := totpCodeAt(secret, totpStep(e.clock.Now()))
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *testEnv) grantSudo(c *client) {
	e.t.Helper()
	if _, err := e.db.Exec(`UPDATE sessions SET sudo_until = ? WHERE token_hash = ?`,
		e.clock.Now().Add(10*time.Minute).UnixMilli(), hashToken(c.cookie)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) enforce2FAForUsers() {
	e.t.Helper()
	s := store.DefaultSecuritySettings()
	s.Enforce2FAForUsers = true
	if err := store.SaveSecuritySettings(context.Background(), e.q, s, e.clock.Now()); err != nil {
		e.t.Fatal(err)
	}
}

// client simulates one browser: it keeps the session cookie and the CSRF token.
type client struct {
	env    *testEnv
	ip     string
	cookie string
	csrf   string
}

func (e *testEnv) newClient(ip string) *client { return &client{env: e, ip: ip} }

type response struct {
	code   int
	body   []byte
	header http.Header
}

func (r response) errCode() string {
	var b struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(r.body, &b)
	return b.Error
}

func (r response) me(t *testing.T) meResponse {
	t.Helper()
	var m meResponse
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("decode Me: %v (%s)", err, r.body)
	}
	return m
}

func (c *client) do(method, path string, body any) response { return c.doWith(method, path, body, nil) }

func (c *client) doWith(method, path string, body any, mutate func(*http.Request)) response {
	c.env.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.env.t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = c.ip + ":40000"
	req.Header.Set("User-Agent", "test-agent")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Origin", c.env.cfg.Origin())
		if c.csrf != "" {
			req.Header.Set("X-CSRF-Token", c.csrf)
		}
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: c.cookie})
	}
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	c.env.srv.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == CookieName {
			if ck.MaxAge < 0 || ck.Value == "" {
				c.cookie = ""
			} else {
				c.cookie = ck.Value
			}
		}
	}
	var m struct {
		CSRFToken string `json:"csrf_token"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &m) == nil && m.CSRFToken != "" {
		c.csrf = m.CSRFToken
	}
	return response{code: rec.Code, body: rec.Body.Bytes(), header: rec.Header()}
}

func (c *client) login(username, pw string) response {
	return c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": username, "password": pw})
}

func (c *client) sessionID() int64 {
	c.env.t.Helper()
	s, err := c.env.q.AuthGetSessionByTokenHash(context.Background(), hashToken(c.cookie))
	if err != nil {
		c.env.t.Fatal(err)
	}
	return s.ID
}
```

- [ ] **Step 2: Failing test `internal/auth/login_test.go`**

```go
package auth

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/password"
)

func TestLoginUserWithout2FAGetsFullSession(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	res := c.login("alice", testPassword)
	if res.code != http.StatusOK {
		t.Fatalf("code = %d body = %s", res.code, res.body)
	}
	sc := res.header.Get("Set-Cookie")
	for _, part := range []string{"__Host-sid=", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(sc, part) {
			t.Errorf("Set-Cookie %q missing %q", sc, part)
		}
	}
	m := res.me(t)
	if m.ID != uid || m.Username != "alice" || m.Role != "user" || m.Stage != "full" || m.Locale != "de" ||
		m.CSRFToken == "" || m.SecondFactors == nil || len(m.SecondFactors) != 0 || m.TOTPEnabled || m.SudoUntil != 0 {
		t.Fatalf("Me = %+v", m)
	}
	if res := c.do(http.MethodGet, "/api/auth/me", nil); res.code != 200 || res.me(t).Stage != "full" {
		t.Fatalf("GET /auth/me = %d %s", res.code, res.body)
	}
	a := e.guard.last()
	if a.Stage != defense.StagePassword || !a.Success || a.Reason != "complete" || a.UserID == nil || *a.UserID != uid ||
		a.IP.String() != "203.0.113.10" || a.UserAgent != "test-agent" || a.Username != "alice" {
		t.Fatalf("attempt = %+v", a)
	}
	if got := e.audit.Actions(); len(got) != 1 || got[0] != audit.ActionLogin {
		t.Fatalf("audit = %v", got)
	}
	var country string
	if err := e.db.QueryRow(`SELECT country FROM sessions WHERE user_id = ?`, uid).Scan(&country); err != nil || country != "DE" {
		t.Fatalf("session country = %q, %v", country, err)
	}
}

func TestLoginStages(t *testing.T) {
	cases := []struct {
		name, role    string
		totp, enforce bool
		stage         string
		factors       string
		reason        string
	}{
		{"admin without 2fa must set up", "admin", false, false, "setup", "", "complete"},
		{"user with enforcement must set up", "user", false, true, "setup", "", "complete"},
		{"user with totp", "user", true, false, "preauth", "totp", "second_factor_pending"},
		{"admin with totp", "admin", true, false, "preauth", "totp", "second_factor_pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			uid := e.createUser("bob", tc.role)
			if tc.totp {
				e.enableTOTP(uid)
			}
			if tc.enforce {
				e.enforce2FAForUsers()
			}
			res := e.newClient("203.0.113.10").login("bob", testPassword)
			if res.code != 200 {
				t.Fatalf("code = %d %s", res.code, res.body)
			}
			m := res.me(t)
			if m.Stage != tc.stage || strings.Join(m.SecondFactors, ",") != tc.factors {
				t.Fatalf("stage=%s factors=%v", m.Stage, m.SecondFactors)
			}
			if a := e.guard.last(); a.Reason != tc.reason || !a.Success {
				t.Fatalf("attempt = %+v", a)
			}
			if tc.stage == "preauth" && len(e.audit.Entries) != 0 {
				t.Fatalf("preauth must not be audited as login: %v", e.audit.Actions())
			}
		})
	}
}

func TestLoginInvalidCredentialsIdentical(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	wrong := c.login("alice", "wrong password 123")
	unknown := c.login("mallory", testPassword)
	if wrong.code != 401 || unknown.code != 401 || wrong.errCode() != "invalid_credentials" || !bytes.Equal(wrong.body, unknown.body) {
		t.Fatalf("wrong=%d %s unknown=%d %s", wrong.code, wrong.body, unknown.code, unknown.body)
	}
	if c.cookie != "" {
		t.Fatal("failed login must not set a session cookie")
	}
	att := e.guard.Recorded()
	if len(att) != 2 || att[0].Reason != "bad_password" || att[0].Success || att[0].UserID == nil ||
		att[1].Reason != "unknown_user" || att[1].UserID != nil || att[1].Username != "mallory" {
		t.Fatalf("attempts = %+v", att)
	}
	if len(e.audit.Entries) != 0 {
		t.Fatalf("failed logins must not be audited: %v", e.audit.Actions())
	}
}

func TestLoginUnusablePasswordHash(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	if _, err := e.db.Exec(`UPDATE users SET password_hash = '!' WHERE id = ?`, uid); err != nil {
		t.Fatal(err)
	}
	if res := e.newClient("203.0.113.10").login("alice", testPassword); res.code != 401 || res.errCode() != "invalid_credentials" {
		t.Fatalf("got %d %s", res.code, res.body)
	}
}

func TestLoginBadRequest(t *testing.T) {
	e := newTestEnv(t)
	c := e.newClient("203.0.113.10")
	if res := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "alice"}); res.code != 400 {
		t.Fatalf("missing password: %d", res.code)
	}
	if res := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "a", "password": "b", "x": "y"}); res.code != 400 {
		t.Fatalf("unknown field: %d", res.code)
	}
	if res := c.login(strings.Repeat("a", 65), testPassword); res.code != 400 {
		t.Fatalf("long username: %d", res.code)
	}
}

func TestLoginGuardErrors(t *testing.T) {
	cases := []struct {
		name, role      string
		loginErr, admin error
		code            int
		errCode, retry  string
	}{
		{"jailed", "user", defense.ErrJailed, nil, 403, "jailed", ""},
		{"ip blocked", "user", defense.ErrIPBlocked, nil, 403, "ip_blocked", ""},
		{"rate limited", "user", &defense.RateLimitError{RetryAfter: 1500 * time.Millisecond}, nil, 429, "rate_limited", "2"},
		{"admin outside allowlist", "admin", nil, defense.ErrAdminNetwork, 403, "forbidden", ""},
		{"admin network only applies to admins", "user", nil, defense.ErrAdminNetwork, 200, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.createUser("alice", tc.role)
			e.guard.LoginErr = tc.loginErr
			e.guard.AdminNetErr = tc.admin
			c := e.newClient("203.0.113.10")
			res := c.login("alice", testPassword)
			if res.code != tc.code || res.errCode() != tc.errCode || res.header.Get("Retry-After") != tc.retry {
				t.Fatalf("got %d %q retry=%q", res.code, res.body, res.header.Get("Retry-After"))
			}
			if tc.code != 200 && c.cookie != "" {
				t.Fatal("blocked login must not create a session")
			}
			if tc.errCode == "forbidden" && e.guard.last().Reason != "admin_network" {
				t.Fatalf("attempt = %+v", e.guard.last())
			}
		})
	}
}

func TestLoginRehashesOldHash(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	if _, err := e.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, password.HashWithParams(testPassword, 19456, 2, 1), uid); err != nil {
		t.Fatal(err)
	}
	if res := e.newClient("203.0.113.10").login("alice", testPassword); res.code != 200 {
		t.Fatalf("login = %d", res.code)
	}
	var h string
	if err := e.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, uid).Scan(&h); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("hash not upgraded: %q", h)
	}
}

func TestLoginRevokesPresentedSession(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	old := c.cookie
	if res := c.login("alice", testPassword); res.code != 200 || c.cookie == old || c.cookie == "" {
		t.Fatalf("second login = %d", res.code)
	}
	stale := e.newClient("203.0.113.10")
	stale.cookie = old
	if res := stale.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("session presented at login must be revoked, got %d", res.code)
	}
}

func TestLogout(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	old := c.cookie
	if res := c.do(http.MethodPost, "/api/auth/logout", nil); res.code != 204 || c.cookie != "" {
		t.Fatalf("logout = %d cookie=%q", res.code, c.cookie)
	}
	stale := e.newClient("203.0.113.10")
	stale.cookie = old
	if res := stale.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("after logout = %d", res.code)
	}
	if got := e.audit.Actions(); got[len(got)-1] != audit.ActionLogout {
		t.Fatalf("audit = %v", got)
	}
}

// New-device detection and EventNewDeviceLogin belong to stream B (inside
// LoginGuard.RecordAttempt). Stream A must report exactly one successful
// "complete" attempt per completed login and must not notify by itself.
func TestCompleteAttemptRecordedWithoutNotification(t *testing.T) {
	for _, role := range []string{"user", "admin"} { // user → full, admin without 2FA → setup
		t.Run(role, func(t *testing.T) {
			e := newTestEnv(t)
			uid := e.createUser("alice", role)
			c := e.newClient("203.0.113.10")
			c.login("alice", "wrong password 123")
			if res := c.login("alice", testPassword); res.code != 200 {
				t.Fatalf("login = %d %s", res.code, res.body)
			}
			var complete []defense.Attempt
			for _, a := range e.guard.Recorded() {
				if a.Success && a.Reason == "complete" {
					complete = append(complete, a)
				}
			}
			if len(complete) != 1 || complete[0].Stage != defense.StagePassword || complete[0].UserID == nil ||
				*complete[0].UserID != uid || complete[0].IP.String() != "203.0.113.10" || complete[0].Username != "alice" {
				t.Fatalf("complete attempts = %+v", complete)
			}
			if len(e.notes.Events) != 0 {
				t.Fatalf("stream A must not send notifications on login: %v", e.notes.Types())
			}
		})
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/auth/` → FAIL (`undefined: New`, `undefined: Deps`, `undefined: meResponse`).

- [ ] **Step 4: `internal/auth/handler.go`**

```go
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Login attempt reasons (contract with stream B, stored in login_attempts.reason).
const (
	reasonComplete            = "complete"
	reasonSecondFactorPending = "second_factor_pending"
	reasonSudo                = "sudo"
	reasonUnknownUser         = "unknown_user"
	reasonBadPassword         = "bad_password"
	reasonAdminNetwork        = "admin_network"
	reasonBadCode             = "bad_code"
	reasonReplayedCode        = "replayed_code"
	reasonMethodNotEnabled    = "method_not_enabled"
	reasonBadAssertion        = "bad_assertion"
	reasonCloneWarning        = "clone_warning"
	reasonNoCeremony          = "no_ceremony"
	reasonBadCurrentPassword  = "bad_current_password"

	maxUserAgent = 512
)

type Deps struct {
	DB       *sql.DB
	Config   config.Panel
	Clock    clock.Clock
	Box      *secretbox.Box
	Guard    defense.LoginGuard
	Geo      defense.GeoResolver
	Audit    audit.Recorder
	Notifier notify.Notifier
}

type Handler struct {
	db       *sql.DB
	q        *store.Queries
	cfg      config.Panel
	clock    clock.Clock
	box      *secretbox.Box
	guard    defense.LoginGuard
	geo      defense.GeoResolver
	audit    audit.Recorder
	notifier notify.Notifier
	sessions *Sessions
	wa       *webauthn.WebAuthn
}

func New(d Deps) (*Handler, error) {
	if d.DB == nil || d.Clock == nil || d.Box == nil || d.Guard == nil || d.Audit == nil || d.Notifier == nil {
		return nil, errors.New("auth: DB, Clock, Box, Guard, Audit and Notifier are required")
	}
	if d.Geo == nil {
		d.Geo = defense.NopGeo{}
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          d.Config.RPID(),
		RPDisplayName: "mc-panel",
		RPOrigins:     []string{d.Config.Origin()},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("auth: webauthn: %w", err)
	}
	q := store.New(d.DB)
	return &Handler{
		db: d.DB, q: q, cfg: d.Config, clock: d.Clock, box: d.Box, guard: d.Guard, geo: d.Geo,
		audit: d.Audit, notifier: d.Notifier, sessions: NewSessions(q, d.Clock, d.Config.Session), wa: wa,
	}, nil
}

// Sessions exposes the session manager (its Middleware is mounted globally by Phase 2).
func (h *Handler) Sessions() *Sessions { return h.sessions }

// Routes registers all stream-A endpoints with the rules from the OpenAPI table.
func (h *Handler) Routes(api *rbac.Router) {
	anyStage := rbac.Authenticated().AllowStages(principal.StagePreAuth, principal.StageSetup, principal.StageFull)
	api.Post("/auth/login", rbac.Public(), h.login)
	api.Get("/auth/me", anyStage, h.getMe)
	api.Post("/auth/logout", anyStage, h.logout)
}

type factors struct {
	totp     bool
	passkeys int64
	recovery int64
}

func (f factors) has2FA() bool { return f.totp || f.passkeys > 0 }

// secondFactors lists the methods usable in the preauth stage.
func (f factors) secondFactors() []string {
	out := []string{}
	if f.totp {
		out = append(out, "totp")
	}
	if f.passkeys > 0 {
		out = append(out, "webauthn")
	}
	if f.has2FA() && f.recovery > 0 {
		out = append(out, "recovery")
	}
	return out
}

func (h *Handler) factors(ctx context.Context, q *store.Queries, u store.User) (factors, error) {
	pk, err := q.AuthCountCredentials(ctx, u.ID)
	if err != nil {
		return factors{}, err
	}
	rc, err := q.AuthCountUnusedRecoveryCodes(ctx, u.ID)
	if err != nil {
		return factors{}, err
	}
	return factors{totp: u.TotpEnabled == 1, passkeys: pk, recovery: rc}, nil
}

// twoFactorMandatory: admins always; users when enforcement is switched on.
func (h *Handler) twoFactorMandatory(ctx context.Context, q *store.Queries, u store.User) (bool, error) {
	if principal.Role(u.Role) == principal.RoleAdmin {
		return true, nil
	}
	s, err := store.LoadSecuritySettings(ctx, q)
	if err != nil {
		return false, err
	}
	return s.Enforce2FAForUsers, nil
}

type meResponse struct {
	ID                     int64    `json:"id"`
	Username               string   `json:"username"`
	Role                   string   `json:"role"`
	Stage                  string   `json:"stage"`
	Locale                 string   `json:"locale"`
	TOTPEnabled            bool     `json:"totp_enabled"`
	PasskeyCount           int64    `json:"passkey_count"`
	RecoveryCodesRemaining int64    `json:"recovery_codes_remaining"`
	SecondFactors          []string `json:"second_factors"`
	SudoUntil              int64    `json:"sudo_until"`
	CSRFToken              string   `json:"csrf_token"`
}

func (h *Handler) writeMe(w http.ResponseWriter, r *http.Request, u store.User, sess store.Session) {
	f, err := h.factors(r.Context(), h.q, u)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	sudo := sess.SudoUntil
	if sudo <= h.clock.Now().UnixMilli() {
		sudo = 0
	}
	httpx.WriteJSON(w, http.StatusOK, meResponse{
		ID: u.ID, Username: u.Username, Role: u.Role, Stage: sess.Stage, Locale: u.Locale,
		TOTPEnabled: f.totp, PasskeyCount: f.passkeys, RecoveryCodesRemaining: f.recovery,
		SecondFactors: f.secondFactors(), SudoUntil: sudo, CSRFToken: sess.CsrfToken,
	})
}

// current returns the principal and the fresh user row; writes 401/500 itself.
func (h *Handler) current(w http.ResponseWriter, r *http.Request) (principal.Principal, store.User, bool) {
	p, ok := principal.From(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
		return p, store.User{}, false
	}
	u, err := h.q.AuthGetUserByID(r.Context(), p.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
		return p, store.User{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return p, store.User{}, false
	}
	return p, u, true
}

func (h *Handler) guardError(w http.ResponseWriter, r *http.Request, err error) {
	var rl *defense.RateLimitError
	switch {
	case errors.As(err, &rl):
		secs := int64(math.Ceil(rl.RetryAfter.Seconds()))
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many attempts, try again later")
	case errors.Is(err, defense.ErrJailed):
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeJailed, "access temporarily blocked")
	case errors.Is(err, defense.ErrIPBlocked):
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeIPBlocked, "access from this address is not allowed")
	case errors.Is(err, defense.ErrAdminNetwork):
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "login not allowed")
	default:
		httpx.Internal(w, r, err)
	}
}

func (h *Handler) recordAttempt(r *http.Request, username string, userID int64, stage defense.AttemptStage, success bool, reason string) {
	a := defense.Attempt{
		IP: httpx.ClientIP(r.Context()), Username: username, Stage: stage,
		Success: success, Reason: reason, UserAgent: userAgent(r),
	}
	if userID > 0 {
		id := userID
		a.UserID = &id
	}
	if err := h.guard.RecordAttempt(r.Context(), a); err != nil {
		slog.ErrorContext(r.Context(), "record login attempt", "err", err, "stage", stage)
	}
}

// auditLog must never be called while a DB transaction of this package is open.
func (h *Handler) auditLog(ctx context.Context, userID int64, username, action, target string, result audit.Result, details map[string]any) {
	id := userID
	e := audit.Entry{
		Action: action, Target: target, Result: result, Details: details,
		ActorType: audit.ActorUser, ActorID: &id, ActorName: username,
	}
	if ip := httpx.ClientIP(ctx); ip.IsValid() {
		e.IP = ip.String()
	}
	if err := h.audit.Record(ctx, e); err != nil {
		slog.ErrorContext(ctx, "audit record", "err", err, "action", action)
	}
}

func userAgent(r *http.Request) string {
	ua := r.UserAgent()
	if len(ua) > maxUserAgent {
		ua = ua[:maxUserAgent]
	}
	return ua
}

func invalidCredentials(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeInvalidCredentials, "invalid username or password")
}

func invalidCode(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeInvalidCode, "invalid code")
}
```

- [ ] **Step 5: `internal/auth/login.go`**

```go
package auth

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

const maxUsernameLen = 64

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" || utf8.RuneCountInString(req.Username) > maxUsernameLen ||
		req.Password == "" || utf8.RuneCountInString(req.Password) > password.MaxLength {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "username and password are required")
		return
	}
	ctx := r.Context()
	ip := httpx.ClientIP(ctx)
	if err := h.guard.CheckLogin(ctx, ip, req.Username); err != nil {
		h.guardError(w, r, err)
		return
	}
	user, err := h.q.AuthGetUserByUsername(ctx, req.Username)
	if errors.Is(err, sql.ErrNoRows) {
		password.DummyVerify(req.Password)
		h.recordAttempt(r, req.Username, 0, defense.StagePassword, false, reasonUnknownUser)
		invalidCredentials(w, r)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	ok, rehash, err := password.Verify(req.Password, user.PasswordHash)
	if err != nil {
		// Unusable hash (e.g. "!" from user creation without password): equalise timing.
		password.DummyVerify(req.Password)
		ok = false
	}
	if !ok {
		h.recordAttempt(r, user.Username, user.ID, defense.StagePassword, false, reasonBadPassword)
		invalidCredentials(w, r)
		return
	}
	if principal.Role(user.Role) == principal.RoleAdmin {
		if err := h.guard.CheckAdminNetwork(ctx, ip); err != nil {
			if errors.Is(err, defense.ErrAdminNetwork) {
				h.recordAttempt(r, user.Username, user.ID, defense.StagePassword, false, reasonAdminNetwork)
			}
			h.guardError(w, r, err)
			return
		}
	}
	if rehash {
		h.rehashPassword(r, user, req.Password)
	}
	f, err := h.factors(ctx, h.q, user)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	stage := principal.StagePreAuth
	if !f.has2FA() {
		mandatory, err := h.twoFactorMandatory(ctx, h.q, user)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		stage = principal.StageFull
		if mandatory {
			stage = principal.StageSetup
		}
	}
	// Session fixation: never keep a session that was presented with the login request.
	if old, ok := principal.From(ctx); ok {
		if err := h.sessions.Revoke(ctx, old.SessionID); err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}
	reason := reasonSecondFactorPending
	if stage != principal.StagePreAuth {
		reason = reasonComplete // stream B derives new-device notifications from this attempt
	}
	h.recordAttempt(r, user.Username, user.ID, defense.StagePassword, true, reason)
	tok, sess, err := h.sessions.Create(ctx, NewSession{
		UserID: user.ID, Stage: stage, IP: ip, UserAgent: userAgent(r), Country: h.geo.Country(ip),
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	setSessionCookie(w, tok)
	if stage != principal.StagePreAuth {
		h.auditLog(ctx, user.ID, user.Username, audit.ActionLogin, user.Username, audit.ResultSuccess,
			map[string]any{"method": "password", "stage": string(stage)})
	}
	h.writeMe(w, r, user, sess)
}

func (h *Handler) rehashPassword(r *http.Request, u store.User, pw string) {
	hash, err := password.Hash(pw)
	if err == nil {
		err = h.q.AuthRehashPassword(r.Context(), store.AuthRehashPasswordParams{
			PasswordHash: hash, Now: h.clock.Now().UnixMilli(), ID: u.ID,
		})
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "rehash password", "err", err, "user_id", u.ID)
	}
}

func (h *Handler) getMe(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	sess, err := h.q.AuthGetSessionByID(r.Context(), p.SessionID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.writeMe(w, r, user, sess)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := principal.From(ctx) // guaranteed by the rbac rule
	if err := h.sessions.Revoke(ctx, p.SessionID); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	clearSessionCookie(w)
	h.auditLog(ctx, p.UserID, p.Username, audit.ActionLogout, p.Username, audit.ResultSuccess, nil)
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 6: Run** `go test ./internal/auth/` → PASS (alle bisherigen Tests).

- [ ] **Step 7: Commit**

```bash
git add internal/auth/handler.go internal/auth/login.go internal/auth/testenv_test.go internal/auth/login_test.go
git commit -m "feat(auth): add login with stages, me and logout" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Zweiter Faktor beim Login – TOTP und Recovery-Code

**Files:**
- Create: `internal/auth/twofa.go`
- Modify: `internal/auth/handler.go` (`Routes` ergänzen)
- Test: `internal/auth/twofa_test.go`

**Interfaces:**
- Consumes: `defense.LoginGuard.CheckSecondFactor(ctx, ip, key)` mit `key = strconv.FormatInt(sessionID, 10)`, `defense.StageTOTP`, `defense.StageRecovery`; `audit.ActionRecoveryUsed`, `audit.ActionLogin`; `rbac.Authenticated().AllowStages(principal.StagePreAuth)`; Tasks 1–6 (`matchTOTP`, `openTOTPSecret`, `hashRecoveryCode`, `Sessions.Rotate`, `recordAttempt`, `writeMe`, `current`).
- Produces:
```go
func decodeCode(w http.ResponseWriter, r *http.Request) (string, bool)       // CodeRequest, max 32 bytes
func secondFactorKey(p principal.Principal) string
func (h *Handler) consumeTOTP(ctx context.Context, u store.User, code string) (reason string, err error) // shared by login + sudo
func (h *Handler) completeSecondFactor(w http.ResponseWriter, r *http.Request, p principal.Principal, u store.User, stage defense.AttemptStage, method string)
// test helpers (twofa_test.go):
func (e *testEnv) preauthClient(username, role, ip string) (*client, int64, string)  // user with TOTP, stage preauth
func (e *testEnv) fullClient(username, role, ip string) (*client, int64, string)     // completed TOTP login; current step consumed
func hasAction(e *testEnv, action string) bool
func wrongCode(code string) string
```

- [ ] **Step 1: Failing test `internal/auth/twofa_test.go`**

```go
package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/store"
)

func (e *testEnv) preauthClient(username, role, ip string) (*client, int64, string) {
	e.t.Helper()
	uid := e.createUser(username, role)
	secret := e.enableTOTP(uid)
	c := e.newClient(ip)
	if res := c.login(username, testPassword); res.code != 200 || res.me(e.t).Stage != "preauth" {
		e.t.Fatalf("login = %d %s", res.code, res.body)
	}
	return c, uid, secret
}

// fullClient completes a TOTP login. The current TOTP step is consumed afterwards;
// advance the clock by 30 s before using the next code.
func (e *testEnv) fullClient(username, role, ip string) (*client, int64, string) {
	e.t.Helper()
	c, uid, secret := e.preauthClient(username, role, ip)
	if res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)}); res.code != 200 {
		e.t.Fatalf("2fa = %d %s", res.code, res.body)
	}
	return c, uid, secret
}

func hasAction(e *testEnv, action string) bool {
	for _, a := range e.audit.Actions() {
		if a == action {
			return true
		}
	}
	return false
}

// wrongCode changes the last digit of a valid code.
func wrongCode(code string) string {
	last := (code[5]-'0'+1)%10 + '0'
	return code[:5] + string(rune(last))
}

func TestTOTPLoginFlow(t *testing.T) {
	e := newTestEnv(t)
	c, uid, secret := e.preauthClient("alice", "admin", "203.0.113.10")
	preCookie, preCSRF := c.cookie, c.csrf
	if res := c.do(http.MethodGet, "/api/auth/me", nil); res.code != 200 || res.me(t).Stage != "preauth" {
		t.Fatalf("me in preauth = %d %s", res.code, res.body)
	}
	if len(e.notes.Types()) != 0 {
		t.Fatalf("preauth must not notify: %v", e.notes.Types())
	}

	bad := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": wrongCode(e.code(secret))})
	if bad.code != 401 || bad.errCode() != "invalid_code" {
		t.Fatalf("wrong code = %d %s", bad.code, bad.body)
	}
	if a := e.guard.last(); a.Stage != defense.StageTOTP || a.Success || a.Reason != "bad_code" {
		t.Fatalf("attempt = %+v", a)
	}

	res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)})
	if res.code != 200 {
		t.Fatalf("2fa = %d %s", res.code, res.body)
	}
	if m := res.me(t); m.Stage != "full" || !m.TOTPEnabled || m.ID != uid {
		t.Fatalf("Me = %+v", m)
	}
	if c.cookie == preCookie || c.csrf == preCSRF {
		t.Fatal("token and CSRF token must rotate on preauth → full")
	}
	stale := e.newClient("203.0.113.10")
	stale.cookie = preCookie
	if res := stale.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("pre-auth token must be invalid after rotation, got %d", res.code)
	}
	if a := e.guard.last(); a.Stage != defense.StageTOTP || !a.Success || a.Reason != "complete" || a.UserID == nil || *a.UserID != uid {
		t.Fatalf("attempt = %+v", a)
	}
	if !hasAction(e, audit.ActionLogin) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	if got := e.notes.Types(); len(got) != 0 {
		t.Fatalf("stream A must not notify on login (new-device events come from stream B): %v", got)
	}
	e.clock.Advance(30 * time.Second)
	if res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)}); res.code != 401 {
		t.Fatalf("2fa endpoint must reject full sessions, got %d", res.code)
	}
}

func TestTOTPReplayRejectedOnLogin(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("alice", "user", "203.0.113.10")
	code := e.code(secret)
	if res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": code}); res.code != 200 {
		t.Fatalf("first use = %d", res.code)
	}
	c2 := e.newClient("203.0.113.10")
	c2.login("alice", testPassword)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": code}); res.code != 401 || res.errCode() != "invalid_code" {
		t.Fatalf("replay = %d %s", res.code, res.body)
	}
	if a := e.guard.last(); a.Reason != "replayed_code" {
		t.Fatalf("attempt = %+v", a)
	}
	e.clock.Advance(30 * time.Second)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)}); res.code != 200 {
		t.Fatalf("next step = %d %s", res.code, res.body)
	}
	older, err := totpCodeAt(secret, totpStep(e.clock.Now())-1) // still inside the ±1 window, but already superseded
	if err != nil {
		t.Fatal(err)
	}
	c3 := e.newClient("203.0.113.10")
	c3.login("alice", testPassword)
	if res := c3.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": older}); res.code != 401 || e.guard.last().Reason != "replayed_code" {
		t.Fatalf("older step must be rejected: %d %+v", res.code, e.guard.last())
	}
}

func TestPreAuthSessionExpires(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("alice", "user", "203.0.113.10")
	e.clock.Advance(5 * time.Minute)
	if res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)}); res.code != 401 || c.cookie != "" {
		t.Fatalf("expired preauth = %d cookie=%q", res.code, c.cookie)
	}
}

func TestSecondFactorGuard(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("alice", "user", "203.0.113.10")
	e.guard.SecondFactorErr = &defense.RateLimitError{RetryAfter: 30 * time.Second}
	res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)})
	if res.code != 429 || res.errCode() != "rate_limited" || res.header.Get("Retry-After") != "30" {
		t.Fatalf("rate limited = %d %s %q", res.code, res.body, res.header.Get("Retry-After"))
	}
	if e.guard.last().Stage != defense.StagePassword {
		t.Fatal("a blocked 2FA request must not be verified or recorded")
	}
	e.guard.SecondFactorErr = defense.ErrJailed
	if res := c.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "abcde-fghjk"}); res.code != 403 || res.errCode() != "jailed" {
		t.Fatalf("jailed = %d %s", res.code, res.body)
	}
}

func TestRecoveryCodeLogin(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	c, uid, _ := e.preauthClient("alice", "user", "203.0.113.10")
	for _, code := range []string{"abcde-fghjk", "mnpqr-stuvw"} {
		if err := e.q.AuthInsertRecoveryCode(ctx, store.AuthInsertRecoveryCodeParams{UserID: uid, CodeHash: hashRecoveryCode(code), CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); strings.Join(m.SecondFactors, ",") != "totp,recovery" || m.RecoveryCodesRemaining != 2 {
		t.Fatalf("Me = %+v", m)
	}
	if res := c.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "zzzzz-zzzzz"}); res.code != 401 || res.errCode() != "invalid_code" {
		t.Fatalf("wrong recovery code = %d %s", res.code, res.body)
	}
	if a := e.guard.last(); a.Stage != defense.StageRecovery || a.Reason != "bad_code" || a.Success {
		t.Fatalf("attempt = %+v", a)
	}
	res := c.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "ABCDE FGHJK"})
	if res.code != 200 {
		t.Fatalf("recovery login = %d %s", res.code, res.body)
	}
	if m := res.me(t); m.Stage != "full" || m.RecoveryCodesRemaining != 1 {
		t.Fatalf("Me = %+v", m)
	}
	if !hasAction(e, audit.ActionRecoveryUsed) || !hasAction(e, audit.ActionLogin) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	c2 := e.newClient("203.0.113.10")
	c2.login("alice", testPassword)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "abcde-fghjk"}); res.code != 401 {
		t.Fatalf("recovery code must be one-time, got %d", res.code)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/auth/ -run 'TestTOTPLoginFlow|TestTOTPReplay|TestPreAuthSessionExpires|TestSecondFactorGuard|TestRecoveryCodeLogin'` → FAIL (`404`/`405` für `/api/auth/2fa/*`, da Routen fehlen).

- [ ] **Step 3: `internal/auth/twofa.go`**

```go
package auth

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

const maxCodeLen = 32

type codeRequest struct {
	Code string `json:"code"`
}

func decodeCode(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req codeRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return "", false
	}
	if req.Code == "" || len(req.Code) > maxCodeLen {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "code is required")
		return "", false
	}
	return req.Code, true
}

// secondFactorKey identifies the session for the 2FA rate limit (stream B adds the IP).
func secondFactorKey(p principal.Principal) string { return strconv.FormatInt(p.SessionID, 10) }

// consumeTOTP verifies code and atomically marks its time step as used. The step
// counter is shared by login, sudo and activation, so a code never works twice.
// Returns "" on success, otherwise the failure reason.
func (h *Handler) consumeTOTP(ctx context.Context, u store.User, code string) (string, error) {
	if u.TotpEnabled != 1 || len(u.TotpSecretEnc) == 0 {
		return reasonMethodNotEnabled, nil
	}
	secret, err := openTOTPSecret(h.box, u.ID, u.TotpSecretEnc)
	if err != nil {
		return "", err
	}
	step, ok := matchTOTP(secret, code, h.clock.Now())
	if !ok {
		return reasonBadCode, nil
	}
	if step <= u.TotpLastStep {
		return reasonReplayedCode, nil
	}
	n, err := h.q.AuthAdvanceTOTPStep(ctx, store.AuthAdvanceTOTPStepParams{Step: step, ID: u.ID})
	if err != nil {
		return "", err
	}
	if n != 1 {
		return reasonReplayedCode, nil // a concurrent request consumed this step first
	}
	return "", nil
}

func (h *Handler) verifyTOTP(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	code, ok := decodeCode(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.guard.CheckSecondFactor(ctx, httpx.ClientIP(ctx), secondFactorKey(p)); err != nil {
		h.guardError(w, r, err)
		return
	}
	reason, err := h.consumeTOTP(ctx, user, code)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if reason != "" {
		h.recordAttempt(r, user.Username, user.ID, defense.StageTOTP, false, reason)
		invalidCode(w, r)
		return
	}
	h.completeSecondFactor(w, r, p, user, defense.StageTOTP, "totp")
}

func (h *Handler) verifyRecovery(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	code, ok := decodeCode(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.guard.CheckSecondFactor(ctx, httpx.ClientIP(ctx), secondFactorKey(p)); err != nil {
		h.guardError(w, r, err)
		return
	}
	n, err := h.q.AuthUseRecoveryCode(ctx, store.AuthUseRecoveryCodeParams{
		UsedAt: sql.NullInt64{Int64: h.clock.Now().UnixMilli(), Valid: true}, UserID: user.ID, CodeHash: hashRecoveryCode(code),
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n != 1 {
		h.recordAttempt(r, user.Username, user.ID, defense.StageRecovery, false, reasonBadCode)
		invalidCode(w, r)
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionRecoveryUsed, user.Username, audit.ResultSuccess, nil)
	h.completeSecondFactor(w, r, p, user, defense.StageRecovery, "recovery")
}

// completeSecondFactor promotes a preauth session to full: "complete" attempt (stream B
// derives new-device notifications from it), token + CSRF rotation, audit, Me response.
func (h *Handler) completeSecondFactor(w http.ResponseWriter, r *http.Request, p principal.Principal, u store.User, stage defense.AttemptStage, method string) {
	ctx := r.Context()
	h.recordAttempt(r, u.Username, u.ID, stage, true, reasonComplete)
	sess, err := h.q.AuthGetSessionByID(ctx, p.SessionID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	tok, sess, err := h.sessions.Rotate(ctx, sess, Rotation{Stage: principal.StageFull, NewCSRF: true})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	setSessionCookie(w, tok)
	h.auditLog(ctx, u.ID, u.Username, audit.ActionLogin, u.Username, audit.ResultSuccess, map[string]any{"method": method})
	h.writeMe(w, r, u, sess)
}
```

- [ ] **Step 4: Routen ergänzen** – in `Routes` (`internal/auth/handler.go`) direkt nach `api.Post("/auth/logout", anyStage, h.logout)`:

```go
	preAuth := rbac.Authenticated().AllowStages(principal.StagePreAuth)
	api.Post("/auth/2fa/totp", preAuth, h.verifyTOTP)
	api.Post("/auth/2fa/recovery", preAuth, h.verifyRecovery)
```

- [ ] **Step 5: Run** `go test ./internal/auth/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/twofa.go internal/auth/twofa_test.go internal/auth/handler.go
git commit -m "feat(auth): add TOTP and recovery code second factor with replay protection" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: TOTP einrichten, aktivieren, deaktivieren (inkl. Recovery-Ausgabe und Letzte-Methode-Schutz)

**Files:**
- Create: `internal/auth/totp_setup.go`
- Modify: `internal/auth/handler.go` (`Routes` ergänzen)
- Test: `internal/auth/totp_setup_test.go`

**Interfaces:**
- Consumes: `AuthSetPendingTOTP`, `AuthEnableTOTP`, `AuthDisableTOTP`, `AuthCountCredentials`, `AuthDeleteRecoveryCodes`, `AuthInsertRecoveryCode`, `AuthCountUnusedRecoveryCodes`, `(*store.Queries).WithTx`; `newTOTPKey`, `sealTOTPSecret`, `openTOTPSecret`, `matchTOTP`, `generateRecoveryCodes`, `hashRecoveryCode`; `audit.ActionTOTPEnable`, `audit.ActionTOTPDisable`; `notify.Event2FAChanged`; `rbac.Authenticated().AllowStages(principal.StageSetup, principal.StageFull)`, `rbac.Authenticated().WithSudo()`; `httpx.CodeSudoRequired`, `httpx.CodeConflict`, `httpx.CodeNotFound`.
- Produces:
```go
var errLastSecondFactor, errNotEnabled, errNotFound error
type totpSetupResponse struct{ Secret string `json:"secret"`; OtpauthURL string `json:"otpauth_url"` }
type recoveryCodesResponse struct{ Codes []string `json:"codes"` }
func (h *Handler) withTx(ctx context.Context, fn func(q *store.Queries) error) error   // BEGIN IMMEDIATE (store.Open sets _txlock=immediate); never audit inside fn
func (h *Handler) requireSudoIf2FA(w http.ResponseWriter, r *http.Request, p principal.Principal, u store.User) bool
func replaceRecoveryCodes(ctx context.Context, q *store.Queries, userID int64, now int64) ([]string, error)
func issueRecoveryCodesIfNone(ctx context.Context, q *store.Queries, userID int64, now int64) ([]string, error) // [] if unused codes exist
func (h *Handler) promoteSetup(w http.ResponseWriter, r *http.Request, p principal.Principal) error           // setup → full, rotates token, keeps CSRF
func (h *Handler) factorRemovalError(w http.ResponseWriter, r *http.Request, err error) bool
func (h *Handler) notify2FAChanged(ctx context.Context, u store.User, change string)
// test helpers (totp_setup_test.go):
func (e *testEnv) insertPasskeyRow(userID int64, name string) int64
```

- [ ] **Step 1: Failing test `internal/auth/totp_setup_test.go`**

```go
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/store"
)

// insertPasskeyRow adds a passkey row directly (counts as a 2FA method; not usable for login).
func (e *testEnv) insertPasskeyRow(userID int64, name string) int64 {
	e.t.Helper()
	credID, err := newToken()
	if err != nil {
		e.t.Fatal(err)
	}
	res, err := e.db.Exec(`INSERT INTO webauthn_credentials (user_id, credential_id, name, credential_json, created_at)
		VALUES (?, ?, ?, '{}', 0)`, userID, []byte(credID), name)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestAdminSetupFlowWithTOTP(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("root", "admin")
	c := e.newClient("203.0.113.10")
	if m := c.login("root", testPassword).me(t); m.Stage != "setup" {
		t.Fatalf("stage = %s", m.Stage)
	}
	setupCookie, setupCSRF := c.cookie, c.csrf

	res := c.do(http.MethodPost, "/api/me/totp/setup", nil)
	if res.code != 200 {
		t.Fatalf("setup = %d %s", res.code, res.body)
	}
	var s totpSetupResponse
	if err := json.Unmarshal(res.body, &s); err != nil || len(s.Secret) != 32 ||
		!strings.HasPrefix(s.OtpauthURL, "otpauth://totp/") || !strings.Contains(s.OtpauthURL, "issuer=mc-panel") {
		t.Fatalf("setup response = %s (%v)", res.body, err)
	}
	var enc []byte
	var enabled int
	if err := e.db.QueryRow(`SELECT totp_secret_enc, totp_enabled FROM users WHERE id = ?`, uid).Scan(&enc, &enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 || len(enc) == 0 || bytes.Contains(enc, []byte(s.Secret)) {
		t.Fatal("pending secret must be stored encrypted with totp_enabled = 0")
	}

	if res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": wrongCode(e.code(s.Secret))}); res.code != 401 || res.errCode() != "invalid_code" {
		t.Fatalf("wrong code = %d %s", res.code, res.body)
	}
	res = c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(s.Secret)})
	if res.code != 200 {
		t.Fatalf("enable = %d %s", res.code, res.body)
	}
	var rc recoveryCodesResponse
	if err := json.Unmarshal(res.body, &rc); err != nil || len(rc.Codes) != 10 {
		t.Fatalf("recovery codes = %s", res.body)
	}
	for _, code := range rc.Codes {
		if !recoveryFormat.MatchString(code) {
			t.Errorf("bad recovery code %q", code)
		}
	}
	if c.cookie == setupCookie || c.csrf != setupCSRF {
		t.Fatal("setup → full must rotate the session token and keep the CSRF token")
	}
	m := c.do(http.MethodGet, "/api/auth/me", nil).me(t)
	if m.Stage != "full" || !m.TOTPEnabled || m.RecoveryCodesRemaining != 10 || strings.Join(m.SecondFactors, ",") != "totp,recovery" {
		t.Fatalf("Me = %+v", m)
	}
	if !hasAction(e, audit.ActionTOTPEnable) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	if got := e.notes.Types(); got[len(got)-1] != notify.Event2FAChanged {
		t.Fatalf("notifications = %v", got)
	}
	c2 := e.newClient("203.0.113.10")
	c2.login("root", testPassword)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(s.Secret)}); res.code != 401 || e.guard.last().Reason != "replayed_code" {
		t.Fatalf("activation code must be consumed: %d", res.code)
	}
}

func TestEnableWithoutSetup(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	if res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": "123456"}); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("enable without setup = %d %s", res.code, res.body)
	}
}

func TestSetupRequiresSudoWhenUserHas2FA(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	e.insertPasskeyRow(uid, "key")
	if res := c.do(http.MethodPost, "/api/me/totp/setup", nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("setup without sudo = %d %s", res.code, res.body)
	}
	e.grantSudo(c)
	if res := c.do(http.MethodPost, "/api/me/totp/setup", nil); res.code != 200 {
		t.Fatalf("setup with sudo = %d %s", res.code, res.body)
	}
}

func TestSetupConflictWhenTOTPEnabled(t *testing.T) {
	e := newTestEnv(t)
	c, _, _ := e.fullClient("alice", "user", "203.0.113.10")
	e.grantSudo(c)
	if res := c.do(http.MethodPost, "/api/me/totp/setup", nil); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("setup while enabled = %d %s", res.code, res.body)
	}
}

func TestDisableTOTPLastFactorAdmin(t *testing.T) {
	e := newTestEnv(t)
	c, uid, _ := e.fullClient("root", "admin", "203.0.113.10")
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("without sudo = %d %s", res.code, res.body)
	}
	e.grantSudo(c)
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("last factor of admin = %d %s", res.code, res.body)
	}
	var enabled int
	if err := e.db.QueryRow(`SELECT totp_enabled FROM users WHERE id = ?`, uid).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("TOTP must stay enabled, enabled=%d err=%v", enabled, err)
	}
	e.insertPasskeyRow(uid, "backup key")
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 204 {
		t.Fatalf("with passkey left = %d %s", res.code, res.body)
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.TOTPEnabled || m.PasskeyCount != 1 {
		t.Fatalf("Me = %+v", m)
	}
	if !hasAction(e, audit.ActionTOTPDisable) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 409 {
		t.Fatalf("disable when not enabled = %d", res.code)
	}
}

func TestDisableTOTPEnforcedUser(t *testing.T) {
	for _, enforce := range []bool{false, true} {
		t.Run(fmt.Sprintf("enforce=%v", enforce), func(t *testing.T) {
			e := newTestEnv(t)
			c, uid, _ := e.fullClient("alice", "user", "203.0.113.10")
			if err := e.q.AuthInsertRecoveryCode(context.Background(), store.AuthInsertRecoveryCodeParams{
				UserID: uid, CodeHash: hashRecoveryCode("abcde-fghjk"), CreatedAt: 1,
			}); err != nil {
				t.Fatal(err)
			}
			if enforce {
				e.enforce2FAForUsers()
			}
			e.grantSudo(c)
			res := c.do(http.MethodDelete, "/api/me/totp", nil)
			if enforce {
				if res.code != 409 || res.errCode() != "conflict" {
					t.Fatalf("enforced user = %d %s", res.code, res.body)
				}
				return
			}
			if res.code != 204 {
				t.Fatalf("not enforced = %d %s", res.code, res.body)
			}
			m := c.do(http.MethodGet, "/api/auth/me", nil).me(t)
			if m.TOTPEnabled || len(m.SecondFactors) != 0 || m.RecoveryCodesRemaining != 0 {
				t.Fatalf("Me = %+v (recovery codes must be removed with the last method)", m)
			}
			var enc []byte
			if err := e.db.QueryRow(`SELECT totp_secret_enc FROM users WHERE id = ?`, uid).Scan(&enc); err != nil || enc != nil {
				t.Fatalf("secret must be removed, got %v %v", enc, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/auth/ -run 'TestAdminSetupFlow|TestEnableWithoutSetup|TestSetupRequiresSudo|TestSetupConflict|TestDisableTOTP'` → FAIL (`undefined: totpSetupResponse`).

- [ ] **Step 3: `internal/auth/totp_setup.go`**

```go
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

var (
	errLastSecondFactor = errors.New("auth: would remove the last mandatory second factor")
	errNotEnabled       = errors.New("auth: method not enabled")
	errNotFound         = errors.New("auth: not found")
)

type totpSetupResponse struct {
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauth_url"`
}

type recoveryCodesResponse struct {
	Codes []string `json:"codes"`
}

// withTx runs fn in a write transaction. store.Open sets _txlock=immediate, so the
// transaction takes the write lock at BEGIN and concurrent factor removals serialise.
// Never call h.auditLog inside fn (the audit recorder opens its own transaction).
func (h *Handler) withTx(ctx context.Context, fn func(q *store.Queries) error) error {
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

// requireSudoIf2FA: once a user has any second factor, adding another one needs sudo.
func (h *Handler) requireSudoIf2FA(w http.ResponseWriter, r *http.Request, p principal.Principal, u store.User) bool {
	f, err := h.factors(r.Context(), h.q, u)
	if err != nil {
		httpx.Internal(w, r, err)
		return false
	}
	if f.has2FA() && !h.clock.Now().Before(p.SudoUntil) {
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeSudoRequired, "re-authentication required")
		return false
	}
	return true
}

func replaceRecoveryCodes(ctx context.Context, q *store.Queries, userID int64, now int64) ([]string, error) {
	codes, err := generateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := q.AuthDeleteRecoveryCodes(ctx, userID); err != nil {
		return nil, err
	}
	for _, c := range codes {
		if err := q.AuthInsertRecoveryCode(ctx, store.AuthInsertRecoveryCodeParams{
			UserID: userID, CodeHash: hashRecoveryCode(c), CreatedAt: now,
		}); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

// issueRecoveryCodesIfNone issues a fresh set when the user has no unused codes
// (first 2FA method); otherwise it returns an empty list.
func issueRecoveryCodesIfNone(ctx context.Context, q *store.Queries, userID int64, now int64) ([]string, error) {
	n, err := q.AuthCountUnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return []string{}, nil
	}
	return replaceRecoveryCodes(ctx, q, userID, now)
}

// promoteSetup upgrades a setup-stage session to full once a 2FA method exists.
// The token rotates; the CSRF token is kept (the response is not a Me object).
func (h *Handler) promoteSetup(w http.ResponseWriter, r *http.Request, p principal.Principal) error {
	if p.Stage != principal.StageSetup {
		return nil
	}
	ctx := r.Context()
	sess, err := h.q.AuthGetSessionByID(ctx, p.SessionID)
	if err != nil {
		return err
	}
	tok, _, err := h.sessions.Rotate(ctx, sess, Rotation{Stage: principal.StageFull, SudoUntil: sess.SudoUntil})
	if err != nil {
		return err
	}
	setSessionCookie(w, tok)
	return nil
}

func (h *Handler) factorRemovalError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errLastSecondFactor):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "cannot remove the last two-factor method")
	case errors.Is(err, errNotEnabled):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "TOTP is not enabled")
	case errors.Is(err, errNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "passkey not found")
	default:
		httpx.Internal(w, r, err)
	}
	return true
}

func (h *Handler) notify2FAChanged(ctx context.Context, u store.User, change string) {
	ip := ""
	if a := httpx.ClientIP(ctx); a.IsValid() {
		ip = a.String()
	}
	h.notifier.Notify(ctx, notify.Event{
		Type: notify.Event2FAChanged, Title: "Two-factor settings changed",
		Message: fmt.Sprintf("%s: %s", u.Username, change), IP: ip, Username: u.Username, Time: h.clock.Now(),
	})
}

func (h *Handler) setupTOTP(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok || !h.requireSudoIf2FA(w, r, p, user) {
		return
	}
	if user.TotpEnabled == 1 {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "TOTP is already enabled; disable it first")
		return
	}
	secret, url, err := newTOTPKey(user.Username)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	enc, err := sealTOTPSecret(h.box, user.ID, secret)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	n, err := h.q.AuthSetPendingTOTP(r.Context(), store.AuthSetPendingTOTPParams{
		TotpSecretEnc: enc, Now: h.clock.Now().UnixMilli(), ID: user.ID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n != 1 {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "TOTP is already enabled; disable it first")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, totpSetupResponse{Secret: secret, OtpauthURL: url})
}

func (h *Handler) enableTOTP(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	code, ok := decodeCode(w, r)
	if !ok {
		return
	}
	if user.TotpEnabled == 1 || len(user.TotpSecretEnc) == 0 {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "no TOTP setup pending")
		return
	}
	ctx := r.Context()
	if err := h.guard.CheckSecondFactor(ctx, httpx.ClientIP(ctx), secondFactorKey(p)); err != nil {
		h.guardError(w, r, err)
		return
	}
	secret, err := openTOTPSecret(h.box, user.ID, user.TotpSecretEnc)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	step, matched := matchTOTP(secret, code, h.clock.Now())
	if !matched || step <= user.TotpLastStep {
		invalidCode(w, r)
		return
	}
	now := h.clock.Now().UnixMilli()
	var codes []string
	err = h.withTx(ctx, func(q *store.Queries) error {
		n, err := q.AuthEnableTOTP(ctx, store.AuthEnableTOTPParams{Step: step, Now: now, ID: user.ID})
		if err != nil {
			return err
		}
		if n != 1 {
			return errNotEnabled
		}
		codes, err = issueRecoveryCodesIfNone(ctx, q, user.ID, now)
		return err
	})
	if errors.Is(err, errNotEnabled) {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "no TOTP setup pending")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := h.promoteSetup(w, r, p); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionTOTPEnable, user.Username, audit.ResultSuccess, nil)
	h.notify2FAChanged(ctx, user, "TOTP enabled")
	httpx.WriteJSON(w, http.StatusOK, recoveryCodesResponse{Codes: codes})
}

func (h *Handler) disableTOTP(w http.ResponseWriter, r *http.Request) {
	_, user, ok := h.current(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	now := h.clock.Now().UnixMilli()
	err := h.withTx(ctx, func(q *store.Queries) error {
		u, err := q.AuthGetUserByID(ctx, user.ID)
		if err != nil {
			return err
		}
		if u.TotpEnabled != 1 {
			return errNotEnabled
		}
		passkeys, err := q.AuthCountCredentials(ctx, u.ID)
		if err != nil {
			return err
		}
		if passkeys == 0 {
			mandatory, err := h.twoFactorMandatory(ctx, q, u)
			if err != nil {
				return err
			}
			if mandatory {
				return errLastSecondFactor
			}
		}
		if _, err := q.AuthDisableTOTP(ctx, store.AuthDisableTOTPParams{Now: now, ID: u.ID}); err != nil {
			return err
		}
		if passkeys == 0 {
			return q.AuthDeleteRecoveryCodes(ctx, u.ID)
		}
		return nil
	})
	if h.factorRemovalError(w, r, err) {
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionTOTPDisable, user.Username, audit.ResultSuccess, nil)
	h.notify2FAChanged(ctx, user, "TOTP disabled")
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Routen ergänzen** – in `Routes` nach den Zeilen aus Task 7:

```go
	setupOrFull := rbac.Authenticated().AllowStages(principal.StageSetup, principal.StageFull)
	api.Post("/me/totp/setup", setupOrFull, h.setupTOTP)
	api.Post("/me/totp/enable", setupOrFull, h.enableTOTP)
	api.Delete("/me/totp", rbac.Authenticated().WithSudo(), h.disableTOTP)
```

- [ ] **Step 5: Run** `go test ./internal/auth/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/totp_setup.go internal/auth/totp_setup_test.go internal/auth/handler.go
git commit -m "feat(auth): add TOTP setup, activation and removal with last-factor protection" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Sudo per TOTP

**Files:**
- Create: `internal/auth/sudo.go`
- Modify: `internal/auth/handler.go` (`Routes` ergänzen)
- Test: `internal/auth/sudo_test.go`

**Interfaces:**
- Consumes: `consumeTOTP`, `decodeCode`, `secondFactorKey` (Task 7); `Sessions.Rotate`; `config.Panel.Session.Sudo.Duration`; `defense.StageSudo`; `audit.ActionSudo`, `audit.ResultFailure`; `rbac.Authenticated()`.
- Produces:
```go
type sudoResponse struct{ SudoUntil int64 `json:"sudo_until"` }
func (h *Handler) grantSudo(w http.ResponseWriter, r *http.Request, p principal.Principal, u store.User, method string) // rotates token, keeps stage + CSRF
func (h *Handler) sudoFailed(r *http.Request, u store.User, method, reason string)
```

- [ ] **Step 1: Failing test `internal/auth/sudo_test.go`**

```go
package auth

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
)

func TestSudoTOTPGrantsSudo(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.fullClient("root", "admin", "203.0.113.10")
	e.clock.Advance(30 * time.Second)
	before, csrf := c.cookie, c.csrf
	res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": e.code(secret)})
	if res.code != 200 {
		t.Fatalf("sudo = %d %s", res.code, res.body)
	}
	var s sudoResponse
	if err := json.Unmarshal(res.body, &s); err != nil || s.SudoUntil != e.clock.Now().Add(10*time.Minute).UnixMilli() {
		t.Fatalf("sudo response = %s", res.body)
	}
	if c.cookie == before || c.csrf != csrf {
		t.Fatal("sudo must rotate the session token and keep the CSRF token")
	}
	stale := e.newClient("203.0.113.10")
	stale.cookie = before
	if res := stale.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("pre-sudo token must be invalid, got %d", res.code)
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.SudoUntil != s.SudoUntil || m.Stage != "full" {
		t.Fatalf("Me = %+v", m)
	}
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 409 {
		t.Fatalf("sudo route must pass rbac (409 = last factor), got %d %s", res.code, res.body)
	}
	if a := e.guard.last(); a.Stage != defense.StageSudo || !a.Success || a.Reason != "sudo" {
		t.Fatalf("attempt = %+v", a)
	}
	if !hasAction(e, audit.ActionSudo) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestTOTPCodeNotReusableForSudo(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("root", "admin", "203.0.113.10")
	code := e.code(secret)
	if res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": code}); res.code != 200 {
		t.Fatalf("login = %d", res.code)
	}
	res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": code})
	if res.code != 401 || res.errCode() != "invalid_code" {
		t.Fatalf("login code reused for sudo = %d %s", res.code, res.body)
	}
	if a := e.guard.last(); a.Stage != defense.StageSudo || a.Success || a.Reason != "replayed_code" {
		t.Fatalf("attempt = %+v", a)
	}
	last := e.audit.Entries[len(e.audit.Entries)-1]
	if last.Action != audit.ActionSudo || last.Result != audit.ResultFailure {
		t.Fatalf("audit = %+v", last)
	}
	e.clock.Advance(30 * time.Second)
	sudoCode := e.code(secret)
	if res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": sudoCode}); res.code != 200 {
		t.Fatalf("fresh code = %d %s", res.code, res.body)
	}
	c2 := e.newClient("203.0.113.10")
	c2.login("root", testPassword)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": sudoCode}); res.code != 401 || e.guard.last().Reason != "replayed_code" {
		t.Fatalf("sudo code reused for login = %d", res.code)
	}
}

func TestSudoExpires(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.fullClient("root", "admin", "203.0.113.10")
	e.clock.Advance(30 * time.Second)
	if res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": e.code(secret)}); res.code != 200 {
		t.Fatalf("sudo = %d", res.code)
	}
	e.clock.Advance(10 * time.Minute)
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("expired sudo = %d %s", res.code, res.body)
	}
}

func TestSudoRequiresFullSession(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("root", "admin", "203.0.113.10")
	if res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": e.code(secret)}); res.code != 401 {
		t.Fatalf("preauth sudo = %d", res.code)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/auth/ -run 'TestSudo|TestTOTPCodeNotReusableForSudo'` → FAIL (`undefined: sudoResponse`).

- [ ] **Step 3: `internal/auth/sudo.go`**

```go
package auth

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

type sudoResponse struct {
	SudoUntil int64 `json:"sudo_until"`
}

func (h *Handler) sudoTOTP(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	code, ok := decodeCode(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.guard.CheckSecondFactor(ctx, httpx.ClientIP(ctx), secondFactorKey(p)); err != nil {
		h.guardError(w, r, err)
		return
	}
	reason, err := h.consumeTOTP(ctx, user, code)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if reason != "" {
		h.sudoFailed(r, user, "totp", reason)
		invalidCode(w, r)
		return
	}
	h.grantSudo(w, r, p, user, "totp")
}

func (h *Handler) sudoFailed(r *http.Request, u store.User, method, reason string) {
	h.recordAttempt(r, u.Username, u.ID, defense.StageSudo, false, reason)
	h.auditLog(r.Context(), u.ID, u.Username, audit.ActionSudo, u.Username, audit.ResultFailure,
		map[string]any{"method": method, "reason": reason})
}

// grantSudo sets sudo_until = now + cfg.Session.Sudo and rotates the session token.
func (h *Handler) grantSudo(w http.ResponseWriter, r *http.Request, p principal.Principal, u store.User, method string) {
	ctx := r.Context()
	sess, err := h.q.AuthGetSessionByID(ctx, p.SessionID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	until := h.clock.Now().Add(h.cfg.Session.Sudo.Duration).UnixMilli()
	tok, _, err := h.sessions.Rotate(ctx, sess, Rotation{Stage: principal.Stage(sess.Stage), SudoUntil: until})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	setSessionCookie(w, tok)
	h.recordAttempt(r, u.Username, u.ID, defense.StageSudo, true, reasonSudo)
	h.auditLog(ctx, u.ID, u.Username, audit.ActionSudo, u.Username, audit.ResultSuccess, map[string]any{"method": method})
	httpx.WriteJSON(w, http.StatusOK, sudoResponse{SudoUntil: until})
}
```

- [ ] **Step 4: Route ergänzen** – in `Routes` nach den Zeilen aus Task 8:

```go
	api.Post("/auth/sudo/totp", rbac.Authenticated(), h.sudoTOTP)
```

- [ ] **Step 5: Run** `go test ./internal/auth/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/sudo.go internal/auth/sudo_test.go internal/auth/handler.go
git commit -m "feat(auth): add sudo mode via TOTP sharing the replay counter with login" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: WebAuthn-Grundlagen und Passkey-Verwaltung

**Files:**
- Create: `internal/auth/webauthn.go`, `internal/auth/passkeys.go`
- Modify: `internal/auth/handler.go` (`Routes` ergänzen)
- Test: `internal/auth/softauthn_test.go`, `internal/auth/passkeys_test.go`

**Interfaces:**
- Consumes (go-webauthn v0.18.x, geprüft gegen pkg.go.dev): `webauthn.User` (`WebAuthnID() []byte`, `WebAuthnName() string`, `WebAuthnDisplayName() string`, `WebAuthnCredentials() []webauthn.Credential`), `webauthn.Credential` (Felder `ID []byte`, `Authenticator.SignCount uint32`, `Authenticator.CloneWarning bool`; Methode `(*Credential).Descriptor() protocol.CredentialDescriptor`), `webauthn.SessionData` (JSON-serialisierbar), `(*webauthn.WebAuthn).BeginRegistration(user webauthn.User, opts ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error)`, `webauthn.WithExclusions([]protocol.CredentialDescriptor) webauthn.RegistrationOption`, `(*webauthn.WebAuthn).CreateCredential(user webauthn.User, session webauthn.SessionData, parsed *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error)`, `protocol.ParseCredentialCreationResponseBytes(data []byte) (*protocol.ParsedCredentialCreationData, error)`. Projekt: `AuthListCredentials`, `AuthInsertCredential`, `AuthGetCredential`, `AuthDeleteCredential`, `AuthCountCredentials`, `AuthSetWebAuthnState`, `AuthTakeWebAuthnState`; `withTx`, `requireSudoIf2FA`, `issueRecoveryCodesIfNone`, `promoteSetup`, `factorRemovalError`, `notify2FAChanged`, `errNotFound`, `errLastSecondFactor` (Task 8); `audit.ActionPasskeyAdd`, `audit.ActionPasskeyRemove`; `chi.URLParam`.
- Produces:
```go
const ceremonyLogin, ceremonySudo, ceremonyRegister = "login", "sudo", "register"
const ceremonyTTL = 5 * time.Minute
var errNoCeremony error
type waUser struct{ u store.User; creds []webauthn.Credential }   // implements webauthn.User
func (h *Handler) loadWAUser(ctx context.Context, u store.User) (waUser, []store.WebauthnCredential, error)
func (h *Handler) saveCeremony(ctx context.Context, sessionID int64, kind string, data *webauthn.SessionData) error
func (h *Handler) takeCeremony(ctx context.Context, sessionID int64, kind string) (webauthn.SessionData, error) // one-shot, kind-bound, 5 min
type passkeyJSON struct{ ID int64; Name string; CreatedAt int64; LastUsedAt *int64 }  // JSON schema Passkey
type passkeyCreatedResponse struct{ Passkey passkeyJSON; RecoveryCodes []string }    // JSON schema PasskeyCreated
// test helpers:
func newSoftAuthenticator(t *testing.T, rpID, origin string) *softAuthenticator
func (a *softAuthenticator) register(options []byte) json.RawMessage
func (a *softAuthenticator) assert(options []byte) json.RawMessage
func (e *testEnv) registerPasskey(c *client, name string) (*softAuthenticator, response)
```
Version-Annahme: Falls `go list -m github.com/go-webauthn/webauthn` eine Version liefert, in der `CreateCredential`/`ValidateLogin` andere Signaturen haben, auf `FinishRegistration`/`FinishLogin` mit einem `*http.Request` ausweichen, dessen Body das rohe `credential`-JSON ist (`r2 := r.Clone(ctx); r2.Body = io.NopCloser(bytes.NewReader(raw))`).

- [ ] **Step 1: Software-Authenticator `internal/auth/softauthn_test.go`**

```go
package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"testing"
)

// softAuthenticator is a minimal software WebAuthn authenticator (ES256 key,
// "none" attestation) that drives real ceremonies through go-webauthn in tests.
type softAuthenticator struct {
	t       *testing.T
	rpID    string
	origin  string
	key     *ecdsa.PrivateKey
	credID  []byte
	counter uint32
}

var b64url = base64.RawURLEncoding

func newSoftAuthenticator(t *testing.T, rpID, origin string) *softAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return &softAuthenticator{t: t, rpID: rpID, origin: origin, key: key, credID: id}
}

func cborHead(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 1<<8:
		return []byte{major<<5 | 24, byte(n)}
	case n < 1<<16:
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	default:
		return []byte{major<<5 | 26, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
}

func cborInt(v int64) []byte {
	if v >= 0 {
		return cborHead(0, uint64(v))
	}
	return cborHead(1, uint64(-1-v))
}

func cborBytes(b []byte) []byte { return append(cborHead(2, uint64(len(b))), b...) }
func cborText(s string) []byte  { return append(cborHead(3, uint64(len(s))), s...) }

func cborMap(kv ...[]byte) []byte {
	out := cborHead(5, uint64(len(kv)/2))
	for _, x := range kv {
		out = append(out, x...)
	}
	return out
}

func (a *softAuthenticator) coseKey() []byte {
	pub, err := a.key.PublicKey.ECDH()
	if err != nil {
		a.t.Fatal(err)
	}
	raw := pub.Bytes() // 0x04 || X(32) || Y(32)
	return cborMap(
		cborInt(1), cborInt(2), // kty: EC2
		cborInt(3), cborInt(-7), // alg: ES256
		cborInt(-1), cborInt(1), // crv: P-256
		cborInt(-2), cborBytes(raw[1:33]),
		cborInt(-3), cborBytes(raw[33:65]),
	)
}

func challengeOf(t *testing.T, options []byte) string {
	t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &o); err != nil || o.PublicKey.Challenge == "" {
		t.Fatalf("options without challenge: %s (%v)", options, err)
	}
	return o.PublicKey.Challenge
}

func (a *softAuthenticator) clientData(typ, challenge string) []byte {
	b, err := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	if err != nil {
		a.t.Fatal(err)
	}
	return b
}

func (a *softAuthenticator) marshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		a.t.Fatal(err)
	}
	return b
}

// register answers navigator.credentials.create() for the given options JSON.
func (a *softAuthenticator) register(options []byte) json.RawMessage {
	a.t.Helper()
	cd := a.clientData("webauthn.create", challengeOf(a.t, options))
	rpHash := sha256.Sum256([]byte(a.rpID))
	ad := append([]byte{}, rpHash[:]...)
	ad = append(ad, 0x45) // flags: UP | UV | AT
	ad = binary.BigEndian.AppendUint32(ad, a.counter)
	ad = append(ad, make([]byte, 16)...) // AAGUID
	ad = binary.BigEndian.AppendUint16(ad, uint16(len(a.credID)))
	ad = append(ad, a.credID...)
	ad = append(ad, a.coseKey()...)
	att := cborMap(cborText("fmt"), cborText("none"), cborText("attStmt"), cborMap(), cborText("authData"), cborBytes(ad))
	return a.marshal(map[string]any{
		"id": b64url.EncodeToString(a.credID), "rawId": b64url.EncodeToString(a.credID), "type": "public-key",
		"authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64url.EncodeToString(cd),
			"attestationObject": b64url.EncodeToString(att),
			"transports":        []string{"internal"},
		},
	})
}

// assert answers navigator.credentials.get(); the sign counter increases by one per call.
func (a *softAuthenticator) assert(options []byte) json.RawMessage {
	a.t.Helper()
	cd := a.clientData("webauthn.get", challengeOf(a.t, options))
	a.counter++
	rpHash := sha256.Sum256([]byte(a.rpID))
	ad := append([]byte{}, rpHash[:]...)
	ad = append(ad, 0x05) // flags: UP | UV
	ad = binary.BigEndian.AppendUint32(ad, a.counter)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		a.t.Fatal(err)
	}
	return a.marshal(map[string]any{
		"id": b64url.EncodeToString(a.credID), "rawId": b64url.EncodeToString(a.credID), "type": "public-key",
		"authenticatorAttachment": "platform", "clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64url.EncodeToString(cd),
			"authenticatorData": b64url.EncodeToString(ad),
			"signature":         b64url.EncodeToString(sig),
		},
	})
}

// registerPasskey runs begin + finish for c with a new software authenticator.
func (e *testEnv) registerPasskey(c *client, name string) (*softAuthenticator, response) {
	e.t.Helper()
	a := newSoftAuthenticator(e.t, e.cfg.RPID(), e.cfg.Origin())
	begin := c.do(http.MethodPost, "/api/me/passkeys/begin", nil)
	if begin.code != 200 {
		e.t.Fatalf("passkey begin = %d %s", begin.code, begin.body)
	}
	return a, c.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": name, "credential": a.register(begin.body)})
}
```

- [ ] **Step 2: Failing test `internal/auth/passkeys_test.go`**

```go
package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/notify"
)

func TestPasskeyRegistrationAndList(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)

	begin := c.do(http.MethodPost, "/api/me/passkeys/begin", nil)
	if begin.code != 200 {
		t.Fatalf("begin = %d %s", begin.code, begin.body)
	}
	var opts struct {
		PublicKey struct {
			RP struct {
				ID string `json:"id"`
			} `json:"rp"`
			User struct {
				Name string `json:"name"`
			} `json:"user"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(begin.body, &opts); err != nil || opts.PublicKey.RP.ID != "localhost" || opts.PublicKey.User.Name != "alice" {
		t.Fatalf("creation options = %s", begin.body)
	}
	if res := c.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": " ", "credential": json.RawMessage(`{}`)}); res.code != 400 || res.errCode() != "validation_failed" {
		t.Fatalf("empty name = %d %s", res.code, res.body)
	}
	a := newSoftAuthenticator(t, e.cfg.RPID(), e.cfg.Origin())
	res := c.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": "  YubiKey  ", "credential": a.register(begin.body)})
	if res.code != 201 {
		t.Fatalf("finish = %d %s", res.code, res.body)
	}
	var created passkeyCreatedResponse
	if err := json.Unmarshal(res.body, &created); err != nil || created.Passkey.Name != "YubiKey" ||
		created.Passkey.LastUsedAt != nil || len(created.RecoveryCodes) != 10 {
		t.Fatalf("created = %s", res.body)
	}
	var credID []byte
	var credJSON string
	if err := e.db.QueryRow(`SELECT credential_id, credential_json FROM webauthn_credentials WHERE user_id = ?`, uid).Scan(&credID, &credJSON); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(credID, a.credID) || !strings.Contains(credJSON, "publicKey") {
		t.Fatalf("stored credential id=%x json=%s", credID, credJSON)
	}
	if res := c.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": "again", "credential": a.register(begin.body)}); res.code != 400 {
		t.Fatalf("ceremony state must be one-shot, got %d", res.code)
	}
	var keys []passkeyJSON
	if res := c.do(http.MethodGet, "/api/me/passkeys", nil); res.code != 200 || json.Unmarshal(res.body, &keys) != nil ||
		len(keys) != 1 || keys[0].ID != created.Passkey.ID {
		t.Fatalf("list = %d %s", res.code, res.body)
	}
	if !hasAction(e, audit.ActionPasskeyAdd) || !slices.Contains(e.notes.Types(), notify.Event2FAChanged) {
		t.Fatalf("audit=%v notes=%v", e.audit.Actions(), e.notes.Types())
	}

	if res := c.do(http.MethodPost, "/api/me/passkeys/begin", nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("second passkey without sudo = %d %s", res.code, res.body)
	}
	e.grantSudo(c)
	begin2 := c.do(http.MethodPost, "/api/me/passkeys/begin", nil)
	if begin2.code != 200 || !strings.Contains(string(begin2.body), b64url.EncodeToString(a.credID)) {
		t.Fatalf("existing credential must be in excludeCredentials: %s", begin2.body)
	}
	b := newSoftAuthenticator(t, e.cfg.RPID(), e.cfg.Origin())
	res = c.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": "Phone", "credential": b.register(begin2.body)})
	var created2 passkeyCreatedResponse
	if res.code != 201 || json.Unmarshal(res.body, &created2) != nil || created2.RecoveryCodes == nil || len(created2.RecoveryCodes) != 0 {
		t.Fatalf("second passkey = %d %s (recovery_codes must be [])", res.code, res.body)
	}
}

func TestAdminSetupWithPasskeyPromotesSession(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("root", "admin")
	c := e.newClient("203.0.113.10")
	c.login("root", testPassword)
	if _, res := e.registerPasskey(c, "key"); res.code != 201 {
		t.Fatalf("register = %d %s", res.code, res.body)
	}
	m := c.do(http.MethodGet, "/api/auth/me", nil).me(t)
	if m.Stage != "full" || m.PasskeyCount != 1 || strings.Join(m.SecondFactors, ",") != "webauthn,recovery" {
		t.Fatalf("Me = %+v", m)
	}
}

func TestDeleteLastPasskeyOfAdmin(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("root", "admin")
	c := e.newClient("203.0.113.10")
	c.login("root", testPassword)
	_, res := e.registerPasskey(c, "key")
	var created passkeyCreatedResponse
	if res.code != 201 || json.Unmarshal(res.body, &created) != nil {
		t.Fatalf("register = %d", res.code)
	}
	path := fmt.Sprintf("/api/me/passkeys/%d", created.Passkey.ID)
	if res := c.do(http.MethodDelete, path, nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("without sudo = %d", res.code)
	}
	e.grantSudo(c)
	if res := c.do(http.MethodDelete, path, nil); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("last passkey of admin = %d %s", res.code, res.body)
	}
	bob := e.createUser("bob", "user")
	bobKey := e.insertPasskeyRow(bob, "bob's key")
	for _, p := range []string{"/api/me/passkeys/999", "/api/me/passkeys/abc", fmt.Sprintf("/api/me/passkeys/%d", bobKey)} {
		if res := c.do(http.MethodDelete, p, nil); res.code != 404 {
			t.Fatalf("DELETE %s = %d", p, res.code)
		}
	}
	e.insertPasskeyRow(uid, "backup")
	if res := c.do(http.MethodDelete, path, nil); res.code != 204 {
		t.Fatalf("with backup key = %d %s", res.code, res.body)
	}
	var keys []passkeyJSON
	if res := c.do(http.MethodGet, "/api/me/passkeys", nil); json.Unmarshal(res.body, &keys) != nil || len(keys) != 1 || keys[0].Name != "backup" {
		t.Fatalf("list = %s", res.body)
	}
	if !hasAction(e, audit.ActionPasskeyRemove) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestDeletePasskeyOfUserRemovesRecoveryCodes(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	_, res := e.registerPasskey(c, "key")
	var created passkeyCreatedResponse
	if res.code != 201 || json.Unmarshal(res.body, &created) != nil {
		t.Fatalf("register = %d", res.code)
	}
	e.grantSudo(c)
	if res := c.do(http.MethodDelete, fmt.Sprintf("/api/me/passkeys/%d", created.Passkey.ID), nil); res.code != 204 {
		t.Fatalf("delete = %d %s", res.code, res.body)
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.PasskeyCount != 0 || m.RecoveryCodesRemaining != 0 || len(m.SecondFactors) != 0 {
		t.Fatalf("Me = %+v", m)
	}
}

func TestConcurrentFactorRemovalKeepsOne(t *testing.T) {
	e := newTestEnv(t)
	c, uid, _ := e.fullClient("root", "admin", "203.0.113.10")
	keyID := e.insertPasskeyRow(uid, "key")
	e.grantSudo(c)
	c1, c2 := *c, *c
	codes := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); codes[0] = c1.do(http.MethodDelete, "/api/me/totp", nil).code }()
	go func() {
		defer wg.Done()
		codes[1] = c2.do(http.MethodDelete, fmt.Sprintf("/api/me/passkeys/%d", keyID), nil).code
	}()
	wg.Wait()
	slices.Sort(codes)
	if codes[0] != 204 || codes[1] != 409 {
		t.Fatalf("codes = %v, want exactly one 204 and one 409", codes)
	}
	var totp, keys int
	if err := e.db.QueryRow(`SELECT totp_enabled, (SELECT count(*) FROM webauthn_credentials WHERE user_id = ?) FROM users WHERE id = ?`, uid, uid).Scan(&totp, &keys); err != nil {
		t.Fatal(err)
	}
	if totp+keys != 1 {
		t.Fatalf("admin must keep exactly one factor, totp=%d keys=%d", totp, keys)
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/auth/ -run 'TestPasskey|TestAdminSetupWithPasskey|TestDelete|TestConcurrentFactorRemoval'` → FAIL (`undefined: passkeyCreatedResponse`).

- [ ] **Step 4: `internal/auth/webauthn.go`**

```go
package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/Bernotese/mc-panel/internal/store"
)

const (
	ceremonyLogin    = "login"
	ceremonySudo     = "sudo"
	ceremonyRegister = "register"
	ceremonyTTL      = 5 * time.Minute
)

var errNoCeremony = errors.New("auth: no pending webauthn ceremony")

// waUser adapts a user row and its stored credentials to webauthn.User.
type waUser struct {
	u     store.User
	creds []webauthn.Credential
}

func (w waUser) WebAuthnID() []byte                         { return w.u.WebauthnHandle }
func (w waUser) WebAuthnName() string                       { return w.u.Username }
func (w waUser) WebAuthnDisplayName() string                { return w.u.Username }
func (w waUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

func (h *Handler) loadWAUser(ctx context.Context, u store.User) (waUser, []store.WebauthnCredential, error) {
	rows, err := h.q.AuthListCredentials(ctx, u.ID)
	if err != nil {
		return waUser{}, nil, err
	}
	creds := make([]webauthn.Credential, 0, len(rows))
	for _, row := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(row.CredentialJson), &c); err != nil {
			return waUser{}, nil, fmt.Errorf("auth: decode credential %d: %w", row.ID, err)
		}
		creds = append(creds, c)
	}
	return waUser{u: u, creds: creds}, rows, nil
}

type ceremonyState struct {
	Kind      string               `json:"kind"`
	CreatedAt int64                `json:"created_at"`
	Data      webauthn.SessionData `json:"data"`
}

func (h *Handler) saveCeremony(ctx context.Context, sessionID int64, kind string, data *webauthn.SessionData) error {
	b, err := json.Marshal(ceremonyState{Kind: kind, CreatedAt: h.clock.Now().UnixMilli(), Data: *data})
	if err != nil {
		return err
	}
	return h.q.AuthSetWebAuthnState(ctx, store.AuthSetWebAuthnStateParams{
		WebauthnState: sql.NullString{String: string(b), Valid: true}, ID: sessionID,
	})
}

// takeCeremony atomically removes the pending state (one-shot) and only returns
// it if it belongs to the expected ceremony kind and is younger than ceremonyTTL.
func (h *Handler) takeCeremony(ctx context.Context, sessionID int64, kind string) (webauthn.SessionData, error) {
	raw, err := h.q.AuthTakeWebAuthnState(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return webauthn.SessionData{}, errNoCeremony
	}
	if err != nil {
		return webauthn.SessionData{}, err
	}
	var st ceremonyState
	if err := json.Unmarshal([]byte(raw.String), &st); err != nil || st.Kind != kind {
		return webauthn.SessionData{}, errNoCeremony
	}
	if h.clock.Now().Sub(time.UnixMilli(st.CreatedAt)) > ceremonyTTL {
		return webauthn.SessionData{}, errNoCeremony
	}
	return st.Data, nil
}
```

- [ ] **Step 5: `internal/auth/passkeys.go`**

```go
package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

const maxPasskeyName = 64

type passkeyRegistrationRequest struct {
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
}

type passkeyJSON struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at"`
}

type passkeyCreatedResponse struct {
	Passkey       passkeyJSON `json:"passkey"`
	RecoveryCodes []string    `json:"recovery_codes"`
}

func toPasskeyJSON(c store.WebauthnCredential) passkeyJSON {
	p := passkeyJSON{ID: c.ID, Name: c.Name, CreatedAt: c.CreatedAt}
	if c.LastUsedAt.Valid {
		v := c.LastUsedAt.Int64
		p.LastUsedAt = &v
	}
	return p
}

func (h *Handler) beginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok || !h.requireSudoIf2FA(w, r, p, user) {
		return
	}
	ctx := r.Context()
	wu, _, err := h.loadWAUser(ctx, user)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	exclusions := make([]protocol.CredentialDescriptor, 0, len(wu.creds))
	for i := range wu.creds {
		exclusions = append(exclusions, wu.creds[i].Descriptor())
	}
	creation, data, err := h.wa.BeginRegistration(wu, webauthn.WithExclusions(exclusions))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := h.saveCeremony(ctx, p.SessionID, ceremonyRegister, data); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, creation)
}

func (h *Handler) finishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	var req passkeyRegistrationRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > maxPasskeyName || len(req.Credential) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, "name (1-64 characters) and credential are required")
		return
	}
	ctx := r.Context()
	data, err := h.takeCeremony(ctx, p.SessionID, ceremonyRegister)
	if errors.Is(err, errNoCeremony) {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "no passkey registration in progress")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Credential)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid passkey response")
		return
	}
	wu, _, err := h.loadWAUser(ctx, user)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	cred, err := h.wa.CreateCredential(wu, data, parsed)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "passkey verification failed")
		return
	}
	credJSON, err := json.Marshal(cred)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now().UnixMilli()
	var row store.WebauthnCredential
	var codes []string
	err = h.withTx(ctx, func(q *store.Queries) error {
		var err error
		row, err = q.AuthInsertCredential(ctx, store.AuthInsertCredentialParams{
			UserID: user.ID, CredentialID: cred.ID, Name: name, CredentialJson: string(credJSON), CreatedAt: now,
		})
		if err != nil {
			return err
		}
		codes, err = issueRecoveryCodesIfNone(ctx, q, user.ID, now)
		return err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := h.promoteSetup(w, r, p); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionPasskeyAdd, name, audit.ResultSuccess, map[string]any{"passkey_id": row.ID})
	h.notify2FAChanged(ctx, user, "passkey added: "+name)
	httpx.WriteJSON(w, http.StatusCreated, passkeyCreatedResponse{Passkey: toPasskeyJSON(row), RecoveryCodes: codes})
}

func (h *Handler) listPasskeys(w http.ResponseWriter, r *http.Request) {
	_, user, ok := h.current(w, r)
	if !ok {
		return
	}
	rows, err := h.q.AuthListCredentials(r.Context(), user.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]passkeyJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPasskeyJSON(row))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) deletePasskey(w http.ResponseWriter, r *http.Request) {
	_, user, ok := h.current(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "passkey not found")
		return
	}
	ctx := r.Context()
	var name string
	err = h.withTx(ctx, func(q *store.Queries) error {
		u, err := q.AuthGetUserByID(ctx, user.ID)
		if err != nil {
			return err
		}
		cred, err := q.AuthGetCredential(ctx, store.AuthGetCredentialParams{ID: id, UserID: u.ID})
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		count, err := q.AuthCountCredentials(ctx, u.ID)
		if err != nil {
			return err
		}
		remaining := count - 1
		if u.TotpEnabled == 1 {
			remaining++
		}
		if remaining == 0 {
			mandatory, err := h.twoFactorMandatory(ctx, q, u)
			if err != nil {
				return err
			}
			if mandatory {
				return errLastSecondFactor
			}
		}
		if _, err := q.AuthDeleteCredential(ctx, store.AuthDeleteCredentialParams{ID: id, UserID: u.ID}); err != nil {
			return err
		}
		name = cred.Name
		if remaining == 0 {
			return q.AuthDeleteRecoveryCodes(ctx, u.ID)
		}
		return nil
	})
	if h.factorRemovalError(w, r, err) {
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionPasskeyRemove, name, audit.ResultSuccess, map[string]any{"passkey_id": id})
	h.notify2FAChanged(ctx, user, "passkey removed: "+name)
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 6: Routen ergänzen** – in `Routes` nach der Zeile aus Task 9 (`setupOrFull` stammt aus Task 8):

```go
	api.Post("/me/passkeys/begin", setupOrFull, h.beginPasskeyRegistration)
	api.Post("/me/passkeys/finish", setupOrFull, h.finishPasskeyRegistration)
	api.Get("/me/passkeys", rbac.Authenticated(), h.listPasskeys)
	api.Delete("/me/passkeys/{id}", rbac.Authenticated().WithSudo(), h.deletePasskey)
```

- [ ] **Step 7: Run** `go test ./internal/auth/` → PASS. Schlägt nur `TestPasskeyRegistrationAndList` beim `finish` mit `400 passkey verification failed` fehl, den Fehler von `CreateCredential` temporär per `t.Logf` ausgeben (z. B. `%+v` von `*protocol.Error` mit `DevInfo`) und den Software-Authenticator korrigieren – nicht die Produktionsprüfung lockern.

- [ ] **Step 8: Commit**

```bash
git add internal/auth/webauthn.go internal/auth/passkeys.go internal/auth/softauthn_test.go internal/auth/passkeys_test.go internal/auth/handler.go
git commit -m "feat(auth): add passkey registration, listing and removal" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: WebAuthn als zweiter Faktor und für Sudo

**Files:**
- Create: `internal/auth/webauthn_login.go`
- Modify: `internal/auth/handler.go` (`Routes` ergänzen)
- Test: `internal/auth/webauthn_login_test.go`

**Interfaces:**
- Consumes: `(*webauthn.WebAuthn).BeginLogin(user webauthn.User, opts ...webauthn.LoginOption) (*protocol.CredentialAssertion, *webauthn.SessionData, error)`, `(*webauthn.WebAuthn).ValidateLogin(user webauthn.User, session webauthn.SessionData, parsed *protocol.ParsedCredentialAssertionData) (*webauthn.Credential, error)`, `protocol.ParseCredentialRequestResponseBytes(data []byte) (*protocol.ParsedCredentialAssertionData, error)` mit `parsed.RawID` und `parsed.Response.AuthenticatorData.Counter uint32`; `loadWAUser`, `saveCeremony`, `takeCeremony`, `errNoCeremony` (Task 10); `completeSecondFactor` (Task 7); `grantSudo`, `sudoFailed` (Task 9); `AuthUpdateCredentialUse`; `defense.StageWebAuthn`.
- Produces:
```go
func (h *Handler) verifyAssertion(ctx context.Context, p principal.Principal, u store.User, kind string, raw json.RawMessage) (reason string, err error)
// test helpers:
func (e *testEnv) passkeyUser(username, role string) *softAuthenticator
func (c *client) webauthnLogin(a *softAuthenticator) response
```

- [ ] **Step 1: Failing test `internal/auth/webauthn_login_test.go`**

```go
package auth

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
)

// passkeyUser creates a user with exactly one registered passkey (no TOTP).
func (e *testEnv) passkeyUser(username, role string) *softAuthenticator {
	e.t.Helper()
	e.createUser(username, role)
	c := e.newClient("203.0.113.10")
	c.login(username, testPassword)
	a, res := e.registerPasskey(c, "key")
	if res.code != 201 {
		e.t.Fatalf("register = %d %s", res.code, res.body)
	}
	return a
}

func (c *client) webauthnLogin(a *softAuthenticator) response {
	c.env.t.Helper()
	begin := c.do(http.MethodPost, "/api/auth/2fa/webauthn/begin", nil)
	if begin.code != 200 {
		c.env.t.Fatalf("begin = %d %s", begin.code, begin.body)
	}
	return c.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": a.assert(begin.body)})
}

func TestWebAuthnLogin(t *testing.T) {
	e := newTestEnv(t)
	a := e.passkeyUser("alice", "user")
	c := e.newClient("198.51.100.20")
	if m := c.login("alice", testPassword).me(t); m.Stage != "preauth" || strings.Join(m.SecondFactors, ",") != "webauthn,recovery" {
		t.Fatalf("Me = %+v", m)
	}
	pre := c.cookie
	begin := c.do(http.MethodPost, "/api/auth/2fa/webauthn/begin", nil)
	if begin.code != 200 || !strings.Contains(string(begin.body), b64url.EncodeToString(a.credID)) {
		t.Fatalf("begin = %d %s", begin.code, begin.body)
	}
	res := c.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": a.assert(begin.body)})
	if res.code != 200 || res.me(t).Stage != "full" || c.cookie == pre {
		t.Fatalf("finish = %d %s", res.code, res.body)
	}
	if att := e.guard.last(); att.Stage != defense.StageWebAuthn || !att.Success || att.Reason != "complete" {
		t.Fatalf("attempt = %+v", att)
	}
	var lastUsed sql.NullInt64
	var credJSON string
	if err := e.db.QueryRow(`SELECT last_used_at, credential_json FROM webauthn_credentials`).Scan(&lastUsed, &credJSON); err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Authenticator struct {
			SignCount uint32 `json:"signCount"`
		} `json:"authenticator"`
	}
	if err := json.Unmarshal([]byte(credJSON), &stored); err != nil || stored.Authenticator.SignCount != 1 ||
		lastUsed.Int64 != e.clock.Now().UnixMilli() {
		t.Fatalf("sign count = %d last_used = %v (%v)", stored.Authenticator.SignCount, lastUsed, err)
	}
}

func TestWebAuthnCloneDetected(t *testing.T) {
	e := newTestEnv(t)
	a := e.passkeyUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	if res := c.webauthnLogin(a); res.code != 200 {
		t.Fatalf("first login = %d %s", res.code, res.body)
	}
	a.counter = 0 // cloned authenticator replays an old counter value
	c2 := e.newClient("203.0.113.10")
	c2.login("alice", testPassword)
	if res := c2.webauthnLogin(a); res.code != 401 || res.errCode() != "invalid_code" || e.guard.last().Reason != "clone_warning" {
		t.Fatalf("clone = %d %s %+v", res.code, res.body, e.guard.last())
	}
}

func TestWebAuthnCeremonyIsOneShotAndKindBound(t *testing.T) {
	e := newTestEnv(t)
	a := e.passkeyUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	begin := c.do(http.MethodPost, "/api/auth/2fa/webauthn/begin", nil)
	assertion := a.assert(begin.body)
	if res := c.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": json.RawMessage(`{"id":"x"}`)}); res.code != 401 || e.guard.last().Reason != "bad_assertion" {
		t.Fatalf("garbage = %d %+v", res.code, e.guard.last())
	}
	if res := c.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": assertion}); res.code != 401 || e.guard.last().Reason != "no_ceremony" {
		t.Fatalf("ceremony must be consumed by the failed attempt: %d %+v", res.code, e.guard.last())
	}
	if res := c.webauthnLogin(a); res.code != 200 {
		t.Fatalf("fresh ceremony = %d %s", res.code, res.body)
	}
	sudoBegin := c.do(http.MethodPost, "/api/auth/sudo/webauthn/begin", nil)
	if sudoBegin.code != 200 {
		t.Fatalf("sudo begin = %d", sudoBegin.code)
	}
	if _, err := e.db.Exec(`UPDATE sessions SET webauthn_state = replace(webauthn_state, '"kind":"sudo"', '"kind":"login"') WHERE id = ?`, c.sessionID()); err != nil {
		t.Fatal(err)
	}
	if res := c.do(http.MethodPost, "/api/auth/sudo/webauthn/finish", map[string]any{"credential": a.assert(sudoBegin.body)}); res.code != 401 || e.guard.last().Reason != "no_ceremony" {
		t.Fatalf("login ceremony must not grant sudo: %d %+v", res.code, e.guard.last())
	}
}

func TestSudoWebAuthn(t *testing.T) {
	e := newTestEnv(t)
	a := e.passkeyUser("root", "admin")
	c := e.newClient("203.0.113.10")
	c.login("root", testPassword)
	if res := c.webauthnLogin(a); res.code != 200 {
		t.Fatalf("login = %d", res.code)
	}
	begin := c.do(http.MethodPost, "/api/auth/sudo/webauthn/begin", nil)
	if begin.code != 200 {
		t.Fatalf("sudo begin = %d %s", begin.code, begin.body)
	}
	res := c.do(http.MethodPost, "/api/auth/sudo/webauthn/finish", map[string]any{"credential": a.assert(begin.body)})
	var s sudoResponse
	if res.code != 200 || json.Unmarshal(res.body, &s) != nil || s.SudoUntil != e.clock.Now().Add(10*time.Minute).UnixMilli() {
		t.Fatalf("sudo finish = %d %s", res.code, res.body)
	}
	if att := e.guard.last(); att.Stage != defense.StageSudo || !att.Success {
		t.Fatalf("attempt = %+v", att)
	}
	var keys []passkeyJSON
	if err := json.Unmarshal(c.do(http.MethodGet, "/api/me/passkeys", nil).body, &keys); err != nil || len(keys) != 1 {
		t.Fatalf("keys = %v %v", keys, err)
	}
	if res := c.do(http.MethodDelete, fmt.Sprintf("/api/me/passkeys/%d", keys[0].ID), nil); res.code != 409 {
		t.Fatalf("sudo route after webauthn sudo = %d (want 409 last factor)", res.code)
	}
}

func TestSudoWebAuthnCeremonyExpires(t *testing.T) {
	e := newTestEnv(t)
	a := e.passkeyUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	c.webauthnLogin(a)
	begin := c.do(http.MethodPost, "/api/auth/sudo/webauthn/begin", nil)
	e.clock.Advance(5*time.Minute + time.Second)
	if res := c.do(http.MethodPost, "/api/auth/sudo/webauthn/finish", map[string]any{"credential": a.assert(begin.body)}); res.code != 401 || e.guard.last().Reason != "no_ceremony" {
		t.Fatalf("expired ceremony = %d %+v", res.code, e.guard.last())
	}
	last := e.audit.Entries[len(e.audit.Entries)-1]
	if last.Action != audit.ActionSudo || last.Result != audit.ResultFailure {
		t.Fatalf("audit = %+v", last)
	}
}

func TestWebAuthnBeginWithoutPasskeys(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("alice", "user", "203.0.113.10")
	if res := c.do(http.MethodPost, "/api/auth/2fa/webauthn/begin", nil); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("login begin without passkeys = %d", res.code)
	}
	c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)})
	if res := c.do(http.MethodPost, "/api/auth/sudo/webauthn/begin", nil); res.code != 409 {
		t.Fatalf("sudo begin without passkeys = %d", res.code)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/auth/ -run 'TestWebAuthn|TestSudoWebAuthn'` → FAIL (`404`/`405` für `/api/auth/2fa/webauthn/*`, Routen fehlen).

- [ ] **Step 3: `internal/auth/webauthn_login.go`**

```go
package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"

	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

type assertionRequest struct {
	Credential json.RawMessage `json:"credential"`
}

func (h *Handler) beginWebAuthnLogin(w http.ResponseWriter, r *http.Request) {
	h.beginAssertion(w, r, ceremonyLogin)
}

func (h *Handler) beginSudoWebAuthn(w http.ResponseWriter, r *http.Request) {
	h.beginAssertion(w, r, ceremonySudo)
}

func (h *Handler) beginAssertion(w http.ResponseWriter, r *http.Request, kind string) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	wu, _, err := h.loadWAUser(ctx, user)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if len(wu.creds) == 0 {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "no passkeys registered")
		return
	}
	assertion, data, err := h.wa.BeginLogin(wu)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := h.saveCeremony(ctx, p.SessionID, kind, data); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, assertion)
}

func decodeAssertion(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	var req assertionRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return nil, false
	}
	if len(req.Credential) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "credential is required")
		return nil, false
	}
	return req.Credential, true
}

// verifyAssertion validates an assertion against the pending ceremony of the given
// kind, rejects non-increasing sign counters and persists the updated credential.
// Returns "" on success, otherwise the failure reason.
func (h *Handler) verifyAssertion(ctx context.Context, p principal.Principal, u store.User, kind string, raw json.RawMessage) (string, error) {
	data, err := h.takeCeremony(ctx, p.SessionID, kind)
	if errors.Is(err, errNoCeremony) {
		return reasonNoCeremony, nil
	}
	if err != nil {
		return "", err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(raw)
	if err != nil {
		return reasonBadAssertion, nil
	}
	wu, rows, err := h.loadWAUser(ctx, u)
	if err != nil {
		return "", err
	}
	idx := -1
	for i, row := range rows {
		if bytes.Equal(row.CredentialID, parsed.RawID) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return reasonBadAssertion, nil
	}
	prevCount := wu.creds[idx].Authenticator.SignCount
	cred, err := h.wa.ValidateLogin(wu, data, parsed)
	if err != nil {
		return reasonBadAssertion, nil
	}
	newCount := parsed.Response.AuthenticatorData.Counter
	if cred.Authenticator.CloneWarning || ((newCount != 0 || prevCount != 0) && newCount <= prevCount) {
		return reasonCloneWarning, nil
	}
	b, err := json.Marshal(cred)
	if err != nil {
		return "", err
	}
	if err := h.q.AuthUpdateCredentialUse(ctx, store.AuthUpdateCredentialUseParams{
		CredentialJson: string(b),
		LastUsedAt:     sql.NullInt64{Int64: h.clock.Now().UnixMilli(), Valid: true},
		ID:             rows[idx].ID,
	}); err != nil {
		return "", err
	}
	return "", nil
}

func (h *Handler) finishWebAuthnLogin(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	raw, ok := decodeAssertion(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.guard.CheckSecondFactor(ctx, httpx.ClientIP(ctx), secondFactorKey(p)); err != nil {
		h.guardError(w, r, err)
		return
	}
	reason, err := h.verifyAssertion(ctx, p, user, ceremonyLogin, raw)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if reason != "" {
		h.recordAttempt(r, user.Username, user.ID, defense.StageWebAuthn, false, reason)
		invalidCode(w, r)
		return
	}
	h.completeSecondFactor(w, r, p, user, defense.StageWebAuthn, "webauthn")
}

func (h *Handler) finishSudoWebAuthn(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	raw, ok := decodeAssertion(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.guard.CheckSecondFactor(ctx, httpx.ClientIP(ctx), secondFactorKey(p)); err != nil {
		h.guardError(w, r, err)
		return
	}
	reason, err := h.verifyAssertion(ctx, p, user, ceremonySudo, raw)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if reason != "" {
		h.sudoFailed(r, user, "webauthn", reason)
		invalidCode(w, r)
		return
	}
	h.grantSudo(w, r, p, user, "webauthn")
}
```

- [ ] **Step 4: Routen ergänzen** – in `Routes` nach den Zeilen aus Task 10 (`preAuth` stammt aus Task 7):

```go
	api.Post("/auth/2fa/webauthn/begin", preAuth, h.beginWebAuthnLogin)
	api.Post("/auth/2fa/webauthn/finish", preAuth, h.finishWebAuthnLogin)
	api.Post("/auth/sudo/webauthn/begin", rbac.Authenticated(), h.beginSudoWebAuthn)
	api.Post("/auth/sudo/webauthn/finish", rbac.Authenticated(), h.finishSudoWebAuthn)
```

- [ ] **Step 5: Run** `go test ./internal/auth/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/webauthn_login.go internal/auth/webauthn_login_test.go internal/auth/handler.go
git commit -m "feat(auth): add WebAuthn second factor and sudo with clone detection" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Profil – Sprache, Passwortänderung, Recovery-Codes neu erzeugen

**Files:**
- Create: `internal/auth/me.go`
- Modify: `internal/auth/handler.go` (`Routes` ergänzen)
- Test: `internal/auth/me_test.go`

**Interfaces:**
- Consumes: `AuthUpdateLocale`, `AuthChangePassword`, `AuthRevokeOtherSessions`; `password.Verify`, `password.Validate`, `password.Hash`, `password.ErrTooShort|ErrTooLong|ErrCommon|ErrContainsUsername`, `password.MaxLength`; `defense.LoginGuard.CheckLogin`; `withTx`, `replaceRecoveryCodes`, `notify2FAChanged` (Task 8); `audit.ActionPasswordChange`, `audit.ActionRecoveryRegenerate`; `httpx.CodeValidation`.
- Produces:
```go
type updateMeRequest struct{ Locale string `json:"locale"` }
type passwordChangeRequest struct{ CurrentPassword string `json:"current_password"`; NewPassword string `json:"new_password"` }
func passwordPolicyMessage(err error) string
```

- [ ] **Step 1: Failing test `internal/auth/me_test.go`**

```go
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/store"
)

func TestUpdateLocale(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	if res := c.do(http.MethodPatch, "/api/me", map[string]string{"locale": "en"}); res.code != 200 || res.me(t).Locale != "en" {
		t.Fatalf("PATCH /me = %d %s", res.code, res.body)
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Locale != "en" {
		t.Fatalf("locale not persisted: %+v", m)
	}
	if res := c.do(http.MethodPatch, "/api/me", map[string]string{"locale": "fr"}); res.code != 400 || res.errCode() != "validation_failed" {
		t.Fatalf("invalid locale = %d %s", res.code, res.body)
	}
}

func TestChangePassword(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	other := e.newClient("198.51.100.20")
	other.login("alice", testPassword)
	change := func(cur, next string) response {
		return c.do(http.MethodPost, "/api/me/password", map[string]string{"current_password": cur, "new_password": next})
	}
	if res := change("not the password!", "a brand new passphrase"); res.code != 401 || res.errCode() != "invalid_credentials" {
		t.Fatalf("wrong current = %d %s", res.code, res.body)
	}
	if a := e.guard.last(); a.Reason != "bad_current_password" || a.Stage != defense.StagePassword || a.Success {
		t.Fatalf("attempt = %+v", a)
	}
	for _, weak := range []string{"short", "passwordpassword", "alice-is-the-best-123"} {
		if res := change(testPassword, weak); res.code != 400 || res.errCode() != "validation_failed" {
			t.Fatalf("weak %q = %d %s", weak, res.code, res.body)
		}
	}
	if res := change(testPassword, "a brand new passphrase"); res.code != 204 {
		t.Fatalf("change = %d %s", res.code, res.body)
	}
	if res := other.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("other sessions must be revoked, got %d", res.code)
	}
	if res := c.do(http.MethodGet, "/api/auth/me", nil); res.code != 200 {
		t.Fatalf("current session must survive, got %d", res.code)
	}
	if res := e.newClient("203.0.113.10").login("alice", "a brand new passphrase"); res.code != 200 {
		t.Fatalf("login with new password = %d", res.code)
	}
	if res := e.newClient("203.0.113.10").login("alice", testPassword); res.code != 401 {
		t.Fatalf("login with old password = %d", res.code)
	}
	if !hasAction(e, audit.ActionPasswordChange) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	e.guard.LoginErr = defense.ErrJailed
	if res := change("a brand new passphrase", "yet another passphrase"); res.code != 403 || res.errCode() != "jailed" {
		t.Fatalf("jailed = %d %s", res.code, res.body)
	}
}

func TestRegenerateRecoveryCodes(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	c, uid, _ := e.fullClient("alice", "user", "203.0.113.10")
	old := hashRecoveryCode("abcde-fghjk")
	if err := e.q.AuthInsertRecoveryCode(ctx, store.AuthInsertRecoveryCodeParams{UserID: uid, CodeHash: old, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if res := c.do(http.MethodPost, "/api/me/recovery-codes", nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("without sudo = %d", res.code)
	}
	e.grantSudo(c)
	res := c.do(http.MethodPost, "/api/me/recovery-codes", nil)
	var rc recoveryCodesResponse
	if res.code != 200 || json.Unmarshal(res.body, &rc) != nil || len(rc.Codes) != 10 {
		t.Fatalf("regenerate = %d %s", res.code, res.body)
	}
	if n, _ := e.q.AuthCountUnusedRecoveryCodes(ctx, uid); n != 10 {
		t.Fatalf("unused = %d", n)
	}
	c2 := e.newClient("203.0.113.10")
	c2.login("alice", testPassword)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "abcde-fghjk"}); res.code != 401 {
		t.Fatalf("old code must be invalid, got %d", res.code)
	}
	if res := c2.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": rc.Codes[0]}); res.code != 200 {
		t.Fatalf("new code = %d %s", res.code, res.body)
	}
	if !hasAction(e, audit.ActionRecoveryRegenerate) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/auth/ -run 'TestUpdateLocale|TestChangePassword|TestRegenerateRecoveryCodes'` → FAIL (`405`/`404`, Routen fehlen).

- [ ] **Step 3: `internal/auth/me.go`**

```go
package auth

import (
	"database/sql"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/store"
)

type updateMeRequest struct {
	Locale string `json:"locale"`
}

func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	var req updateMeRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.Locale != "de" && req.Locale != "en" {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, `locale must be "de" or "en"`)
		return
	}
	ctx := r.Context()
	if err := h.q.AuthUpdateLocale(ctx, store.AuthUpdateLocaleParams{
		Locale: req.Locale, Now: h.clock.Now().UnixMilli(), ID: user.ID,
	}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	user.Locale = req.Locale
	sess, err := h.q.AuthGetSessionByID(ctx, p.SessionID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.writeMe(w, r, user, sess)
}

type passwordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func passwordPolicyMessage(err error) string {
	switch {
	case errors.Is(err, password.ErrTooShort):
		return "password must have at least 12 characters"
	case errors.Is(err, password.ErrTooLong):
		return "password must have at most 256 characters"
	case errors.Is(err, password.ErrCommon):
		return "password is too common"
	case errors.Is(err, password.ErrContainsUsername):
		return "password must not contain the username"
	default:
		return "password does not meet the policy"
	}
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	var req passwordChangeRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.CurrentPassword == "" || utf8.RuneCountInString(req.CurrentPassword) > password.MaxLength {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, "current_password is required")
		return
	}
	ctx := r.Context()
	// A stolen session must not allow brute-forcing the current password.
	if err := h.guard.CheckLogin(ctx, httpx.ClientIP(ctx), user.Username); err != nil {
		h.guardError(w, r, err)
		return
	}
	valid, _, err := password.Verify(req.CurrentPassword, user.PasswordHash)
	if err != nil {
		valid = false
	}
	if !valid {
		h.recordAttempt(r, user.Username, user.ID, defense.StagePassword, false, reasonBadCurrentPassword)
		httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeInvalidCredentials, "current password is incorrect")
		return
	}
	if err := password.Validate(req.NewPassword, user.Username); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, passwordPolicyMessage(err))
		return
	}
	hash, err := password.Hash(req.NewPassword)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now().UnixMilli()
	if err := h.q.AuthChangePassword(ctx, store.AuthChangePasswordParams{PasswordHash: hash, Now: now, ID: user.ID}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	revoked, err := h.q.AuthRevokeOtherSessions(ctx, store.AuthRevokeOtherSessionsParams{
		RevokedAt: sql.NullInt64{Int64: now, Valid: true}, UserID: user.ID, KeepID: p.SessionID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionPasswordChange, user.Username, audit.ResultSuccess,
		map[string]any{"revoked_sessions": revoked})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	_, user, ok := h.current(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	now := h.clock.Now().UnixMilli()
	var codes []string
	err := h.withTx(ctx, func(q *store.Queries) error {
		var err error
		codes, err = replaceRecoveryCodes(ctx, q, user.ID, now)
		return err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionRecoveryRegenerate, user.Username, audit.ResultSuccess, nil)
	h.notify2FAChanged(ctx, user, "recovery codes regenerated")
	httpx.WriteJSON(w, http.StatusOK, recoveryCodesResponse{Codes: codes})
}
```

- [ ] **Step 4: Routen ergänzen** – in `Routes` nach den Zeilen aus Task 11:

```go
	api.Patch("/me", rbac.Authenticated(), h.updateMe)
	api.Post("/me/password", rbac.Authenticated(), h.changePassword)
	api.Post("/me/recovery-codes", rbac.Authenticated().WithSudo(), h.regenerateRecoveryCodes)
```

- [ ] **Step 5: Run** `go test ./internal/auth/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/me.go internal/auth/me_test.go internal/auth/handler.go
git commit -m "feat(auth): add locale update, password change and recovery code regeneration" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Session-Verwaltung (eigene und alle Sessions)

**Files:**
- Create: `internal/auth/sessions_api.go`
- Modify: `internal/auth/handler.go` (`Routes` ergänzen)
- Test: `internal/auth/sessions_api_test.go`

**Interfaces:**
- Consumes: `AuthListActiveSessionsForUser`, `AuthListAllActiveSessions`, `AuthRevokeUserSession`, `AuthRevokeOtherSessions`, `AuthGetSessionByID`, `AuthRevokeSession`; `clearSessionCookie`; `audit.ActionSessionRevoke`; `rbac.Authenticated()`, `rbac.Admin()`, `rbac.Admin().WithSudo()`; `chi.URLParam`.
- Produces:
```go
type sessionJSON struct{ /* JSON schema Session */ }
func (h *Handler) activeWindow() (now, idleCutoff int64)
```

- [ ] **Step 1: Failing test `internal/auth/sessions_api_test.go`**

```go
package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
)

func listSessions(t *testing.T, c *client, path string) []sessionJSON {
	t.Helper()
	res := c.do(http.MethodGet, path, nil)
	var out []sessionJSON
	if res.code != 200 || json.Unmarshal(res.body, &out) != nil {
		t.Fatalf("GET %s = %d %s", path, res.code, res.body)
	}
	return out
}

func TestMySessions(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	a := e.newClient("203.0.113.10")
	a.login("alice", testPassword)
	b := e.newClient("198.51.100.20")
	b.login("alice", testPassword)

	list := listSessions(t, a, "/api/me/sessions")
	if len(list) != 2 {
		t.Fatalf("sessions = %+v", list)
	}
	var bID int64
	current := 0
	for _, s := range list {
		if s.UserID != uid || s.Username != "alice" || s.Country != "DE" || s.UserAgent != "test-agent" || s.ExpiresAt <= s.CreatedAt {
			t.Fatalf("session = %+v", s)
		}
		if s.Current {
			current++
			if s.IP != "203.0.113.10" {
				t.Fatalf("current session ip = %s", s.IP)
			}
		} else {
			bID = s.ID
		}
	}
	if current != 1 || bID == 0 {
		t.Fatalf("current=%d bID=%d", current, bID)
	}

	e.createUser("mallory", "user")
	m := e.newClient("192.0.2.30")
	m.login("mallory", testPassword)
	for _, p := range []string{fmt.Sprintf("/api/me/sessions/%d", m.sessionID()), "/api/me/sessions/99999", "/api/me/sessions/abc"} {
		if res := a.do(http.MethodDelete, p, nil); res.code != 404 {
			t.Fatalf("DELETE %s = %d", p, res.code)
		}
	}
	if res := a.do(http.MethodDelete, fmt.Sprintf("/api/me/sessions/%d", bID), nil); res.code != 204 {
		t.Fatalf("revoke b = %d %s", res.code, res.body)
	}
	if res := b.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("revoked session still valid: %d", res.code)
	}
	if !hasAction(e, audit.ActionSessionRevoke) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	own := a.sessionID()
	if res := a.do(http.MethodDelete, fmt.Sprintf("/api/me/sessions/%d", own), nil); res.code != 204 || a.cookie != "" {
		t.Fatalf("revoking the current session must clear the cookie: %d cookie=%q", res.code, a.cookie)
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	a, b, c := e.newClient("203.0.113.10"), e.newClient("198.51.100.20"), e.newClient("192.0.2.30")
	for _, cl := range []*client{a, b, c} {
		cl.login("alice", testPassword)
	}
	if res := a.do(http.MethodPost, "/api/me/sessions/revoke-others", nil); res.code != 204 {
		t.Fatalf("revoke-others = %d %s", res.code, res.body)
	}
	for _, cl := range []*client{b, c} {
		if res := cl.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
			t.Fatalf("other session still valid: %d", res.code)
		}
	}
	if res := a.do(http.MethodGet, "/api/auth/me", nil); res.code != 200 {
		t.Fatalf("current session = %d", res.code)
	}
}

func TestSessionListExcludesIdle(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	a := e.newClient("203.0.113.10")
	a.login("alice", testPassword)
	e.newClient("198.51.100.20").login("alice", testPassword)
	e.clock.Advance(20 * time.Minute)
	a.do(http.MethodGet, "/api/auth/me", nil) // keeps a alive
	e.clock.Advance(11 * time.Minute)         // b idle for 31 min
	if list := listSessions(t, a, "/api/me/sessions"); len(list) != 1 || !list[0].Current {
		t.Fatalf("sessions = %+v", list)
	}
}

func TestAdminSessions(t *testing.T) {
	e := newTestEnv(t)
	admin, _, _ := e.fullClient("root", "admin", "203.0.113.10")
	e.createUser("alice", "user")
	u := e.newClient("198.51.100.20")
	u.login("alice", testPassword)
	if res := u.do(http.MethodGet, "/api/admin/sessions", nil); res.code != 403 || res.errCode() != "forbidden" {
		t.Fatalf("user on admin route = %d %s", res.code, res.body)
	}
	if list := listSessions(t, admin, "/api/admin/sessions"); len(list) != 2 {
		t.Fatalf("all sessions = %+v", list)
	}
	path := fmt.Sprintf("/api/admin/sessions/%d", u.sessionID())
	if res := admin.do(http.MethodDelete, path, nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("without sudo = %d", res.code)
	}
	e.grantSudo(admin)
	if res := admin.do(http.MethodDelete, path, nil); res.code != 204 {
		t.Fatalf("revoke = %d %s", res.code, res.body)
	}
	if res := u.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("revoked user session = %d", res.code)
	}
	if res := admin.do(http.MethodDelete, "/api/admin/sessions/999999", nil); res.code != 404 {
		t.Fatalf("unknown session = %d", res.code)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/auth/ -run 'TestMySessions|TestRevokeOtherSessions|TestSessionListExcludesIdle|TestAdminSessions'` → FAIL (`undefined: sessionJSON`).

- [ ] **Step 3: `internal/auth/sessions_api.go`**

```go
package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

type sessionJSON struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	Username   string `json:"username"`
	Current    bool   `json:"current"`
	IP         string `json:"ip"`
	Country    string `json:"country"`
	UserAgent  string `json:"user_agent"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	ExpiresAt  int64  `json:"expires_at"`
}

// activeWindow matches Sessions.valid: a session is idle once last_seen_at <= now - Idle.
func (h *Handler) activeWindow() (now, idleCutoff int64) {
	n := h.clock.Now().UnixMilli()
	return n, n - h.cfg.Session.Idle.Milliseconds()
}

func sessionIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "session not found")
		return 0, false
	}
	return id, true
}

func (h *Handler) listMySessions(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	now, cutoff := h.activeWindow()
	rows, err := h.q.AuthListActiveSessionsForUser(r.Context(), store.AuthListActiveSessionsForUserParams{
		Now: now, IdleCutoff: cutoff, UserID: p.UserID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]sessionJSON, 0, len(rows))
	for _, s := range rows {
		out = append(out, sessionJSON{
			ID: s.ID, UserID: s.UserID, Username: s.Username, Current: s.ID == p.SessionID, IP: s.Ip,
			Country: s.Country, UserAgent: s.UserAgent, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) listAllSessions(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	now, cutoff := h.activeWindow()
	rows, err := h.q.AuthListAllActiveSessions(r.Context(), store.AuthListAllActiveSessionsParams{Now: now, IdleCutoff: cutoff})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]sessionJSON, 0, len(rows))
	for _, s := range rows {
		out = append(out, sessionJSON{
			ID: s.ID, UserID: s.UserID, Username: s.Username, Current: s.ID == p.SessionID, IP: s.Ip,
			Country: s.Country, UserAgent: s.UserAgent, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) revokedAt() sql.NullInt64 {
	return sql.NullInt64{Int64: h.clock.Now().UnixMilli(), Valid: true}
}

func (h *Handler) afterRevoke(w http.ResponseWriter, r *http.Request, p principal.Principal, id int64, details map[string]any) {
	if id == p.SessionID {
		clearSessionCookie(w)
	}
	h.auditLog(r.Context(), p.UserID, p.Username, audit.ActionSessionRevoke, fmt.Sprintf("session:%d", id), audit.ResultSuccess, details)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) revokeMySession(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := sessionIDParam(w, r)
	if !ok {
		return
	}
	n, err := h.q.AuthRevokeUserSession(r.Context(), store.AuthRevokeUserSessionParams{
		RevokedAt: h.revokedAt(), ID: id, UserID: p.UserID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "session not found")
		return
	}
	h.afterRevoke(w, r, p, id, nil)
}

func (h *Handler) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	n, err := h.q.AuthRevokeOtherSessions(r.Context(), store.AuthRevokeOtherSessionsParams{
		RevokedAt: h.revokedAt(), UserID: p.UserID, KeepID: p.SessionID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.auditLog(r.Context(), p.UserID, p.Username, audit.ActionSessionRevoke, "sessions:others", audit.ResultSuccess,
		map[string]any{"count": n})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) revokeAnySession(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := sessionIDParam(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	sess, err := h.q.AuthGetSessionByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "session not found")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	n, err := h.q.AuthRevokeSession(ctx, store.AuthRevokeSessionParams{RevokedAt: h.revokedAt(), ID: id})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "session not found")
		return
	}
	h.afterRevoke(w, r, p, id, map[string]any{"user_id": sess.UserID})
}
```

- [ ] **Step 4: Routen ergänzen** – in `Routes` nach den Zeilen aus Task 12:

```go
	api.Get("/me/sessions", rbac.Authenticated(), h.listMySessions)
	api.Delete("/me/sessions/{id}", rbac.Authenticated(), h.revokeMySession)
	api.Post("/me/sessions/revoke-others", rbac.Authenticated(), h.revokeOtherSessions)
	api.Get("/admin/sessions", rbac.Admin(), h.listAllSessions)
	api.Delete("/admin/sessions/{id}", rbac.Admin().WithSudo(), h.revokeAnySession)
```

- [ ] **Step 5: Run** `go test ./internal/auth/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/sessions_api.go internal/auth/sessions_api_test.go internal/auth/handler.go
git commit -m "feat(auth): add session listing and revocation endpoints" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Routen-Vertrag und CSRF-Integration

**Files:**
- Test: `internal/auth/routes_test.go`

**Interfaces:**
- Consumes: `(*rbac.Router).Routes() []rbac.RouteInfo`, `rbac.Rule.String()`, `rbac.Public/Authenticated/Admin`, `.AllowStages`, `.WithSudo`; Test-Umgebung aus Task 6.
- Produces: – (reiner Vertragstest; schlägt fehl, sobald eine Route fehlt, doppelt ist oder eine andere Regel als in der Phase-0-Tabelle hat).

- [ ] **Step 1: Test `internal/auth/routes_test.go`**

```go
package auth

import (
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

func TestRoutesMatchContract(t *testing.T) {
	e := newTestEnv(t)
	anyStage := rbac.Authenticated().AllowStages(principal.StagePreAuth, principal.StageSetup, principal.StageFull)
	preAuth := rbac.Authenticated().AllowStages(principal.StagePreAuth)
	setupOrFull := rbac.Authenticated().AllowStages(principal.StageSetup, principal.StageFull)
	want := map[string]rbac.Rule{
		"POST /auth/login":                rbac.Public(),
		"GET /auth/me":                    anyStage,
		"POST /auth/logout":               anyStage,
		"POST /auth/2fa/totp":             preAuth,
		"POST /auth/2fa/recovery":         preAuth,
		"POST /auth/2fa/webauthn/begin":   preAuth,
		"POST /auth/2fa/webauthn/finish":  preAuth,
		"POST /auth/sudo/totp":            rbac.Authenticated(),
		"POST /auth/sudo/webauthn/begin":  rbac.Authenticated(),
		"POST /auth/sudo/webauthn/finish": rbac.Authenticated(),
		"PATCH /me":                       rbac.Authenticated(),
		"POST /me/password":               rbac.Authenticated(),
		"POST /me/totp/setup":             setupOrFull,
		"POST /me/totp/enable":            setupOrFull,
		"DELETE /me/totp":                 rbac.Authenticated().WithSudo(),
		"POST /me/passkeys/begin":         setupOrFull,
		"POST /me/passkeys/finish":        setupOrFull,
		"GET /me/passkeys":                rbac.Authenticated(),
		"DELETE /me/passkeys/{id}":        rbac.Authenticated().WithSudo(),
		"POST /me/recovery-codes":         rbac.Authenticated().WithSudo(),
		"GET /me/sessions":                rbac.Authenticated(),
		"DELETE /me/sessions/{id}":        rbac.Authenticated(),
		"POST /me/sessions/revoke-others": rbac.Authenticated(),
		"GET /admin/sessions":             rbac.Admin(),
		"DELETE /admin/sessions/{id}":     rbac.Admin().WithSudo(),
	}
	if len(want) != 25 {
		t.Fatalf("contract table has %d entries, want 25", len(want))
	}
	got := map[string]string{}
	for _, ri := range e.router.Routes() {
		key := ri.Method + " " + ri.Pattern
		if _, dup := got[key]; dup {
			t.Fatalf("route %s registered twice", key)
		}
		got[key] = ri.Rule.String()
	}
	for key, rule := range want {
		if got[key] != rule.String() {
			t.Errorf("%s: rule %q, want %q", key, got[key], rule.String())
		}
	}
	if len(got) != len(want) {
		t.Errorf("registered %d routes, want %d: %v", len(got), len(want), got)
	}
}

func TestCSRFEnforcedOnAuthRoutes(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	body := map[string]string{"locale": "en"}
	if res := c.doWith(http.MethodPatch, "/api/me", body, func(r *http.Request) { r.Header.Del("X-CSRF-Token") }); res.code != 403 || res.errCode() != "csrf_invalid" {
		t.Fatalf("missing token = %d %s", res.code, res.body)
	}
	if res := c.doWith(http.MethodPatch, "/api/me", body, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }); res.code != 403 || res.errCode() != "csrf_invalid" {
		t.Fatalf("cross-site = %d %s", res.code, res.body)
	}
	if res := c.do(http.MethodPatch, "/api/me", body); res.code != 200 {
		t.Fatalf("valid request = %d %s", res.code, res.body)
	}
	evil := e.newClient("203.0.113.10")
	creds := map[string]string{"username": "alice", "password": testPassword}
	if res := evil.doWith(http.MethodPost, "/api/auth/login", creds, func(r *http.Request) {
		r.Header.Del("Sec-Fetch-Site")
		r.Header.Set("Origin", "https://evil.example")
	}); res.code != 403 || res.errCode() != "csrf_invalid" || evil.cookie != "" {
		t.Fatalf("cross-origin login = %d %s", res.code, res.body)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/auth/ -run 'TestRoutesMatchContract|TestCSRFEnforcedOnAuthRoutes' -v` → PASS (alle Routen sind aus Task 6–13 registriert). Fehlt eine Route oder weicht eine Regel ab, zeigt der Test den Schlüssel; dann `Routes` korrigieren, nicht den Test.

- [ ] **Step 3: Gesamtlauf, Race-Detector und Lint**

```bash
go test ./internal/auth/... ./internal/store/...
CGO_ENABLED=1 go test -race ./internal/auth/...
go vet ./internal/auth/...
golangci-lint run ./internal/auth/...
```
Expected: alles grün, keine Lint-Befunde (insbesondere keine ungenutzten Konstanten/Felder – alle `reason*`-Konstanten und `h.wa` werden ab Task 11 verwendet).

- [ ] **Step 4: Commit**

```bash
git add internal/auth/routes_test.go
git commit -m "test(auth): pin route rules to the OpenAPI contract and CSRF integration" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-Review

Spec-Abdeckung (Stream A) → Task:

| Anforderung (Spec) | Task | Pinnender Test |
|---|---|---|
| §4.1 argon2id, Re-Hash bei Parameteränderung beim Login | 6 | `TestLoginRehashesOldHash` |
| §4.1 Länge 12–256, häufige Passwörter, kein Benutzername (Passwortänderung) | 12 | `TestChangePassword` |
| §4.1 Unbekannter Benutzer: Dummy-Hash, identische Meldung | 6 | `TestLoginInvalidCredentialsIdentical`, `TestLoginUnusablePasswordHash` |
| §4.2 TOTP 30 s / 6 Stellen / ±1 Schritt | 3 | `TestMatchTOTP` |
| §4.2 TOTP-Replay-Schutz (Login, Sudo, Aktivierung gemeinsam) | 1, 7, 8, 9 | `TestAuthQueries/totp…`, `TestTOTPReplayRejectedOnLogin`, `TestTOTPCodeNotReusableForSudo`, `TestAdminSetupFlowWithTOTP` |
| §4.2 TOTP-Secret verschlüsselt (AES-GCM, AAD `totp:<id>`) | 3, 8 | `TestSealTOTPSecret`, `TestAdminSetupFlowWithTOTP` |
| §4.2 Ausstehendes Setup-Secret (`totp_secret_enc` bei `totp_enabled = 0`) | 8 | `TestAdminSetupFlowWithTOTP`, `TestEnableWithoutSetup`, `TestSetupConflictWhenTOTPEnabled` |
| §4.2 WebAuthn: mehrere Credentials, benennbar, löschbar, RP-ID = Domain | 10 | `TestPasskeyRegistrationAndList`, `TestDeleteLastPasskeyOfAdmin` |
| §4.2 WebAuthn als 2. Faktor, Sign-Count/`last_used_at` | 11 | `TestWebAuthnLogin`, `TestWebAuthnCloneDetected` |
| §4.2 Recovery-Codes 10×10, gehasht, einmalig, automatisch bei erster Methode, Neu-Erzeugung mit Sudo | 2, 7, 8, 10, 12 | `TestGenerateRecoveryCodes`, `TestRecoveryCodeLogin`, `TestAdminSetupFlowWithTOTP`, `TestRegenerateRecoveryCodes` |
| §4.2 2FA-Pflicht Admin / erzwingbar für User (Setup-Stage) | 6, 8, 10 | `TestLoginStages`, `TestAdminSetupWithPasskeyPromotesSession` |
| Übersicht #2: letzte 2FA-Methode nicht entfernbar | 8, 10 | `TestDisableTOTPLastFactorAdmin`, `TestDisableTOTPEnforcedUser`, `TestDeleteLastPasskeyOfAdmin`, `TestConcurrentFactorRemovalKeepsOne` |
| §4.3 Pre-Auth-Session 5 min, nur für 2FA | 4, 7 | `TestPreAuthSessionExpiresAfterFiveMinutes`, `TestPreAuthSessionExpires`, `TestTOTPLoginFlow` |
| §4.3 Neue Session-ID nach jedem Privilegienwechsel | 4, 6, 7, 8, 9 | `TestRotateAndRevoke`, `TestLoginRevokesPresentedSession`, `TestTOTPLoginFlow`, `TestSudoTOTPGrantsSudo` |
| §4.3 Jeder Versuch in `login_attempts` (über `LoginGuard.RecordAttempt`) | 6, 7, 9, 11, 12 | Attempt-Assertions in allen Login-/Sudo-Tests |
| §4.4 Token 256 Bit, DB nur SHA-256, Cookie-Attribute | 2, 4 | `TestNewToken`, `TestSessionCreateAndLookup`, `TestSessionCookies` |
| §4.4 Idle 30 min, absolut 12 h, `last_seen` max. 1×/min | 4 | `TestIdleTimeoutAndTouchThrottling`, `TestAbsoluteTimeout` |
| §4.4 Gespeichert: IP, User-Agent, Land (GeoIP), Sudo-bis | 4, 6, 13 | `TestSessionCreateAndLookup`, `TestMySessions` |
| §4.4 Sessions auflisten/widerrufen, alle anderen widerrufen, Passwortänderung widerruft andere | 12, 13 | `TestMySessions`, `TestRevokeOtherSessions`, `TestAdminSessions`, `TestChangePassword` |
| §4.5 Sudo ≤ 10 min per 2FA, `403 sudo_required` | 9, 11 | `TestSudoTOTPGrantsSudo`, `TestSudoExpires`, `TestSudoWebAuthn` |
| §4.5 2FA-Methoden ändern verlangt Sudo (Handler-Prüfung bei Setup/Passkey-Begin, rbac bei DELETE) | 8, 10 | `TestSetupRequiresSudoWhenUserHas2FA`, `TestPasskeyRegistrationAndList` |
| §4.6 Sec-Fetch-Site/Origin + Synchronizer-Token, Login nur Origin-Prüfung | 5, 14 | `TestCSRF`, `TestCSRFEnforcedOnAuthRoutes` |
| §7.1/§7.2 Rate-Limit/Jail über Guard, `429` + `Retry-After`, `403 jailed` | 6, 7, 12 | `TestLoginGuardErrors`, `TestSecondFactorGuard`, `TestChangePassword` |
| §7.3 Admin nur aus Allowlist (`CheckAdminNetwork` nach Passwort) | 6 | `TestLoginGuardErrors` |
| §7.5 „2FA geändert“ durch A; „neues Gerät/neue IP“ sendet Stream B aus `RecordAttempt` – A liefert genau einen `complete`-Erfolgsversuch und sendet selbst nichts | 6, 7, 8, 10, 12 | `TestCompleteAttemptRecordedWithoutNotification`, `TestTOTPLoginFlow`, `TestAdminSetupFlowWithTOTP` |
| §6 Audit aller Auth-Aktionen (nie innerhalb eigener Transaktion) | 6–13 | `hasAction`-Assertions |
| OpenAPI: 25 `A`-Endpunkte mit exakten Regeln | 6–13, 14 | `TestRoutesMatchContract` |
| Abnahme 3: `admin create` → erzwungenes Setup → Login mit TOTP und mit Passkey | 6, 8, 10, 11 | `TestAdminSetupFlowWithTOTP`, `TestAdminSetupWithPasskeyPromotesSession`, `TestSudoWebAuthn` |

Bewusste Festlegungen / Abweichungen (an den Lead zu melden):
- `POST /me/totp/setup` antwortet `409 conflict`, wenn TOTP bereits aktiv ist → `Conflict` in `api/openapi.yaml` bei `setupTotp` ergänzen.
- `DELETE /me/totp` antwortet `409 conflict` auch dann, wenn TOTP gar nicht aktiv ist (Fehlercode bereits im Vertrag).
- `POST /me/passkeys/finish` nutzt `400 validation_failed` (Name) und `400 bad_request` (fehlende/abgelaufene Zeremonie, ungültige Antwort) – beide Codes unter `BadRequest`.
- Fehlgeschlagene Logins werden nicht auditiert (nur `login_attempts`); Sudo-Fehlschläge schon.
- Nach setup→full bleibt der CSRF-Token gleich; das Frontend muss nach `enable`/`finish` im Setup-Flow nur `GET /auth/me` neu laden, um `stage: full` zu sehen.

## Wiring-Vertrag

Phase 2 (`cmd/panel`) ruft genau dies auf:

```go
key, err := secretbox.LoadOrCreateKey(cfg.SecretKeyPath())
box, err := secretbox.New(key)

authH, err := auth.New(auth.Deps{
	DB:       db,           // *sql.DB from store.Open (must keep _txlock=immediate)
	Config:   cfg,          // config.Panel after Validate (RPID(), Origin(), Session)
	Clock:    clock.Real{},
	Box:      box,
	Guard:    loginGuard,   // stream B: implementation of defense.LoginGuard
	Geo:      geo,          // stream B: defense.GeoResolver (or defense.NopGeo{})
	Audit:    auditRec,     // stream D: the single process-wide audit.Recorder
	Notifier: notifier,     // stream B: notify.Notifier dispatcher
})

mux.Use(
	httpx.RequestID,
	httpx.RealIP(cfg.TrustedPrefixes()),
	httpx.SecurityHeaders,
	httpx.BodyLimit(1 << 20),
	ipFilter,                     // stream B
	authH.Sessions().Middleware,  // func(http.Handler) http.Handler – sets principal.Principal
	auth.CSRF(cfg.Origin()),      // func(string) func(http.Handler) http.Handler
	apiRateLimit,                 // stream B (may use principal.SessionID)
)
rt.Route("/api", func(api *rbac.Router) {
	authH.Routes(api) // registers the 25 stream-A routes
	// defenseH.Routes(api), usersH.Routes(api), auditH.Routes(api)
})
```

Signaturen:
```go
func New(d auth.Deps) (*auth.Handler, error)
func (h *auth.Handler) Sessions() *auth.Sessions
func (s *auth.Sessions) Middleware(next http.Handler) http.Handler
func CSRF(origin string) func(http.Handler) http.Handler
func (h *auth.Handler) Routes(api *rbac.Router)
const auth.CookieName = "__Host-sid"
```

Hintergrund-Goroutinen: **keine**. Abgelaufene/widerrufene Session-Zeilen bleiben in der Tabelle (werden bei Lookup/Listen ignoriert); eine Aufräumroutine ist nicht Teil dieses Teilprojekts.

Vertrag mit Stream B (`login_attempts.reason`): Erfolg `complete` markiert eine abgeschlossene Anmeldung (setup/full, genau ein Versuch pro Anmeldung); Stream B leitet daraus in `RecordAttempt` die Neues-Gerät-Benachrichtigung ab – Stream A sendet `EventNewDeviceLogin` nie selbst; `second_factor_pending` = Passwort korrekt, 2FA ausstehend; `sudo` = Sudo-Bestätigung. Der 2FA-Schlüssel für `CheckSecondFactor` ist die dezimale Session-ID.
