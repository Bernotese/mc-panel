package servers

import (
	"context"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// propertiesJSON is ServerProperties with every field set (defaults resolved, spec §7).
type propertiesJSON struct {
	MOTD               string  `json:"motd"`
	Difficulty         string  `json:"difficulty"`
	Gamemode           string  `json:"gamemode"`
	MaxPlayers         int     `json:"max_players"`
	PVP                bool    `json:"pvp"`
	Hardcore           bool    `json:"hardcore"`
	AllowFlight        bool    `json:"allow_flight"`
	EnableCommandBlock bool    `json:"enable_command_block"`
	OnlineMode         bool    `json:"online_mode"`
	WhitelistEnabled   bool    `json:"whitelist_enabled"`
	ViewDistance       int     `json:"view_distance"`
	SimulationDistance int     `json:"simulation_distance"`
	SpawnProtection    int     `json:"spawn_protection"`
	Seed               *string `json:"seed,omitempty"` // nil = hidden from the caller (redactFor)
	LevelType          string  `json:"level_type"`
}

func resolvedProperties(name string, p instancespec.Properties) propertiesJSON {
	r := instancespec.Resolve(name, p)
	return propertiesJSON{
		MOTD: *r.MOTD, Difficulty: *r.Difficulty, Gamemode: *r.Gamemode, MaxPlayers: *r.MaxPlayers,
		PVP: *r.PVP, Hardcore: *r.Hardcore, AllowFlight: *r.AllowFlight, EnableCommandBlock: *r.EnableCommandBlock,
		OnlineMode: *r.OnlineMode, WhitelistEnabled: *r.WhitelistEnabled,
		ViewDistance: *r.ViewDistance, SimulationDistance: *r.SimulationDistance, SpawnProtection: *r.SpawnProtection,
		Seed: r.Seed, LevelType: *r.LevelType,
	}
}

// instanceJSON is the OpenAPI schema InstanceConfig.
type instanceJSON struct {
	Type          instancespec.Type `json:"type"`
	Version       string            `json:"version"`
	LoaderVersion *string           `json:"loader_version"`
	Modpack       *string           `json:"modpack"`
	Java          instancespec.Java `json:"java"`
	MemoryMiB     int               `json:"memory_mib"`
	Properties    propertiesJSON    `json:"properties"`
	// Revision is the spec revision; PATCH /servers/{id} takes it back as expected_revision.
	Revision int64 `json:"revision"`
}

// serverJSON is the OpenAPI schema Server; permissions are the caller's effective permissions.
type serverJSON struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Permissions []rbac.Permission `json:"permissions"`
	Address     *string           `json:"address"`
	Instance    *instanceJSON     `json:"instance"`
	Status      statusJSON        `json:"status"`
	// DownAlerts is the per-server switch of the TP5 down/crash-loop alerts; admins only
	// (missing row = on), null for other users.
	DownAlerts *bool `json:"down_alerts"`
	// Web fields (web port paths spec §3.2, applyWebView); web_routes and web_error admins only.
	WebURL    *string         `json:"web_url,omitempty"`
	WebStatus string          `json:"web_status"`
	WebRoutes *[]webRouteJSON `json:"web_routes,omitempty"`
	WebError  string          `json:"web_error,omitempty"`
	// Dev instances: Dev only on a dev, DevID only on a server that has a dev (devData.apply).
	Dev   *devSummaryJSON `json:"dev,omitempty"`
	DevID *int64          `json:"dev_id,omitempty"`
	// Data safety (data safety spec §2.3): the data state and, while busy, the kind of the job.
	DataState State   `json:"data_state"`
	DataJob   *string `json:"data_job"`
}

// webRouteJSON is the OpenAPI schema WebRoute; url is absent without base domain.
type webRouteJSON struct {
	Path string  `json:"path"`
	Port int     `json:"port"`
	URL  *string `json:"url,omitempty"`
}

