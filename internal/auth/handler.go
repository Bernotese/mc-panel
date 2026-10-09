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

	"mcpanel/internal/audit"
	"mcpanel/internal/clock"
	"mcpanel/internal/config"
	"mcpanel/internal/defense"
	"mcpanel/internal/httpx"
	"mcpanel/internal/notify"
	"mcpanel/internal/principal"
	"mcpanel/internal/rbac"
	"mcpanel/internal/secretbox"
	"mcpanel/internal/store"
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
