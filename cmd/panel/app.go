package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/audit"
	"mcpanel/internal/audit/auditapi"
	"mcpanel/internal/auth"
	"mcpanel/internal/clock"
	"mcpanel/internal/config"
	"mcpanel/internal/defense"
	"mcpanel/internal/httpx"
	"mcpanel/internal/notify"
	"mcpanel/internal/rbac"
	"mcpanel/internal/secretbox"
	"mcpanel/internal/users"
	"mcpanel/internal/webui"
)

type appDeps struct {
	Clock clock.Clock
	Agent agentapi.Client
}

type app struct {
	Handler http.Handler
	Mux     *chi.Mux
	Routes  []rbac.RouteInfo
	Run     func(ctx context.Context) // background loops; blocks until ctx is done
	Close   func()                    // releases the GeoIP reader
}

// buildApp wires every stream into one HTTP handler. The middleware order is binding:
// RequestID -> RealIP -> SecurityHeaders -> BodyLimit -> IPFilter -> Sessions -> CSRF -> APIRateLimit.
func buildApp(ctx context.Context, cfg config.Panel, db *sql.DB, d appDeps) (*app, error) {
	key, err := secretbox.LoadOrCreateKey(cfg.SecretKeyPath())
	if err != nil {
		return nil, err
	}
	box, err := secretbox.New(key)
	if err != nil {
		return nil, err
	}
	geo, geoOK, closeGeo := defense.LoadGeo(cfg.GeoIPPath, slog.Default())

	auditRec := audit.NewSQLRecorder(db, d.Clock)
	disp := notify.NewDispatcher(db, box, d.Clock, notify.DispatcherOptions{Dev: cfg.Dev})
	svc, err := defense.NewService(ctx, defense.Deps{
		DB: db, Clock: d.Clock, Geo: geo, GeoAvailable: geoOK,
		Audit: auditRec, Notifier: disp, Agent: d.Agent, Dev: cfg.Dev,
	})
	if err != nil {
		_ = closeGeo()
		return nil, fmt.Errorf("defense: %w", err)
	}
	authH, err := auth.New(auth.Deps{
		DB: db, Config: cfg, Clock: d.Clock, Box: box,
		Guard: svc, Geo: geo, Audit: auditRec, Notifier: disp,
	})
	if err != nil {
		_ = closeGeo()
		return nil, fmt.Errorf("auth: %w", err)
	}
	usersH := users.NewHandler(db, d.Clock, auditRec, disp)
	auditH := auditapi.NewHandler(db, d.Clock, disp)
	defenseH := defense.NewHandler(svc)
	notifyH := notify.NewHandler(notify.HandlerDeps{
		DB: db, Box: box, Dispatcher: disp, Audit: auditRec, Clock: d.Clock, Dev: cfg.Dev,
	})

	mux := chi.NewRouter()
	mux.Use(
		httpx.RequestID,
		httpx.RealIP(cfg.TrustedPrefixes()),
		httpx.SecurityHeaders,
		httpx.BodyLimit(1<<20),
		svc.IPFilter,
		authH.Sessions().Middleware,
		auth.CSRF(cfg.Origin()),
		svc.APIRateLimit,
	)
	// Must be set before the /api sub-router is mounted: chi copies them into the sub-router
	// only if they exist at Mount time. Everything outside /api is served by the SPA catch-all.
	mux.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "no such endpoint")
	})
	mux.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, http.StatusMethodNotAllowed, httpx.CodeBadRequest, "method not allowed")
	})

	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), d.Clock)
	rt.Route("/api", func(api *rbac.Router) {
		api.Get("/health", rbac.Public(), func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
		authH.Routes(api)
		defenseH.Routes(api)
		notifyH.Routes(api)
		usersH.Routes(api)
		auditH.Routes(api)
	})
	mux.Handle("/*", webui.Handler())

	run := func(ctx context.Context) {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); disp.Run(ctx) }()
		go func() { defer wg.Done(); svc.Run(ctx) }()
		wg.Wait()
	}
	return &app{
		Handler: mux,
		Mux:     mux,
		Routes:  rt.Routes(),
		Run:     run,
		Close:   func() { _ = closeGeo() },
	}, nil
}
