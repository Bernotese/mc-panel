package servers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Dev instances (dev instances spec): a server "dev-<name>" whose spec and data follow another
// server ("prod", its source). Whether a server is a dev is decided only by its dev_instances
// row. Guiding principle (spec §1.2): nothing here writes prod's rows; the queries that copy
// onto a dev only match a dev of the given source.

const (
	// DevNamePrefix starts the name of every dev instance; new ordinary servers may not use it.
	DevNamePrefix = "dev-"
	// maxNameLen is the longest server name (instancespec's name rule, a DNS label).
	maxNameLen = 32
	// devMemoryMiB / devModdedMemoryMiB are the default memory of a dev: the minimum a test
	// server needs, more for mod loaders and modpacks (spec §2).
	devMemoryMiB       = 2048
	devModdedMemoryMiB = 3072
	// devMOTDPrefix marks the server list entry of a dev (spec §3.2); maxMOTDRunes is
	// instancespec's motd limit.
	devMOTDPrefix = "[DEV] "
	maxMOTDRunes  = 150
	// DefaultDevSyncTime is the nightly sync at 05:00 (minutes of the day in the backup time
	// zone); DefaultDevIdleMinutes the time without players before a dev sleeps.
	DefaultDevSyncTime    = 300
	DefaultDevIdleMinutes = 15
)

var (
	// ErrDevExists: the server already has a dev instance, or is one (no dev of a dev).
	ErrDevExists = errors.New("servers: the server already has a dev instance or is one")
	// ErrNotDev: the server is not a dev instance.
	ErrNotDev = errors.New("servers: the server is not a dev instance")
	// ErrDevNameTooLong: "dev-" + the source's name would exceed the name limit. It wraps
	// instancespec.ErrInvalid (a validation error).
	ErrDevNameTooLong = fmt.Errorf("%w: the name of a dev instance would exceed %d characters", instancespec.ErrInvalid, maxNameLen)
	// ErrNoInstance: the server (or the source of a dev) is not configured.
	ErrNoInstance = errors.New("servers: server is not configured (no instance)")
	// ErrBaseDomainMissing: no base domain is set in the server settings.
	ErrBaseDomainMissing = errBaseDomainMissing
	// ErrNameTaken: another server already has the dev's name.
	ErrNameTaken = errors.New("servers: server name already taken")
	// ErrInvalidMemory: the requested memory of a dev is out of range or above this host's
	// limit. It wraps instancespec.ErrInvalid; the wrapping error's text is the message for the
	// user.
	ErrInvalidMemory = fmt.Errorf("%w: memory_mib", instancespec.ErrInvalid)
	// ErrServerNotFound: no server with this id.
	ErrServerNotFound = errors.New("servers: server not found")
)

// DevOverrides are the properties a dev keeps over its source's (spec §3.2), stored as JSON in
// dev_instances.property_overrides.
type DevOverrides struct {
	ViewDistance       int `json:"view_distance"`
	SimulationDistance int `json:"simulation_distance"`
}

// DefaultDevOverrides are the overrides of a new dev.
func DefaultDevOverrides() DevOverrides { return DevOverrides{ViewDistance: 4, SimulationDistance: 3} }

// ParseDevOverrides reads dev_instances.property_overrides; a missing value takes its default.
func ParseDevOverrides(raw string) (DevOverrides, error) {
	var ov DevOverrides
	if err := json.Unmarshal([]byte(raw), &ov); err != nil {
		return DevOverrides{}, fmt.Errorf("servers: dev property overrides: %w", err)
	}
	def := DefaultDevOverrides()
	if ov.ViewDistance == 0 {
		ov.ViewDistance = def.ViewDistance
	}
	if ov.SimulationDistance == 0 {
		ov.SimulationDistance = def.SimulationDistance
	}
	return ov, nil
}

// ApplyDevOverrides returns the properties of a dev of the server prodName with properties p:
// p with the overrides and the motd "[DEV] " + prod's resolved motd (at most 150 characters).
// Everything else, the game mode and whitelist included, stays prod's.
func ApplyDevOverrides(prodName string, p instancespec.Properties, ov DevOverrides) instancespec.Properties {
	out := p
	motd := devMOTDPrefix + *instancespec.Resolve(prodName, p).MOTD
	if utf8.RuneCountInString(motd) > maxMOTDRunes {
		motd = string([]rune(motd)[:maxMOTDRunes])
	}
	vd, sd := ov.ViewDistance, ov.SimulationDistance
	out.MOTD, out.ViewDistance, out.SimulationDistance = &motd, &vd, &sd
	return out
}

