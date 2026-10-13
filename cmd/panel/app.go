package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/audit/auditapi"
	"github.com/Bernotese/mc-panel/internal/auth"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/monitoring"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
	"github.com/Bernotese/mc-panel/internal/users"
	"github.com/Bernotese/mc-panel/internal/webui"
)

// sessionPruneInterval is how often ended sessions older than auth.SessionRetention are deleted.
const sessionPruneInterval = time.Hour

// pruneSessionsLoop calls prune once immediately and then every interval until ctx is done.
func pruneSessionsLoop(ctx context.Context, prune func(context.Context) (int64, error), every time.Duration) {
	once := func() {
		n, err := prune(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			slog.Error("prune sessions", "err", err)
		case n > 0:
			slog.Info("pruned old sessions", "count", n)
		}
	}
	once()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			once()
		}
	}
}

type appDeps struct {
	Clock clock.Clock
	Agent agentapi.Client
	// Instances is the agent's instance API (in production the same *agent.Client as Agent);
	// nil means no agent: the panel works, instance states are "unknown".
	Instances agentapi.InstancesClient
	// Console is the agent's console API (logs, RCON, player lists; the same *agent.Client);
	// nil means no agent: console routes answer agent_unavailable.
	Console agentapi.ConsoleClient
	// Files is the files service (mcpanel-files); nil means no service: file routes answer
	// files_unavailable.
	Files filesapi.Client
	// Backups is the backup API of the files service (in production the same *filesapi.UnixClient
	// as Files); nil means no service: the backups tab reports "not available".
	Backups filesapi.BackupClient
	// Upstream talks to Modrinth and Hangar; nil means upstream.New with the default bases (the
	// real services). Tests inject httptest servers.
	Upstream *upstream.Client
	Version  string // panel version, sent in the User-Agent of catalog and plugin requests
}

type app struct {
	Handler  http.Handler
	Mux      *chi.Mux
	Routes   []rbac.RouteInfo
	Run      func(ctx context.Context) // background loops; blocks until ctx is done
	Close    func()                    // ends console streams, releases the GeoIP reader
	Shutdown func()                    // ends open console streams (http.Server.RegisterOnShutdown)
}

// buildApp wires every stream into one HTTP handler. The middleware order is binding:
// RequestID -> RealIP -> SecurityHeaders -> BodyLimit (except the file upload stream) -> IPFilter ->
// Sessions -> CSRF -> APIRateLimit.
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
	// The files handler is one of the servers handler's data gates: no start while an archive
	// is extracted into a server (files spec §5.1).
	filesH := serverfiles.NewHandler(serverfiles.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Files: d.Files, Instances: d.Instances,
	})
	// One version catalog for the create form and for resolving LATEST in the plugin search.
	cat := catalog.New(catalog.Options{Clock: d.Clock, Version: d.Version})
	// The backups handler needs servers for an immediate reconcile pass after a restore, the
	// servers handler needs backups as data gate and purger: serversH is assigned right below.
	var serversH *servers.Handler
	backupsH := backups.NewHandler(backups.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Notifier: disp, Files: d.Backups,
		Instances: d.Instances, Console: d.Console,
		Reconcile: func() { serversH.Trigger() },
	})
	serversH = servers.NewHandler(servers.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Box: box, Instances: d.Instances,
		Catalog: cat,
		// The panel's own restore hold first (a map lookup), then the file service's jobs.
		Gate:    servers.AnyGate(backupsH, filesH),
		Backups: backupsH,
	})
	// Monitoring reads the agent status, pings through mc-router and asks RCON for TPS; the
	// data gates keep down alerts quiet while an extract or a restore holds the server.
	monitoringH := monitoring.NewHandler(monitoring.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Notifier: disp, Instances: d.Instances, Console: d.Console,
		Gate: servers.AnyGate(backupsH, filesH), PingAddr: cfg.MonitoringPingAddr, InstancesDir: cfg.InstancesDir,
	})
	pluginsH := plugins.NewHandler(plugins.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Files: d.Files, Instances: d.Instances,
		Catalog: cat, Upstream: d.Upstream, Version: d.Version,
	})
	checker := rbac.NewDBChecker(db)
	consoleH := console.NewHandler(console.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Agent: d.Console,
		Access: console.NewAccess(authH.Sessions(), checker),
	})
	auditH := auditapi.NewHandler(db, d.Clock, disp)
	defenseH := defense.NewHandler(svc)
	notifyH := notify.NewHandler(notify.HandlerDeps{
		DB: db, Box: box, Dispatcher: disp, Audit: auditRec, Clock: d.Clock, Dev: cfg.Dev,
	})

	mux := chi.NewRouter()
	mux.Use(
		httpx.RequestID,
		httpx.RealIP(cfg.TrustedPrefixes()),
		httpx.SecurityHeaders(cfg.Dev),
		httpx.BodyLimitExcept(1<<20, serverfiles.UnlimitedBody),
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

	rt := rbac.NewRouter(mux, checker, d.Clock)
	rt.Route("/api", func(api *rbac.Router) {
		api.Get("/health", rbac.Public(), func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
		authH.Routes(api)
		defenseH.Routes(api)
		notifyH.Routes(api)
		usersH.Routes(api)
		serversH.Routes(api)
		consoleH.Routes(api)
		filesH.Routes(api)
		pluginsH.Routes(api)
		backupsH.Routes(api)
		monitoringH.Routes(api)
		auditH.Routes(api)
	})
	mux.Handle("/*", webui.Handler())

	run := func(ctx context.Context) {
		var wg sync.WaitGroup
		wg.Add(7)
		go func() { defer wg.Done(); disp.Run(ctx) }()
		go func() { defer wg.Done(); svc.Run(ctx) }()
		go func() { defer wg.Done(); serversH.Run(ctx) }()
		go func() { defer wg.Done(); pluginsH.Run(ctx) }()
		go func() { defer wg.Done(); backupsH.Run(ctx) }()
		go func() { defer wg.Done(); monitoringH.Run(ctx) }()
		go func() { defer wg.Done(); pruneSessionsLoop(ctx, authH.PruneSessions, sessionPruneInterval) }()
		wg.Wait()
	}
	return &app{
		Handler:  mux,
		Mux:      mux,
		Routes:   rt.Routes(),
		Run:      run,
		Close:    func() { consoleH.Shutdown(); _ = closeGeo() },
		Shutdown: consoleH.Shutdown,
	}, nil
}
