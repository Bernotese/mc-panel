// Package monitoring implements sub-project 5: it samples the servers (agent status, Server
// List Ping through mc-router, TPS/MSPT over RCON) and the host (/proc, statfs) every 30 s,
// keeps 30 days of history in SQLite in three resolutions, evaluates the down, crash-loop and
// disk alerts and serves the values to the dashboard and the monitoring tab.
package monitoring

import (
	"strconv"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/timescale"
)

// Snapshot is the result of one collector pass (V4).
type Snapshot struct {
	At      time.Time
	Agent   bool // the agent answered this pass
	Docker  bool // the agent reported Docker ok
	Servers []ServerSample
	Host    HostSample
}

// ServerSample is one server in a pass; measurements are nil when unknown (V5).
type ServerSample struct {
	ID             int64
	Name           string
	DesiredRunning bool
	DataState      servers.State // data state of the pass (data safety spec §2)
	Gated          bool          // DataState busy or recovery_required: down alerts are silenced
	State          agentapi.ContainerState
	StartedAt      int64 // Unix ms, 0 = not running
	RestartCount   int
	Revision       int64
	Up             *bool // true = valid ping answer, false = running but no answer, nil = no ping
	Players        *int
	PlayersMax     *int
	PlayerNames    []string
	TPS            *float64
	MSPT           *float64
	TPSSource      string // TPSSourceTickQuery | TPSSourcePaper | TPSSourceNone | TPSSourceUnknown
	CPUPercent     *float64
	MemoryBytes    *int64
	MemoryLimit    *int64
}

type HostSample struct {
	CPUPercent  *float64
	Load1       *float64
	MemoryUsed  *int64
	MemoryTotal *int64
	DiskFree    *int64
	DiskTotal   *int64
}

// FiringAlert is an alert that fires right now (alerter.Firing); ServerID 0 = host.
type FiringAlert struct {
	Kind       string
	ServerID   int64
	ServerName string
	Since      time.Time
	Detail     string
}

// HostSeries is the series of the host values; server series are their IDs.
const HostSeries int64 = 0

// Metric names (spec §4); server and host share cpu_percent as a name in different series.
const (
	MetricUp          = "up"
	MetricPlayers     = "players"
	MetricPlayersMax  = "players_max"
	MetricTPS         = "tps"
	MetricMSPT        = "mspt"
	MetricCPU         = "cpu_percent"
	MetricMemory      = "memory_bytes"
	MetricMemoryLimit = "memory_limit_bytes"

	MetricHostCPU   = "cpu_percent"
	MetricMemUsed   = "memory_used"
	MetricMemTotal  = "memory_total"
	MetricLoad1     = "load1"
	MetricDiskFree  = "disk_free"
	MetricDiskTotal = "disk_total"
)

// TPS sources (V3 TpsSource). Paper/purpur fall back to `mspt` only (tps = min(20, 1000/mspt)).
const (
	TPSSourceTickQuery = "tick_query"
	TPSSourcePaper     = "paper"
	TPSSourceNone      = "none"
	TPSSourceUnknown   = "unknown"
)

// Alert kinds (V3 AlertKind) and alert_state keys.
const (
	alertServerDown = "server.down"
	alertCrashLoop  = "server.crash_loop"
	alertDiskLow    = "host.disk_low"
	// alertRecovery: a restore of the server could not be completed (data safety spec §2.3).
	alertRecovery = "server.recovery_required"
)

// Timings and retention (Global Constraints). Waits run on wall time; clock.Clock drives
// timestamps and alert logic. The cadences, the start grace, the reminder and the minute of
// the alert settings are shortened in acceptance builds only (internal/timescale); production
// builds keep exactly these values.
var (
	collectEvery = timescale.D(30 * time.Second)
	tpsEvery     = timescale.D(time.Minute)
	liveTPSEvery = timescale.D(5 * time.Second)
	liveWindow   = timescale.D(30 * time.Second)
	startGrace   = timescale.D(5 * time.Minute)
	remindEvery  = timescale.D(24 * time.Hour)
	// alertMinute is the unit of down_after_min and crash_window_min.
	alertMinute = timescale.D(time.Minute)
)

const (
	rollupEvery      = 5 * time.Minute
	pingTimeout      = 3 * time.Second
	maxParallelPings = 4
	retainRaw        = 24 * time.Hour
	retain5m         = 7 * 24 * time.Hour
	retain1h         = 30 * 24 * time.Hour
)

// alertKey is the alert_state key: "<kind>:<server id>" for server alerts, "<kind>" for the
// host. internal/servers deletes "server.down:<id>" and "server.crash_loop:<id>" literally.
func alertKey(kind string, serverID int64) string {
	if serverID == 0 {
		return kind
	}
	return kind + ":" + strconv.FormatInt(serverID, 10)
}

// Phase A: the constants above are used by B1 (collector, rollup) and B2 (alerts); this keeps
// the unused linter quiet until then. B1/B2 leave it in place (harmless).
var _ = []any{alertServerDown, alertCrashLoop, alertDiskLow, collectEvery, tpsEvery, liveTPSEvery,
	liveWindow, rollupEvery, pingTimeout, maxParallelPings, retainRaw, retain5m, retain1h,
	startGrace, remindEvery, alertKey}