// viewData is everything a Server response needs besides the server row.
type viewData struct {
	baseDomain *string
	instances  map[int64]store.Instance
	webRoutes  map[int64][]agentapi.WebTarget
	snap       Snapshot
	downAlerts map[int64]bool // server_alerts rows (missing = on)
	dev        devData
	data       DataStates
	// dataBy bounds all data state lookups of one response together (stateTimeout): a files
	// service that hangs makes every later server unknown at once instead of one by one.
	dataBy time.Time
}

func (h *Handler) loadViewData(ctx context.Context) (viewData, error) {
	set, err := h.q.ServersGetSettings(ctx)
	if err != nil {
		return viewData{}, err
	}
	rows, err := h.q.ServersListInstances(ctx)
	if err != nil {
		return viewData{}, err
	}
	d := viewData{
		baseDomain: nullString(set.BaseDomain), instances: make(map[int64]store.Instance, len(rows)), snap: h.snapshot(),
		data: h.data, dataBy: h.ioClock.Now().Add(stateTimeout),
	}
	for _, r := range rows {
		d.instances[r.ServerID] = instanceFromListRow(r)
	}
	routes, err := h.q.ServersListWebRoutes(ctx)
	if err != nil {
		return viewData{}, err
	}
	d.webRoutes = webTargetsByServer(routes)
	alerts, err := h.q.MonitoringListServerAlerts(ctx)
	if err != nil {
		return viewData{}, err
	}
	d.downAlerts = make(map[int64]bool, len(alerts))
	for _, a := range alerts {
		d.downAlerts[a.ServerID] = a.DownAlerts != 0
	}
	if d.dev, err = h.loadDevData(ctx); err != nil {
		return viewData{}, err
	}
	return d, nil
}

func (d viewData) server(ctx context.Context, s store.Server, perms []rbac.Permission) (serverJSON, error) {
	var in *store.Instance
	if row, ok := d.instances[s.ID]; ok {
		in = &row
	}
	out, err := view(s, perms, in, d.webRoutes[s.ID], d.baseDomain, d.snap)
	if err != nil {
		return serverJSON{}, err
	}
	on, ok := d.downAlerts[s.ID]
	if !ok {
		on = true
	}
	out.DownAlerts = &on
	d.dev.apply(&out)
	d.applyData(ctx, &out)
	return out, nil
}

// applyData sets data_state and data_job; a dev whose data is busy (its sync holds it) shows
// sync_pending.
func (d viewData) applyData(ctx context.Context, out *serverJSON) {
	ctx, cancel := context.WithDeadline(ctx, d.dataBy)
	defer cancel()
	info, _ := d.data.Lookup(ctx, out.ID, nil) // an error comes with StateUnknown
	out.DataState = info.State
	if info.State == StateBusy && info.Job != "" {
		job := info.Job
		out.DataJob = &job
	}
	if out.Dev != nil && info.State == StateBusy {
		out.Dev.SyncPending = true
	}
}

// view builds the Server schema; in == nil is a server without instance ("not configured",
// all instance fields null). routes are the server's web routes, root first.
func view(s store.Server, perms []rbac.Permission, in *store.Instance, routes []agentapi.WebTarget, baseDomain *string, snap Snapshot) (serverJSON, error) {
	out := serverJSON{ID: s.ID, Name: s.Name, Permissions: perms, Status: deriveStatus(s.Name, in, snap), DataState: StateSafe}
	applyWebView(&out, s.Name, in, routes, baseDomain, snap.Web)
	if in == nil {
		return out, nil
	}
	c, err := configFromInstance(*in)
	if err != nil {
		return serverJSON{}, err
	}
	if baseDomain != nil {
		addr := s.Name + "." + *baseDomain
		out.Address = &addr
	}
	out.Instance = &instanceJSON{
		Type: c.Type, Version: c.Version, LoaderVersion: c.LoaderVersion, Modpack: c.Modpack,
		Java: c.Java, MemoryMiB: c.MemoryMiB, Properties: resolvedProperties(s.Name, c.Properties),
		Revision: in.Revision,
	}
	return out, nil
}
