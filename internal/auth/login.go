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
	if h.guard.KnownDevice(ctx, ip, req.Username) {
		// a flood of logins from other addresses cannot take this argon2 slot (SEC-09)
		ctx = password.WithReservedSlot(ctx)
	}
	user, err := h.q.AuthGetUserByUsername(ctx, req.Username)
	if errors.Is(err, sql.ErrNoRows) {
		if errors.Is(password.DummyVerifyContext(ctx, req.Password), password.ErrBusy) {
			busy(w, r)
			return
		}
		h.recordAttempt(r, req.Username, 0, defense.StagePassword, false, reasonUnknownUser)
		invalidCredentials(w, r)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	// Admin network restriction before the password check: a caller outside the allowlist must
	// not learn whether the password was right (same work, same generic 401).
	if principal.Role(user.Role) == principal.RoleAdmin {
		if err := h.guard.CheckAdminNetwork(ctx, ip); err != nil {
			if !errors.Is(err, defense.ErrAdminNetwork) {
				h.guardError(w, r, err)
				return
			}
			if errors.Is(password.DummyVerifyContext(ctx, req.Password), password.ErrBusy) {
				busy(w, r)
				return
			}
			h.recordAttempt(r, user.Username, user.ID, defense.StagePassword, false, reasonAdminNetwork)
			invalidCredentials(w, r)
			return
		}
	}
	ok, rehash, err := password.VerifyContext(ctx, req.Password, user.PasswordHash)
	if errors.Is(err, password.ErrMalformedHash) {
		// Unusable hash (e.g. "!" from user creation without password): equalise timing.
		err = password.DummyVerifyContext(ctx, req.Password)
		ok = false
	}
	if errors.Is(err, password.ErrBusy) {
		busy(w, r)
		return
	}
	if !ok {
		h.recordAttempt(r, user.Username, user.ID, defense.StagePassword, false, reasonBadPassword)
		invalidCredentials(w, r)
		return
	}
	if rehash {
		h.rehashPassword(r, user, req.Password)
	}
	// The stage decision (does the user have a second factor? is one mandatory?) and the session
	// insert run in one write transaction (BEGIN IMMEDIATE), so a concurrent first-factor
	// enrolment cannot slip in between: it either commits before (-> preauth session) or after,
	// and then revokes this session like every other password-only session.
	var (
		stage principal.Stage
		tok   string
		sess  store.Session
	)
	err = h.withTx(ctx, func(q *store.Queries) error {
		u, err := q.AuthGetUserByID(ctx, user.ID)
		if err != nil {
			return err
		}
		user = u
		f, err := h.factors(ctx, q, u)
		if err != nil {
			return err
		}
		stage = principal.StagePreAuth
		if !f.has2FA() {
			mandatory, err := twoFactorMandatory(ctx, q, u)
			if err != nil {
				return err
			}
			stage = principal.StageFull
			if mandatory {
				stage = principal.StageSetup
			}
		}
		if h.afterStageDecision != nil {
			h.afterStageDecision()
		}
		sessions := h.sessions.withQueries(q)
		// Session fixation: never keep a session that was presented with the login request.
		if old, ok := principal.From(ctx); ok {
			if err := sessions.Revoke(ctx, old.SessionID); err != nil {
				return err
			}
		}
		tok, sess, err = sessions.Create(ctx, NewSession{
			UserID: u.ID, Stage: stage, IP: ip, UserAgent: userAgent(r), Country: h.geo.Country(ip),
		})
		return err
	})
	if errors.Is(err, sql.ErrNoRows) { // user vanished meanwhile
		invalidCredentials(w, r)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	reason := reasonSecondFactorPending
	if stage != principal.StagePreAuth {
		reason = reasonComplete // stream B derives new-device notifications from this attempt
	}
	// Only after the session exists: stream B derives the new-device notification from "complete".
	h.recordAttempt(r, user.Username, user.ID, defense.StagePassword, true, reason)
	setSessionCookie(w, tok)
	if stage != principal.StagePreAuth {
		h.auditLog(ctx, user.ID, user.Username, audit.ActionLogin, user.Username, audit.ResultSuccess,
			map[string]any{"method": "password", "stage": string(stage)})
	}
	h.writeMe(w, r, user, sess)
}

func (h *Handler) rehashPassword(r *http.Request, u store.User, pw string) {
	hash, err := password.HashContext(r.Context(), pw)
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
	sess.Stage = string(p.Stage) // the effective stage (mandatory 2FA may downgrade a stored full session)
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
	if p.Stage != principal.StagePreAuth { // a password holder must not be able to flood the audit chain
		h.auditLog(ctx, p.UserID, p.Username, audit.ActionLogout, p.Username, audit.ResultSuccess, nil)
	}
	w.WriteHeader(http.StatusNoContent)
}
