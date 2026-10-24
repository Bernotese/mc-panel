package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// presentedTokenKey carries the token hash of the cookie the session loader authenticated.
type presentedTokenKey struct{}

const (
	csrfHeader = "X-CSRF-Token"
	loginPath  = "/api/auth/login"
	logoutPath = "/api/auth/logout"
	// BackgroundHeader marks requests the web app makes on its own (polling, refetch on window
	// focus). They are served normally but do not count as activity, so an open tab does not
	// keep an idle session alive (SEC-19). Only GET and HEAD can be background requests.
	BackgroundHeader = "X-MCPanel-Background"
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
		if !s.adminNetworkAllowed(w, r, user) {
			return
		}
		if !isBackground(r) {
			if err := s.Touch(ctx, sess); err != nil {
				httpx.Internal(w, r, err)
				return
			}
		}
		stage, err := s.effectiveStage(ctx, user, sess)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		p := principal.Principal{
			UserID:    user.ID,
			Username:  user.Username,
			Role:      principal.Role(user.Role),
			Stage:     stage,
			SessionID: sess.ID,
			CSRFToken: sess.CsrfToken,
		}
		if sess.SudoUntil > 0 {
			p.SudoUntil = time.UnixMilli(sess.SudoUntil)
		}
		ctx = context.WithValue(ctx, presentedTokenKey{}, sess.TokenHash)
		next.ServeHTTP(w, r.WithContext(principal.With(ctx, p)))
	})
}

func isBackground(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.Header.Get(BackgroundHeader) == "1"
}

// adminNetworkAllowed applies the admin allowlist to every API request of an admin session
// (SEC-16): a stolen cookie, a session from before the setting was enabled or a user promoted
// later must not work from any other network. It writes 403 ip_blocked and returns false when
// the request is refused. Requests for the web app itself and the logout pass.
func (s *Sessions) adminNetworkAllowed(w http.ResponseWriter, r *http.Request, user store.User) bool {
	if s.adminNet == nil || principal.Role(user.Role) != principal.RoleAdmin || !strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	if r.Method == http.MethodPost && r.URL.Path == logoutPath {
		return true // signing out stays possible from anywhere
	}
	err := s.adminNet(r.Context(), httpx.ClientIP(r.Context()))
	switch {
	case err == nil:
		return true
	case errors.Is(err, defense.ErrAdminNetwork):
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeIPBlocked, "admin access from your network is not allowed")
	default:
		httpx.Internal(w, r, err)
	}
	return false
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

// passwordStage applies a forced password change (SEC-45): a signed-in session of a user whose
// password an admin set is limited to changing it, before any 2FA setup. A preauth session stays
// preauth: the second factor comes first.
func passwordStage(user store.User, stage principal.Stage) principal.Stage {
	if user.MustChangePassword == 1 && (stage == principal.StageFull || stage == principal.StageSetup) {
		return principal.StagePassword
	}
	return stage
}

// effectiveStage first applies passwordStage. Otherwise it downgrades a stored full session to
// setup when 2FA is mandatory for the user
// (admin, full-trust permission or enforcement) but the user has no method: a user promoted to
// admin, granted such a permission or newly under enforcement must set up 2FA before using the
// panel. The other way round, a stored setup session of a user without a method continues as
// full once 2FA is optional again (e.g. the permission was revoked): a password login would give
// a full session at that point too.
func (s *Sessions) effectiveStage(ctx context.Context, user store.User, sess store.Session) (principal.Stage, error) {
	stage := principal.Stage(sess.Stage)
	if passwordStage(user, stage) == principal.StagePassword {
		return principal.StagePassword, nil
	}
	if (stage != principal.StageFull && stage != principal.StageSetup) || user.TotpEnabled == 1 {
		return stage, nil
	}
	passkeys, err := s.q.AuthCountCredentials(ctx, user.ID)
	if err != nil || passkeys > 0 {
		return stage, err
	}
	mandatory, err := twoFactorMandatory(ctx, s.q, user)
	if err != nil {
		return stage, err
	}
	if mandatory {
		return principal.StageSetup, nil
	}
	return principal.StageFull, nil
}
