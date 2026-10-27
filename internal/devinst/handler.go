// Package devinst implements dev instances (dev instances spec): a copy of a server ("prod")
// at dev-<name>.<base domain> that is overwritten from a restic snapshot of prod every night,
// sleeps when nobody plays and wakes when a player connects. It owns the dev API, the nightly
// schedule, sleep detection, the dev ops list and the wake loop; the sync itself runs as a job
// of the backup queue (internal/backups) and the spec copy lives in internal/servers.
package devinst

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/timescale"
)

type Deps struct {
	DB       *sql.DB
	Clock    clock.Clock     // nil = clock.Real{}
	Audit    audit.Recorder  // nil = nothing is recorded
	Notifier notify.Notifier // nil = notify.Nop{}
	Servers  Servers
	Syncer   Syncer
	// ServerView renders the Server schema of a server (servers.Handler.DevView) for the 201
	// of POST /servers/{id}/dev.
	ServerView func(ctx context.Context, id int64) (any, error)
	Waker      agentapi.WakerClient   // nil = no wake loop
	Console    agentapi.ConsoleClient // RCON; nil = warnings and ops are not sent
	Gate       servers.DataGate       // nil = never held
	Log        *slog.Logger           // nil = slog.Default()
}

// Timings (dev instances plan, Global Constraints). The minute of the schedule tick and of
// idle_minutes is shortened in acceptance builds only (internal/timescale).
var (
	tickEvery  = timescale.D(time.Minute)
	idleMinute = timescale.D(time.Minute)
)

const (
	fastEvery  = 2 * time.Second  // wake loop and delayed syncs
	deferStep  = 15 * time.Minute // retry of a scheduled sync while players are on dev
	deferMax   = 2 * time.Hour    // after that the scheduled sync runs anyway
	warnDelay  = 60 * time.Second // between the chat warning and the sync
	wakeGap    = 2 * time.Minute  // per dev
	wakeWindow = time.Minute      // panel-wide cap window
	wakeCap    = 10               // wakes per wakeWindow
	// manualCooldown is the least time between two manual syncs of one dev, from the end of
	// the previous one: each forces a fresh backup of prod and holds the panel-wide queue.
	manualCooldown = 10 * time.Minute
	alertEvery     = 6 * time.Hour    // dev.sync_failed per dev
	callTimeout    = 10 * time.Second // DB writes of callbacks, agent calls
	rconTimeout    = 15 * time.Second
)

// Sync triggers (backups.DevSync.Trigger, audit detail of dev.sync).
const (
	triggerScheduled = "scheduled"
	triggerManual    = "manual"
	triggerCreate    = "create"
)

type Handler struct {
	q        *store.Queries
	clock    clock.Clock
	rec      audit.Recorder
	notifier notify.Notifier
	servers  Servers
	syncer   Syncer
	view     func(ctx context.Context, id int64) (any, error)
	waker    agentapi.WakerClient
	console  agentapi.ConsoleClient
	gate     servers.DataGate
	log      *slog.Logger

	tickEvery, fastEvery time.Duration

	// writeMu serialises the schedule writes of dev_instances (PATCH, tick, sync results), so a
	// sync result never overwrites a concurrent pause with a stale next_sync_at.
	writeMu sync.Mutex
	tickMu  sync.Mutex // serialises ticks (Run and tests)
	lastTZ  string     // time zone of the previous tick ("" before the first; guarded by tickMu)

	mu       sync.Mutex
	passSeen bool // a monitoring pass arrived (before it, player counts are unknown)
	devs     map[int64]*devState
	delayed  []delayedSync
	wakes    []time.Time // panel-wide wakes within the last wakeWindow
}

// devState is what the handler remembers about one dev between passes and requests.
type devState struct {
	seen       bool  // a monitoring pass listed it
	running    bool  // state running in the last pass
	players    *int  // of the last pass; nil = unknown
	startedAt  int64 // StartedAt of the current run (Unix ms)
	lastActive time.Time
	slept      bool  // sleep already ordered for this run
	opsFor     int64 // StartedAt the ops list was applied for
	pending    bool  // a sync is waiting (delayed) or queued; cleared by its Done
	lastWake   time.Time
	lastAlert  time.Time
	lastManual time.Time // start of the last manual sync, its end once done (manualCooldown)
}

// delayedSync is a sync that waits warnDelay after its chat warning.
type delayedSync struct {
	devID   int64
	trigger string
	at      time.Time
	paused  bool            // sync_paused when the wait began: a pause during the wait drops it
	ctx     context.Context // keeps actor and IP of the request for the audit entry
}

func NewHandler(d Deps) *Handler {
	h := &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, notifier: d.Notifier, servers: d.Servers,
		syncer: d.Syncer, view: d.ServerView, waker: d.Waker, console: d.Console, gate: d.Gate, log: d.Log,
		tickEvery: tickEvery, fastEvery: fastEvery, devs: map[int64]*devState{},
	}
	if h.clock == nil {
		h.clock = clock.Real{}
	}
	if h.rec == nil {
		h.rec = nopRecorder{}
	}
	if h.notifier == nil {
		h.notifier = notify.Nop{}
	}
	if h.gate == nil {
		h.gate = noGate{}
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	return h
}

