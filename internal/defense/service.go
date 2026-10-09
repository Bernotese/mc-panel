package defense

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/audit"
	"mcpanel/internal/clock"
	"mcpanel/internal/notify"
	"mcpanel/internal/store"
)

// Rate limits (spec §7.1).
var (
	LoginPerIPLimit   = Limit{Every: 6 * time.Second, Burst: 5}         // 10/min per IP key
	LoginPerUserLimit = Limit{Every: 12 * time.Second, Burst: 5}        // 5/min per username
	SecondFactorLimit = Limit{Every: 12 * time.Second, Burst: 5}        // 5/min per session+IP
	APILimit          = Limit{Every: 200 * time.Millisecond, Burst: 60} // 300/min per session
)

const (
	FailureWindow    = 10 * time.Minute
	FailureThreshold = 5
	JailBase         = 15 * time.Minute
	JailMax          = 7 * 24 * time.Hour
	RepeatWindow     = 30 * 24 * time.Hour
	AttemptRetention = 90 * 24 * time.Hour

	RulesRefreshInterval   = 30 * time.Second
	SyncInterval           = 10 * time.Second
	PruneInterval          = time.Hour
	LimiterCleanupInterval = time.Minute
	LimiterIdle            = 10 * time.Minute
	AgentCallTimeout       = 5 * time.Second
	AgentStatusTimeout     = 2 * time.Second
)

// Deps are the collaborators of the defense service. Agent may be nil
// (firewall sync is then disabled and the agent is reported unreachable).
type Deps struct {
	DB           *sql.DB
	Clock        clock.Clock
	Geo          GeoResolver
	GeoAvailable bool
	Audit        audit.Recorder
	Notifier     notify.Notifier
	Agent        agentapi.Client
	Logger       *slog.Logger
	// Dev enables development conveniences: loopback client IPs are exempt from the admin
	// allowlist only in dev mode (in production the panel sits behind a reverse proxy).
	Dev bool
}

// Service implements LoginGuard, the IP filter, the API rate limit,
// the firewall sync and the security-center API.
type Service struct {
	db           *sql.DB
	q            *store.Queries
	clock        clock.Clock
	geo          GeoResolver
	geoAvailable bool
	audit        audit.Recorder
	notifier     notify.Notifier
	agent        agentapi.Client
	log          *slog.Logger
	dev          bool

	loginIP      *keyedLimiter
	loginUser    *keyedLimiter
	secondFactor *keyedLimiter
	api          *keyedLimiter

	snap   atomic.Pointer[snapshot] // read lock-free by middleware
	snapMu sync.Mutex               // serializes snapshot writers (Reload, addJail)
	jailMu sync.Mutex               // serializes jail decisions
	kick   chan struct{}

	syncMu     sync.Mutex
	unbMu      sync.Mutex     // guards unbannable (also read by the status endpoint)
	unbannable map[int64]bool // jail IDs whose ban failed permanently
}

func NewService(ctx context.Context, d Deps) (*Service, error) {
	if d.DB == nil || d.Clock == nil || d.Audit == nil {
		return nil, errors.New("defense: DB, Clock and Audit are required")
	}
	if d.Geo == nil {
		d.Geo = NopGeo{}
	}
	if d.Notifier == nil {
		d.Notifier = notify.Nop{}
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	s := &Service{
		db: d.DB, q: store.New(d.DB), clock: d.Clock, geo: d.Geo, geoAvailable: d.GeoAvailable,
		audit: d.Audit, notifier: d.Notifier, agent: d.Agent, log: d.Logger, dev: d.Dev,
		loginIP:      newKeyedLimiter(LoginPerIPLimit),
		loginUser:    newKeyedLimiter(LoginPerUserLimit),
		secondFactor: newKeyedLimiter(SecondFactorLimit),
		api:          newKeyedLimiter(APILimit),
		kick:         make(chan struct{}, 1),
		unbannable:   make(map[int64]bool),
	}
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// GeoAvailable reports whether a GeoIP database was loaded.
func (s *Service) GeoAvailable() bool { return s.geoAvailable }

// kickSync asks the firewall sync loop to run soon (non-blocking).
func (s *Service) kickSync() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Service) isUnbannable(id int64) bool {
	s.unbMu.Lock()
	defer s.unbMu.Unlock()
	return s.unbannable[id]
}

func (s *Service) markUnbannable(id int64) {
	s.unbMu.Lock()
	defer s.unbMu.Unlock()
	s.unbannable[id] = true
}
