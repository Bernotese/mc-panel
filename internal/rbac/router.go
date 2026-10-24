package rbac

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

// Checker answers per-server permission questions (implemented in stream D).
type Checker interface {
	HasServerPermission(ctx context.Context, userID, serverID int64, p Permission) (bool, error)
}

type RouteInfo struct {
	Method  string
	Pattern string
	Rule    Rule
}

// Router wraps chi so that every route must declare a Rule.
type Router struct {
	mux     chi.Router
	prefix  string
	checker Checker
	clock   clock.Clock
	reg     *[]RouteInfo
}

func NewRouter(mux chi.Router, checker Checker, c clock.Clock) *Router {
	return &Router{mux: mux, checker: checker, clock: c, reg: &[]RouteInfo{}}
}

func (rt *Router) Get(p string, rule Rule, h http.HandlerFunc) { rt.handle(http.MethodGet, p, rule, h) }
func (rt *Router) Post(p string, rule Rule, h http.HandlerFunc) {
	rt.handle(http.MethodPost, p, rule, h)
}
func (rt *Router) Put(p string, rule Rule, h http.HandlerFunc) { rt.handle(http.MethodPut, p, rule, h) }
func (rt *Router) Patch(p string, rule Rule, h http.HandlerFunc) {
	rt.handle(http.MethodPatch, p, rule, h)
}
func (rt *Router) Delete(p string, rule Rule, h http.HandlerFunc) {
	rt.handle(http.MethodDelete, p, rule, h)
}

// Route mounts a sub-router. Only Phase 2 wiring uses this (once, for "/api").
func (rt *Router) Route(p string, fn func(*Router)) {
	rt.mux.Route(p, func(sub chi.Router) {
		fn(&Router{mux: sub, prefix: rt.prefix + p, checker: rt.checker, clock: rt.clock, reg: rt.reg})
	})
}

func (rt *Router) Routes() []RouteInfo { return slices.Clone(*rt.reg) }

func (rt *Router) handle(method, p string, rule Rule, h http.HandlerFunc) {
	if rule.kind == kindInvalid {
		panic("rbac: route " + method + " " + rt.prefix + p + " registered without a rule")
	}
	full := rt.prefix + p
	if rule.kind == kindServer && !strings.Contains(full, "{"+rule.param+"}") {
		panic("rbac: route " + method + " " + full + " has no path parameter {" + rule.param + "}")
	}
	for _, ri := range *rt.reg {
		if ri.Method == method && ri.Pattern == full {
			panic("rbac: duplicate route " + method + " " + full)
		}
	}
	*rt.reg = append(*rt.reg, RouteInfo{Method: method, Pattern: full, Rule: rule})
	rt.mux.Method(method, p, rt.enforce(rule, h))
}

func (rt *Router) enforce(rule Rule, h http.HandlerFunc) http.HandlerFunc {
	if rule.kind == kindPublic {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := principal.From(r.Context())
		if !ok {
			httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
			return
		}
		if !slices.Contains(rule.stages, p.Stage) {
			switch p.Stage {
			case principal.StageSetup:
				httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeSetupRequired, "two-factor setup required")
			case principal.StagePassword:
				httpx.WriteError(w, r, http.StatusForbidden, httpx.CodePasswordChangeRequired, "password change required")
			default:
				httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication incomplete")
			}
			return
		}
		switch rule.kind {
		case kindAdmin:
			if !p.IsAdmin() {
				httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "admin role required")
				return
			}
		case kindServer:
			if !p.IsAdmin() {
				id, err := strconv.ParseInt(chi.URLParam(r, rule.param), 10, 64)
				if err != nil || id <= 0 {
					httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
					return
				}
				allowed, err := rt.checker.HasServerPermission(r.Context(), p.UserID, id, rule.perm)
				if err != nil {
					httpx.Internal(w, r, err)
					return
				}
				if !allowed {
					httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "missing permission "+string(rule.perm))
					return
				}
			}
		}
		if rule.sudo && !rt.clock.Now().Before(p.SudoUntil) {
			httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeSudoRequired, "re-authentication required")
			return
		}
		h(w, r)
	}
}