// DevMemoryMiB is the default memory of a dev of type t on a host whose per-server limit is
// maxMiB.
func DevMemoryMiB(t instancespec.Type, maxMiB int) int {
	m := devMemoryMiB
	if t.HasLoader() || t.IsModpack() {
		m = devModdedMemoryMiB
	}
	return min(m, maxMiB)
}

// checkDevMemory checks a requested dev memory against instancespec's bounds and the host
// limit (MaxMemoryMiB); errors wrap ErrInvalidMemory.
func checkDevMemory(mib, maxMiB int) error {
	if mib < instancespec.MinMemoryMiB || mib > maxMiB || mib%instancespec.MemoryStepMiB != 0 {
		return fmt.Errorf("%w: must be %d..%d MiB in steps of %d MiB on this host",
			ErrInvalidMemory, instancespec.MinMemoryMiB, maxMiB, instancespec.MemoryStepMiB)
	}
	return nil
}

// DevName is the name of the dev of the server prodName.
func DevName(prodName string) string { return DevNamePrefix + prodName }

// NextDevSync is the next nightly sync after now at syncTime (minutes of the day) in loc.
func NextDevSync(now time.Time, loc *time.Location, syncTime int) time.Time {
	t, _ := backups.NextRun(backups.Schedule{Mode: backups.ModeDaily, TimeOfDay: syncTime}, loc, now, time.Time{})
	return t
}

