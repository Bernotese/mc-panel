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
	"unicode/utf8"

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
	// recoveryKey is the HMAC key for recovery codes (derived from the secretbox key).
	recoveryKey []byte
	twoFA       *twoFALocks
	// failedNotes deduplicates auth.2fa_failed notifications per user and IP key.
	failedNotes *noteDedup

	// afterStageDecision is a test seam (nil in production): it runs in the login flow after the
	// session stage was decided and before the session row is inserted.
	afterStageDecision func()
	// afterPasswordCheck is a test seam (nil in production): it runs right after a password was
	// verified (login, current password) and before anything is written.
	afterPasswordCheck func()
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
	sessions := NewSessions(q, d.Clock, d.Config.Session)
	sessions.adminNet = d.Guard.CheckAdminNetwork
	return &Handler{
		db: d.DB, q: q, cfg: d.Config, clock: d.Clock, box: d.Box, guard: d.Guard, geo: d.Geo,
		audit: d.Audit, notifier: d.Notifier, sessions: sessions, wa: wa,
		recoveryKey: d.Box.DeriveKey(recoveryKeyPurpose), twoFA: newTwoFALocks(),
		failedNotes: newNoteDedup(failedSecondFactorNoteWindow),
	}, nil
}

// Sessions exposes the session manager (its Middleware is mounted globally by Phase 2).
func (h *Handler) Sessions() *Sessions { return h.sessions }