// Routes registers the dev API (paths relative to /api).
func (h *Handler) Routes(api *rbac.Router) {
	api.Post("/servers/{id}/dev", rbac.Admin().WithSudo(), h.handleCreate)
	api.Get("/servers/{id}/dev", rbac.Server(rbac.PermServerView, "id"), h.handleGet)
	api.Patch("/servers/{id}/dev", rbac.Server(rbac.PermServerFiles, "id"), h.handleUpdate)
	api.Post("/servers/{id}/dev/sync", rbac.Server(rbac.PermServerFiles, "id"), h.handleSync)
	api.Get("/servers/{id}/dev/ops", rbac.Server(rbac.PermServerConsole, "id"), h.handleListOps)
	api.Post("/servers/{id}/dev/ops", rbac.Server(rbac.PermServerConsole, "id"), h.handleAddOp)
	api.Delete("/servers/{id}/dev/ops/{name}", rbac.Server(rbac.PermServerConsole, "id"), h.handleRemoveOp)
}

// Run blocks until ctx is done: recovery first, then the minute tick (due syncs, deferrals)
// and the 2-second loop (delayed syncs, waker).
func (h *Handler) Run(ctx context.Context) {
	h.recover(ctx)
	slow := time.NewTicker(h.tickEvery)
	defer slow.Stop()
	fast := time.NewTicker(h.fastEvery)
	defer fast.Stop()
	h.tick(ctx)
	h.fastTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-slow.C:
			h.tick(ctx)
		case <-fast.C:
			h.fastTick(ctx)
		}
	}
}

// fastTick runs the delayed syncs that are due and one round of the wake loop.
func (h *Handler) fastTick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	h.runDelayed(ctx)
	h.wakeTick(ctx)
}

// state returns the state of devID; call with h.mu held.
func (h *Handler) state(devID int64) *devState {
	st := h.devs[devID]
	if st == nil {
		st = &devState{}
		h.devs[devID] = st
	}
	return st
}

// dev is a loaded dev instance with its own and its source's name.
type dev struct {
	row        store.DevInstance
	name       string
	sourceName string
}

// loadServer returns the server of the {id} path parameter; it writes 404 for malformed ids
// and unknown servers.
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request) (store.Server, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeServerNotFound(w, r)
		return store.Server{}, false
	}
	s, err := h.q.RbacGetServer(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeServerNotFound(w, r)
		return store.Server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Server{}, false
	}
	return s, true
}

// loadDev returns the dev of the {id} path parameter; it writes 404 for unknown servers and
// 409 not_dev for a server that is not a dev instance.
func (h *Handler) loadDev(w http.ResponseWriter, r *http.Request) (dev, bool) {
	s, ok := h.loadServer(w, r)
	if !ok {
		return dev{}, false
	}
	d, err := h.getDev(r.Context(), s.ID)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeNotDev, "the server is not a dev instance")
		return dev{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return dev{}, false
	}
	return d, true
}

// getDev loads the dev row of id with its own and its source's name (sql.ErrNoRows = no dev).
func (h *Handler) getDev(ctx context.Context, id int64) (dev, error) {
	row, err := h.q.DevGet(ctx, id)
	if err != nil {
		return dev{}, err
	}
	s, err := h.q.RbacGetServer(ctx, id)
	if err != nil {
		return dev{}, err
	}
	src, err := h.q.RbacGetServer(ctx, row.SourceID)
	if err != nil {
		return dev{}, err
	}
	return dev{row: row, name: s.Name, sourceName: src.Name}, nil
}

func writeServerNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
}

func writeValidation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

func result(err error) audit.Result {
	if err != nil {
		return audit.ResultFailure
	}
	return audit.ResultSuccess
}

// record writes an audit entry; actor and IP come from ctx (a background action has none and
// is recorded as the system). A failing audit write is logged.
func (h *Handler) record(ctx context.Context, action string, serverID int64, res audit.Result, details map[string]any) {
	h.recordEntry(ctx, audit.Entry{Action: action, Target: serverTarget(serverID), Result: res, Details: details})
}

func (h *Handler) recordEntry(ctx context.Context, e audit.Entry) {
	ctx = context.WithoutCancel(ctx)
	if err := h.rec.Record(ctx, e); err != nil {
		h.log.ErrorContext(ctx, "audit record failed", "action", e.Action, "target", e.Target, "err", err)
	}
}

func nullTime(t time.Time) sql.NullInt64 { return sql.NullInt64{Int64: t.UnixMilli(), Valid: true} }

func msPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

func strPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

type nopRecorder struct{}

func (nopRecorder) Record(context.Context, audit.Entry) error { return nil }

type noGate struct{}

func (noGate) DataJobRunning(context.Context, int64) bool { return false }
