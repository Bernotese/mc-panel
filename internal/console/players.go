package console

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

const (
	onlineTTL     = 10 * time.Second
	onlineTimeout = 5 * time.Second
	listCommand   = "list uuids"
)

// onlinePlayers is the OnlinePlayers schema.
type onlinePlayers struct {
	Available bool           `json:"available"`
	Online    []onlinePlayer `json:"online"`
	Max       *int           `json:"max"`
}

func unavailablePlayers() onlinePlayers { return onlinePlayers{Online: []onlinePlayer{}} }

// onlineCache keeps the last answer per server for onlineTTL (spec §6.3).
type onlineCache struct {
	mu      sync.Mutex
	entries map[int64]*onlineEntry
}

type onlineEntry struct {
	mu  sync.Mutex // held while one request asks the agent; concurrent ones wait and reuse it
	at  time.Time
	val onlinePlayers
	set bool
}

func newOnlineCache() *onlineCache { return &onlineCache{entries: map[int64]*onlineEntry{}} }

func (c *onlineCache) entry(id int64) *onlineEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok {
		e = &onlineEntry{}
		c.entries[id] = e
	}
	return e
}

// forget drops the cached answer of a server (after a player action changed it).
func (c *onlineCache) forget(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

// handleOnlinePlayers answers who is online; it never fails because of the agent (decision 2).
func (h *Handler) handleOnlinePlayers(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.onlinePlayers(r.Context(), s.ID))
}

func (h *Handler) onlinePlayers(ctx context.Context, id int64) onlinePlayers {
	e := h.online.entry(id)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := h.clock.Now()
	if e.set && now.Sub(e.at) < onlineTTL {
		return e.val
	}
	val, cacheable := h.askOnline(ctx, id)
	if cacheable {
		e.val, e.at, e.set = val, now, true
	}
	return val
}

// askOnline runs "list uuids"; cacheable is false if the caller's context ended meanwhile.
func (h *Handler) askOnline(ctx context.Context, id int64) (onlinePlayers, bool) {
	cctx, cancel := context.WithTimeout(ctx, onlineTimeout)
	defer cancel()
	out, err := h.agent.Rcon(cctx, id, listCommand)
	if err != nil {
		h.log.DebugContext(ctx, "online players unavailable", "server", id, "err", err)
		return unavailablePlayers(), ctx.Err() == nil
	}
	players, maxP, ok := parseList(out)
	if !ok {
		h.log.WarnContext(ctx, "unrecognised answer to list", "server", id)
		return unavailablePlayers(), true
	}
	return onlinePlayers{Available: true, Online: players, Max: &maxP}, true
}