// Routes registers all stream-A endpoints with the rules from the OpenAPI table.
func (h *Handler) Routes(api *rbac.Router) {
	anyStage := rbac.Authenticated().AllowStages(principal.StagePreAuth, principal.StageSetup, principal.StagePassword, principal.StageFull)
	api.Post("/auth/login", rbac.Public(), h.login)
	api.Get("/auth/me", anyStage, h.getMe)
	api.Post("/auth/logout", anyStage, h.logout)
	preAuth := rbac.Authenticated().AllowStages(principal.StagePreAuth)
	api.Post("/auth/2fa/totp", preAuth, h.verifyTOTP)
	api.Post("/auth/2fa/recovery", preAuth, h.verifyRecovery)
	setupOrFull := rbac.Authenticated().AllowStages(principal.StageSetup, principal.StageFull)
	api.Post("/me/totp/setup", setupOrFull, h.setupTOTP)
	api.Post("/me/totp/enable", setupOrFull, h.enableTOTP)
	api.Delete("/me/totp", rbac.Authenticated().WithSudo(), h.disableTOTP)
	api.Post("/auth/sudo/totp", rbac.Authenticated(), h.sudoTOTP)
	api.Post("/me/passkeys/begin", setupOrFull, h.beginPasskeyRegistration)
	api.Post("/me/passkeys/finish", setupOrFull, h.finishPasskeyRegistration)
	api.Get("/me/passkeys", rbac.Authenticated(), h.listPasskeys)
	api.Delete("/me/passkeys/{id}", rbac.Authenticated().WithSudo(), h.deletePasskey)
	api.Post("/auth/2fa/webauthn/begin", preAuth, h.beginWebAuthnLogin)
	api.Post("/auth/2fa/webauthn/finish", preAuth, h.finishWebAuthnLogin)
	api.Post("/auth/sudo/webauthn/begin", rbac.Authenticated(), h.beginSudoWebAuthn)
	api.Post("/auth/sudo/webauthn/finish", rbac.Authenticated(), h.finishSudoWebAuthn)
	api.Patch("/me", rbac.Authenticated(), h.updateMe)
	// The only route of the password stage besides /auth/me and the logout (SEC-45).
	api.Post("/me/password", rbac.Authenticated().AllowStages(principal.StagePassword, principal.StageFull), h.changePassword)
	api.Post("/me/recovery-codes", rbac.Authenticated().WithSudo(), h.regenerateRecoveryCodes)
	api.Get("/me/sessions", rbac.Authenticated(), h.listMySessions)
	api.Delete("/me/sessions/{id}", rbac.Authenticated(), h.revokeMySession)
	api.Post("/me/sessions/revoke-others", rbac.Authenticated(), h.revokeOtherSessions)
	api.Get("/admin/sessions", rbac.Admin(), h.listAllSessions)
	api.Delete("/admin/sessions/{id}", rbac.Admin().WithSudo(), h.revokeAnySession)
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

// Why 2FA is mandatory for a user (Me.two_factor_reason); reasonNone means it is optional.
const (
	reasonNone        = "none"
	reasonAdmin       = "admin"
	reasonPermissions = "permissions"
	reasonPolicy      = "policy"
)

// twoFactorMandatory: admins always; users holding a full-trust permission on any server
// (SEC-10); other users when enforcement is switched on.
func (h *Handler) twoFactorMandatory(ctx context.Context, q *store.Queries, u store.User) (bool, error) {
	return twoFactorMandatory(ctx, q, u)
}

func twoFactorMandatory(ctx context.Context, q *store.Queries, u store.User) (bool, error) {
	reason, err := twoFactorReason(ctx, q, u)
	return reason != reasonNone, err
}

// twoFactorReason is evaluated on every request (effectiveStage), so a permission granted to a
// logged-in user takes effect at once and a revoked one makes 2FA optional again.
func twoFactorReason(ctx context.Context, q *store.Queries, u store.User) (string, error) {
	if principal.Role(u.Role) == principal.RoleAdmin {
		return reasonAdmin, nil
	}
	perms := make([]string, len(rbac.FullTrustPermissions))
	for i, p := range rbac.FullTrustPermissions {
		perms[i] = string(p)
	}
	fullTrust, err := q.RbacUserHasAnyPermission(ctx, store.RbacUserHasAnyPermissionParams{UserID: u.ID, Perms: perms})
	if err != nil {
		return reasonNone, err
	}
	if fullTrust {
		return reasonPermissions, nil
	}
	s, err := store.LoadSecuritySettings(ctx, q)
	if err != nil {
		return reasonNone, err
	}
	if s.Enforce2FAForUsers {
		return reasonPolicy, nil
	}
	return reasonNone, nil
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
	TwoFactorReason        string   `json:"two_factor_reason"`
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
	reason, err := twoFactorReason(r.Context(), h.q, u)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	stage := passwordStage(u, principal.Stage(sess.Stage)) // login and second factor report the forced change too
	httpx.WriteJSON(w, http.StatusOK, meResponse{
		ID: u.ID, Username: u.Username, Role: u.Role, Stage: string(stage), Locale: u.Locale,
		TOTPEnabled: f.totp, PasskeyCount: f.passkeys, RecoveryCodesRemaining: f.recovery,
		SecondFactors: f.secondFactors(), TwoFactorReason: reason, SudoUntil: sudo, CSRFToken: sess.CsrfToken,
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
	if userID > 0 && isSecondFactorStage(stage) {
		h.noteSecondFactor(r.Context(), username, userID, success)
	}
	if userID > 0 && !success && isLoginSecondFactorStage(stage) {
		h.notifySecondFactorFailed(r.Context(), username, userID)
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
		cut := maxUserAgent
		for cut > 0 && !utf8.RuneStart(ua[cut]) { // never cut inside a multi-byte rune
			cut--
		}
		ua = ua[:cut]
	}
	return ua
}

func invalidCredentials(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeInvalidCredentials, "invalid username or password")
}

// presentedSession returns the current session row of the request's principal with TokenHash
// replaced by the hash of the token the client actually presented (recorded by the session
// loader). Passing it to Sessions.Rotate binds the rotation to that token: if the token was
// superseded after the loader ran, the compare-and-swap fails with ErrNoSession.
func (h *Handler) presentedSession(ctx context.Context, p principal.Principal) (store.Session, error) {
	sess, err := h.q.AuthGetSessionByID(ctx, p.SessionID)
	if err != nil {
		return store.Session{}, err
	}
	hash, ok := ctx.Value(presentedTokenKey{}).([]byte)
	if !ok {
		return store.Session{}, ErrNoSession
	}
	sess.TokenHash = hash
	return sess, nil
}

// requireActiveSession runs inside the write transaction of every credential-sensitive change: the
// session that authorized the request must still be active (not revoked, expired or idle) and belong
// to the principal's user, otherwise it returns ErrNoSession.
func (h *Handler) requireActiveSession(ctx context.Context, q *store.Queries, p principal.Principal) error {
	sess, err := q.AuthGetSessionByID(ctx, p.SessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSession
	}
	if err != nil {
		return err
	}
	if sess.UserID != p.UserID || !h.sessions.valid(sess) {
		return ErrNoSession
	}
	return nil
}

// sessionGone is the response when the session was revoked or rotated while the request ran.
func sessionGone(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
}

// sessionError maps a Sessions.Rotate or requireActiveSession error to a response.
func sessionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNoSession):
		sessionGone(w, r)
	case errors.Is(err, errCredentialChanged):
		credentialChanged(w, r)
	default:
		httpx.Internal(w, r, err)
	}
}

// credentialChanged answers a request whose verified password was replaced while it ran.
func credentialChanged(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "the password was changed meanwhile; try again")
}

// busy answers a request whose password check could not get an argon2 slot in time
// (password.ErrBusy). Known and unknown usernames get the identical response.
func busy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "1")
	httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "server busy, try again shortly")
}

func invalidCode(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeInvalidCode, "invalid code")
}
