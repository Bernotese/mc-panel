package servers

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

// curseForgeKeyAAD binds the sealed CurseForge API key to its column.
const curseForgeKeyAAD = "instance_settings.curseforge_api_key"

// settingsTarget is the audit target of server settings changes.
const settingsTarget = "servers"

// Docker status values of the ServerSettings schema.
const (
	dockerOK          = "ok"
	dockerUnavailable = "unavailable"
	dockerUnknown     = "unknown" // the agent did not answer
)

type unknownContainerJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// settingsJSON is the OpenAPI schema ServerSettings. The CurseForge key itself never leaves
// the panel; clients only learn whether one is set.
type settingsJSON struct {
	BaseDomain          *string                `json:"base_domain"`
	CurseForgeAPIKeySet bool                   `json:"curseforge_api_key_set"`
	Docker              string                 `json:"docker"`
	UnknownContainers   []unknownContainerJSON `json:"unknown_containers"`
}

// updateSettingsRequest is the OpenAPI schema UpdateServerSettingsRequest: absent fields stay,
// null removes the value.
type updateSettingsRequest struct {
	BaseDomain       optional[string] `json:"base_domain"`
	CurseForgeAPIKey optional[string] `json:"curseforge_api_key"`
}

func nullString(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func toNullString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

// strOrNil turns an optional string into a JSON-friendly audit value.
func strOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// validCurseForgeKey checks a key with exactly the rule the agent applies (instancespec), by
// validating a minimal CurseForge spec that carries it.
func validCurseForgeKey(key string) error {
	pack := "x"
	s := instancespec.Spec{
		Type: instancespec.TypeCurseForge, Modpack: &pack, Java: instancespec.Java21,
		MemoryMiB: instancespec.MinMemoryMiB, CurseForgeAPIKey: &key,
	}
	return s.Validate()
}

// instanceSettings are the runtime settings with the CurseForge key decrypted.
type instanceSettings struct {
	BaseDomain    *string
	CurseForgeKey *string
}

func (h *Handler) loadSettings(ctx context.Context) (instanceSettings, error) {
	row, err := h.q.ServersGetSettings(ctx)
	if err != nil {
		return instanceSettings{}, err
	}
	out := instanceSettings{BaseDomain: nullString(row.BaseDomain)}
	if len(row.CurseforgeApiKey) > 0 {
		pt, err := h.box.Open(row.CurseforgeApiKey, []byte(curseForgeKeyAAD))
		if err != nil {
			return instanceSettings{}, fmt.Errorf("servers: open curseforge key: %w", err)
		}
		key := string(pt)
		out.CurseForgeKey = &key
	}
	return out, nil
}

func (h *Handler) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	body, err := h.settingsBody(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// settingsBody builds the ServerSettings response. Docker status and unknown containers come
// from a live agent call; an unreachable agent yields docker "unknown" and no containers.
func (h *Handler) settingsBody(ctx context.Context) (settingsJSON, error) {
	row, err := h.q.ServersGetSettings(ctx)
	if err != nil {
		return settingsJSON{}, err
	}
	rows, err := h.q.ServersListInstances(ctx)
	if err != nil {
		return settingsJSON{}, err
	}
	known := make(map[int64]bool, len(rows))
	for _, in := range rows {
		known[in.ServerID] = true
	}
	out := settingsJSON{
		BaseDomain:          nullString(row.BaseDomain),
		CurseForgeAPIKeySet: len(row.CurseforgeApiKey) > 0,
		Docker:              dockerUnknown,
		UnknownContainers:   []unknownContainerJSON{},
	}
	resp, _, err := h.refreshStatus(ctx)
	switch {
	case err == nil && resp.Docker == agentapi.DockerOK:
		out.Docker = dockerOK
	case err == nil || errors.Is(err, agentapi.ErrDockerUnavailable):
		out.Docker = dockerUnavailable
	}
	if err == nil {
		for _, st := range resp.Instances {
			if !known[st.ID] {
				out.UnknownContainers = append(out.UnknownContainers, unknownContainerJSON{ID: st.ID, Name: st.Name})
			}
		}
		slices.SortFunc(out.UnknownContainers, func(a, b unknownContainerJSON) int { return cmp.Compare(a.ID, b.ID) })
	}
	return out, nil
}

func (h *Handler) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req updateSettingsRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	var newBase *string
	if req.BaseDomain.Value != nil {
		d := strings.ToLower(*req.BaseDomain.Value)
		if err := instancespec.ValidBaseDomain(d); err != nil {
			writeValidation(w, r, err.Error())
			return
		}
		newBase = &d
	}
	var sealed []byte // nil removes the key
	if req.CurseForgeAPIKey.Value != nil {
		key := *req.CurseForgeAPIKey.Value
		if err := validCurseForgeKey(key); err != nil {
			writeValidation(w, r, "curseforge_api_key must match ^[A-Za-z0-9$./_-]{1,128}$")
			return
		}
		var err error
		if sealed, err = h.box.Seal([]byte(key), []byte(curseForgeKeyAAD)); err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}
	var (
		oldBase     *string
		baseChanged bool
	)
	now := h.clock.Now().UnixMilli()
	err := h.inTx(ctx, func(q *store.Queries) error {
		cur, err := q.ServersGetSettings(ctx)
		if err != nil {
			return err
		}
		oldBase = nullString(cur.BaseDomain)
		params := store.ServersUpdateSettingsParams{BaseDomain: cur.BaseDomain, CurseforgeApiKey: cur.CurseforgeApiKey, UpdatedAt: now}
		if req.BaseDomain.Set {
			params.BaseDomain = toNullString(newBase)
		}
		if req.CurseForgeAPIKey.Set {
			params.CurseforgeApiKey = sealed
		}
		baseChanged = params.BaseDomain != cur.BaseDomain
		if err := q.ServersUpdateSettings(ctx, params); err != nil {
			return err
		}
		if baseChanged {
			// The FQDN is part of every container (label, route): all instances need a new revision.
			return q.ServersBumpAllRevisions(ctx, now)
		}
		return nil
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	details := map[string]any{}
	if baseChanged {
		details["base_domain"] = map[string]any{"old": strOrNil(oldBase), "new": strOrNil(newBase)}
	}
	if req.CurseForgeAPIKey.Set {
		details["curseforge_api_key"] = "set"
		if req.CurseForgeAPIKey.Value == nil {
			details["curseforge_api_key"] = "removed"
		}
	}
	if len(details) > 0 {
		h.record(ctx, audit.ActionServerSettingsUpdate, settingsTarget, audit.ResultSuccess, details)
	}
	if baseChanged {
		h.Trigger()
	}
	body, err := h.settingsBody(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}