// backupLocation is the time zone of schedules (instance_settings.backup_timezone); UTC if
// the stored zone cannot be loaded.
func backupLocation(row store.InstanceSetting) *time.Location {
	loc, err := time.LoadLocation(row.BackupTimezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// CreateDev writes server, instance (prod's spec + overrides, desired stopped) and
// dev_instances (sleeping, next_sync_at from sync_time) in one transaction. memoryMiB nil takes
// DevMemoryMiB. Errors: ErrServerNotFound, ErrDevExists, ErrDevNameTooLong, ErrNoInstance,
// ErrBaseDomainMissing, ErrNameTaken and validation errors (instancespec.ErrInvalid).
func (h *Handler) CreateDev(ctx context.Context, prodID int64, memoryMiB *int) (store.Server, error) {
	set, err := h.loadSettings(ctx)
	if err != nil {
		return store.Server{}, err
	}
	if set.BaseDomain == nil {
		return store.Server{}, ErrBaseDomainMissing
	}
	row, err := h.q.ServersGetSettings(ctx)
	if err != nil {
		return store.Server{}, err
	}
	loc := backupLocation(row)
	maxMiB := h.maxMemoryMiB(ctx)
	now := h.clock.Now()
	var dev store.Server
	err = h.inTx(ctx, func(q *store.Queries) error {
		prod, err := q.RbacGetServer(ctx, prodID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrServerNotFound
		}
		if err != nil {
			return err
		}
		for _, get := range []func(context.Context, int64) (store.DevInstance, error){q.DevGet, q.DevGetBySource} {
			if _, err := get(ctx, prodID); err == nil {
				return ErrDevExists
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		name := DevName(prod.Name)
		if len(name) > maxNameLen {
			return ErrDevNameTooLong
		}
		if !instancespec.ValidName(name) {
			return fmt.Errorf("%w: name %q is not a valid server name", instancespec.ErrInvalid, name)
		}
		src, err := q.ServersGetInstance(ctx, prodID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoInstance
		}
		if err != nil {
			return err
		}
		c, err := configFromInstance(src)
		if err != nil {
			return err
		}
		ov := DefaultDevOverrides()
		c.Properties = ApplyDevOverrides(prod.Name, c.Properties, ov)
		c.MemoryMiB = DevMemoryMiB(c.Type, maxMiB)
		if memoryMiB != nil {
			if err := checkDevMemory(*memoryMiB, maxMiB); err != nil {
				return err
			}
			c.MemoryMiB = *memoryMiB
		}
		if err := c.validate(set.CurseForgeKey); err != nil {
			return err
		}
		propsJSON, err := c.propertiesJSON()
		if err != nil {
			return err
		}
		ovJSON, err := json.Marshal(ov)
		if err != nil {
			return err
		}
		id, err := q.ServersNextServerID(ctx)
		if err != nil {
			return err
		}
		ms := now.UnixMilli()
		dev, err = q.ServersCreateServer(ctx, store.ServersCreateServerParams{ID: id, Name: name, CreatedAt: ms})
		if store.IsUniqueViolation(err) {
			return ErrNameTaken
		}
		if err != nil {
			return err
		}
		if _, err := q.ServersCreateInstance(ctx, store.ServersCreateInstanceParams{
			ServerID: dev.ID, Type: string(c.Type), Version: c.Version, LoaderVersion: toNullString(c.LoaderVersion),
			Modpack: toNullString(c.Modpack), Java: string(c.Java), MemoryMib: int64(c.MemoryMiB),
			Properties: propsJSON, CreatedAt: ms, UpdatedAt: ms,
		}); err != nil {
			return err
		}
		// A dev starts asleep: the first sync fills it, a connection attempt wakes it.
		if _, err := q.ServersSetDesiredState(ctx, store.ServersSetDesiredStateParams{
			DesiredState: desiredStopped, UpdatedAt: ms, ServerID: dev.ID,
		}); err != nil {
			return err
		}
		next := NextDevSync(now, loc, DefaultDevSyncTime)
		_, err = q.DevCreate(ctx, store.DevCreateParams{
			ServerID: dev.ID, SourceID: prodID, SyncTime: DefaultDevSyncTime, IdleMinutes: DefaultDevIdleMinutes,
			PropertyOverrides: string(ovJSON), NextSyncAt: sql.NullInt64{Int64: next.UnixMilli(), Valid: !next.IsZero()},
			CreatedAt: ms, UpdatedAt: ms,
		})
		return err
	})
	if err != nil {
		return store.Server{}, err
	}
	h.Trigger() // the reconcile pass creates the (stopped) container, which the router needs
	return dev, nil
}

// ApplyDevSpec copies prod's type/version/loader/modpack/java/properties onto the dev,
// applies the overrides stored in dev_instances, keeps the dev's memory and bumps its
// revision; the dev's plugin_installs rows become copies of prod's. It reads prod's rows and
// writes only the dev's. Errors: ErrNotDev, ErrNoInstance and validation errors.
func (h *Handler) ApplyDevSpec(ctx context.Context, devID int64) error {
	set, err := h.loadSettings(ctx)
	if err != nil {
		return err
	}
	// Like a reconcile pass: a spec write must not interleave with a pass that read the old one.
	h.reconcile.mu.Lock()
	defer h.reconcile.mu.Unlock()
	err = h.inTx(ctx, func(q *store.Queries) error {
		d, err := q.DevGet(ctx, devID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotDev
		}
		if err != nil {
			return err
		}
		prod, err := q.RbacGetServer(ctx, d.SourceID)
		if err != nil {
			return err
		}
		src, err := q.ServersGetInstance(ctx, d.SourceID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoInstance
		}
		if err != nil {
			return err
		}
		cur, err := q.ServersGetInstance(ctx, devID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoInstance
		}
		if err != nil {
			return err
		}
		c, err := configFromInstance(src)
		if err != nil {
			return err
		}
		ov, err := ParseDevOverrides(d.PropertyOverrides)
		if err != nil {
			return err
		}
		c.MemoryMiB = int(cur.MemoryMib)
		c.Properties = ApplyDevOverrides(prod.Name, c.Properties, ov)
		if err := c.validate(set.CurseForgeKey); err != nil {
			return err
		}
		propsJSON, err := c.propertiesJSON()
		if err != nil {
			return err
		}
		_, err = q.ServersUpdateDevInstance(ctx, store.ServersUpdateDevInstanceParams{
			Type: string(c.Type), Version: c.Version, LoaderVersion: toNullString(c.LoaderVersion),
			Modpack: toNullString(c.Modpack), Java: string(c.Java), Properties: propsJSON,
			UpdatedAt: h.clock.Now().UnixMilli(), ServerID: devID, SourceID: d.SourceID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotDev
		}
		if err != nil {
			return err
		}
		if err := q.PluginsDeleteDevInstalls(ctx, devID); err != nil {
			return err
		}
		return q.PluginsCopyInstalls(ctx, store.PluginsCopyInstallsParams{TargetID: devID, SourceID: d.SourceID})
	})
	if err != nil {
		return err
	}
	h.Trigger()
	return nil
}

// SetDevSleeping sets dev_instances.sleeping and desired_state (sleeping → stopped, awake →
// running) under the reconcile lock, then triggers a reconcile pass. Waking records now as
// last_wake_at; falling asleep keeps it. Errors: ErrNotDev, ErrNoInstance.
func (h *Handler) SetDevSleeping(ctx context.Context, devID int64, sleeping bool, now time.Time) error {
	h.reconcile.mu.Lock()
	defer h.reconcile.mu.Unlock()
	err := h.inTx(ctx, func(q *store.Queries) error {
		d, err := q.DevGet(ctx, devID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotDev
		}
		if err != nil {
			return err
		}
		desired, wake := desiredStopped, d.LastWakeAt
		if !sleeping {
			desired, wake = desiredRunning, sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
		}
		return setDevState(ctx, q, devID, desired, sleeping, wake, h.clock.Now())
	})
	if err != nil {
		return err
	}
	h.Trigger()
	return nil
}

// setDevState writes the desired state and the sleeping flag of a dev together.
func setDevState(ctx context.Context, q *store.Queries, devID int64, desired string, sleeping bool, wake sql.NullInt64, now time.Time) error {
	n, err := q.ServersSetDesiredState(ctx, store.ServersSetDesiredStateParams{
		DesiredState: desired, UpdatedAt: now.UnixMilli(), ServerID: devID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNoInstance
	}
	return q.DevSetSleeping(ctx, store.DevSetSleepingParams{
		Sleeping: boolInt(sleeping), LastWakeAt: wake, UpdatedAt: now.UnixMilli(), ServerID: devID,
	})
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// DevView is the Server schema of a dev as an admin sees it (201 of POST /servers/{id}/dev).
func (h *Handler) DevView(ctx context.Context, devID int64) (serverJSON, error) {
	s, err := h.q.RbacGetServer(ctx, devID)
	if errors.Is(err, sql.ErrNoRows) {
		return serverJSON{}, ErrServerNotFound
	}
	if err != nil {
		return serverJSON{}, err
	}
	d, err := h.loadViewData(ctx)
	if err != nil {
		return serverJSON{}, err
	}
	if _, ok := d.dev.devs[devID]; !ok {
		return serverJSON{}, ErrNotDev
	}
	return d.server(s, allPerms())
}

// devSummaryJSON is the OpenAPI schema DevSummary.
type devSummaryJSON struct {
	SourceID       int64   `json:"source_id"`
	SourceName     string  `json:"source_name"`
	SyncPaused     bool    `json:"sync_paused"`
	NextSyncAt     *int64  `json:"next_sync_at"`
	LastSyncAt     *int64  `json:"last_sync_at"`
	LastSyncStatus *string `json:"last_sync_status"`
	SyncPending    bool    `json:"sync_pending"`
	Sleeping       bool    `json:"sleeping"`
}

// devData is the dev side of the server views: the summary of every dev and, per source, the
// id of its dev.
type devData struct {
	devs     map[int64]devSummaryJSON
	bySource map[int64]int64
}

// loadDevData reads every dev. A sync is pending while it waits for players to leave, is
// running, or holds the dev's data gate (queued in the backup queue).
func (h *Handler) loadDevData(ctx context.Context) (devData, error) {
	rows, err := h.q.DevList(ctx)
	if err != nil {
		return devData{}, err
	}
	dd := devData{devs: make(map[int64]devSummaryJSON, len(rows)), bySource: make(map[int64]int64, len(rows))}
	for _, r := range rows {
		status := nullString(r.LastSyncStatus)
		v := devSummaryJSON{
			SourceID: r.SourceID, SourceName: r.SourceName, SyncPaused: r.SyncPaused != 0,
			LastSyncAt: nullInt(r.LastSyncAt), LastSyncStatus: status, Sleeping: r.Sleeping != 0,
			SyncPending: r.DeferredSince.Valid || status != nil && *status == "running" || h.gate.DataJobRunning(ctx, r.ServerID),
		}
		if !v.SyncPaused {
			v.NextSyncAt = nullInt(r.NextSyncAt)
		}
		dd.devs[r.ServerID] = v
		dd.bySource[r.SourceID] = r.ServerID
	}
	return dd, nil
}

// apply adds dev (on a dev) and dev_id (on a server that has a dev) to out.
func (dd devData) apply(out *serverJSON) {
	if v, ok := dd.devs[out.ID]; ok {
		out.Dev = &v
	}
	if id, ok := dd.bySource[out.ID]; ok {
		out.DevID = &id
	}
}

func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

// isDev reports whether id is a dev instance.
func isDev(ctx context.Context, q *store.Queries, id int64) (bool, error) {
	_, err := q.DevGet(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// reservedName reports whether a new ordinary server may not use name (spec §2).
func reservedName(name string) bool { return strings.HasPrefix(name, DevNamePrefix) }
