package monitoring

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"sync"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store"
)

type Deps struct {
	DB           *sql.DB
	Clock        clock.Clock
	Audit        audit.Recorder
	Notifier     notify.Notifier          // nil = notify.Nop{}
	Instances    agentapi.InstancesClient // nil = no agent: no server samples
	Console      agentapi.ConsoleClient   // RCON for TPS; nil = no TPS
	DataStates   servers.DataStates       // nil = every server's data is safe
	PingAddr     string                   // "" = no ping
	InstancesDir string                   // statfs target; "" = no disk values
	Log          *slog.Logger             // nil = slog.Default()
	// OnPass is called after the alerts of every pass with the same snapshot (dev instances:
	// sleep detection and the dev ops); nil = none.
	OnPass func(ctx context.Context, s Snapshot)
}

// auditTarget is the audit target of the monitoring settings.
const auditTarget = "monitoring"

type Handler struct {
	d      Deps
	q      *store.Queries
	log    *slog.Logger
	coll   *collector
	alerts *alerter
}

// NewHandler normalises Deps (Notifier and Log are never nil afterwards) and builds the
// collector and the alerter with them.
func NewHandler(d Deps) *Handler {
	if d.Notifier == nil {
		d.Notifier = notify.Nop{}
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Handler{d: d, q: store.New(d.DB), log: d.Log, coll: newCollector(d), alerts: newAlerter(d)}
}

// Routes registers the monitoring routes (paths relative to /api, contract V3).
func (h *Handler) Routes(api *rbac.Router) { h.routes(api) }

// Run blocks until ctx is done: the collector (each pass goes to the alerter) and the rollup
// loop.
func (h *Handler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); h.coll.Run(ctx, h.pass) }()
	go func() { defer wg.Done(); runRollup(ctx, h.d) }()
	wg.Wait()
}

// pass hands one snapshot to the alerter, then to Deps.OnPass.
func (h *Handler) pass(ctx context.Context, s Snapshot) {
	h.alerts.Evaluate(ctx, s)
	if h.d.OnPass != nil {
		h.d.OnPass(ctx, s)
	}
}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

// record writes an audit entry (actor and IP from the context); a failing write is logged.
func (h *Handler) record(ctx context.Context, action, target string, res audit.Result, details map[string]any) {
	ctx = context.WithoutCancel(ctx)
	if err := h.d.Audit.Record(ctx, audit.Entry{Action: action, Target: target, Result: res, Details: details}); err != nil {
		h.log.ErrorContext(ctx, "audit record failed", "action", action, "target", target, "err", err)
	}
}
