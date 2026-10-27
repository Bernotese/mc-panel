package devinst

import (
	"context"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/monitoring"
)

// OnPass gets every monitoring pass (monitoring.Deps.OnPass). For each running dev it keeps
// the idle clock and puts the dev to sleep after idle_minutes without players, and it applies
// the dev ops list once per start as soon as the server answers.
//
// The idle clock starts at the container's start (so a fresh wake is not put back to sleep
// before anyone could join), or at the first pass for a dev this process has not seen before
// (players may have left a minute before a panel restart). A pass with players or with an
// unknown count (failed ping, no agent) resets it: unknown never counts as empty. A gated dev
// (a sync or restore rewrites its data) is never put to sleep here.
func (h *Handler) OnPass(ctx context.Context, s monitoring.Snapshot) {
	rows, err := h.q.DevList(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "devinst: list devs", "err", err)
		return
	}
	now := s.At
	if now.IsZero() {
		now = h.clock.Now()
	}
	samples := make(map[int64]monitoring.ServerSample, len(s.Servers))
	for _, smp := range s.Servers {
		samples[smp.ID] = smp
	}
	var sleep, ops []int64
	var opsStart = map[int64]int64{}
	h.mu.Lock()
	known := make(map[int64]bool, len(rows))
	for _, row := range rows {
		id := row.ServerID
		known[id] = true
		st := h.state(id)
		smp, ok := samples[id]
		if !ok || smp.State != agentapi.StateRunning {
			st.seen, st.running, st.players, st.startedAt = true, false, nil, 0
			continue
		}
		st.running, st.players = true, smp.Players
		if smp.StartedAt != st.startedAt || !st.seen {
			first := !st.seen || smp.StartedAt == 0
			st.seen, st.startedAt, st.slept = true, smp.StartedAt, false
			st.lastActive = time.UnixMilli(smp.StartedAt)
			if first {
				st.lastActive = now
			}
		}
		if smp.Players == nil || *smp.Players > 0 {
			if now.After(st.lastActive) {
				st.lastActive = now
			}
		} else if !smp.Gated && !st.slept && now.Sub(st.lastActive) >= time.Duration(row.IdleMinutes)*idleMinute {
			st.slept = true
			sleep = append(sleep, id)
		}
		if smp.Up != nil && *smp.Up && st.opsFor != smp.StartedAt {
			ops = append(ops, id)
			opsStart[id] = smp.StartedAt
		}
	}
	for id, st := range h.devs { // deleted devs
		if !known[id] && !st.pending {
			delete(h.devs, id)
		}
	}
	h.mu.Unlock()

	for _, id := range sleep {
		if err := h.servers.SetDevSleeping(ctx, id, true, now); err != nil {
			h.log.WarnContext(ctx, "devinst: put a dev to sleep", "server_id", id, "err", err)
			h.mu.Lock()
			if st := h.devs[id]; st != nil {
				st.slept = false // the next pass tries again
			}
			h.mu.Unlock()
			continue
		}
		h.log.InfoContext(ctx, "devinst: dev fell asleep", "server_id", id)
	}
	for _, id := range ops {
		if !h.applyOps(ctx, id) {
			continue
		}
		h.mu.Lock()
		if st := h.devs[id]; st != nil {
			st.opsFor = opsStart[id]
		}
		h.mu.Unlock()
	}
}
