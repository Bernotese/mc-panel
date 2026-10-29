package devinst

import (
	"context"
	"errors"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/servers/admit"
)

// wakeTick sends the complete list of sleeping devs to the agent (PUT /v1/waker; the agent
// routes their hostnames to the waker) and wakes the devs a player tried to join. The list
// comes from the database on every tick, so a restarted agent or panel has it again within
// one tick. Agent errors are retried on the next tick.
func (h *Handler) wakeTick(ctx context.Context) {
	if h.waker == nil {
		return
	}
	rows, err := h.q.DevListSleeping(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "devinst: list sleeping devs", "err", err)
		return
	}
	settings, err := h.q.ServersGetSettings(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "devinst: read the base domain", "err", err)
		return
	}
	req := agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{}}
	sleeping := make(map[int64]bool, len(rows))
	if settings.BaseDomain.Valid && settings.BaseDomain.String != "" { // without one nothing is routed
		for _, r := range rows {
			if len(req.Sleeping) == agentapi.MaxWakerTargets {
				h.log.WarnContext(ctx, "devinst: too many sleeping devs for the waker", "max", agentapi.MaxWakerTargets)
				break
			}
			req.Sleeping = append(req.Sleeping, agentapi.WakerTarget{ID: r.ServerID, FQDN: r.Name + "." + settings.BaseDomain.String})
			sleeping[r.ServerID] = true
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	resp, err := h.waker.Waker(callCtx, req)
	cancel()
	if err != nil {
		h.log.DebugContext(ctx, "devinst: waker", "err", err)
		return
	}
	if !resp.Ready {
		h.log.DebugContext(ctx, "devinst: the waker is not ready")
	}
	for _, id := range resp.Wakes {
		if sleeping[id] {
			h.wake(ctx, id)
		}
	}
}

// wake wakes devID unless its data is not safe (a sync or restore rewrites it, a restore is
// unresolved or the files service does not answer; data safety spec §3), it was woken less
// than wakeGap ago, or wakeCap wakes happened panel-wide within wakeWindow. The wish to run is
// stored under the dev's admission lock (like a user's start) and the reconcile loop admits
// the start itself again.
func (h *Handler) wake(ctx context.Context, devID int64) {
	if st, err := h.data.State(ctx, devID); st != servers.StateSafe {
		h.log.DebugContext(ctx, "devinst: wake ignored, the dev's data is not safe", "server_id", devID, "state", st, "err", err)
		return
	}
	now := h.clock.Now()
	h.mu.Lock()
	st := h.state(devID)
	if !st.lastWake.IsZero() && now.Sub(st.lastWake) < wakeGap {
		h.mu.Unlock()
		return
	}
	recent := h.wakes[:0]
	for _, t := range h.wakes {
		if now.Sub(t) < wakeWindow {
			recent = append(recent, t)
		}
	}
	h.wakes = recent
	if len(h.wakes) >= wakeCap {
		h.mu.Unlock()
		h.log.WarnContext(ctx, "devinst: wake limit reached", "server_id", devID, "limit", wakeCap)
		return
	}
	prevWake := st.lastWake
	st.lastWake = now
	h.wakes = append(h.wakes, now)
	h.mu.Unlock()

	err := h.admit(ctx, devID, func(ctx context.Context) error { return h.servers.SetDevSleeping(ctx, devID, false, now) })
	if err != nil {
		if refused := errors.Is(err, admit.ErrAdmissionBusy) || errors.Is(err, admit.ErrDataBusy) ||
			errors.Is(err, admit.ErrRecoveryRequired) || errors.Is(err, admit.ErrDataStateUnknown) || errors.Is(err, admit.ErrDeleted); refused {
			h.log.DebugContext(ctx, "devinst: wake not admitted", "server_id", devID, "err", err)
		} else {
			h.log.WarnContext(ctx, "devinst: wake a dev", "server_id", devID, "err", err)
		}
		h.mu.Lock()
		st.lastWake = prevWake // a failed wake does not use up the limits
		for i := len(h.wakes) - 1; i >= 0; i-- {
			if h.wakes[i].Equal(now) {
				h.wakes = append(h.wakes[:i], h.wakes[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
		return
	}
	h.recordEntry(ctx, audit.Entry{
		Action: audit.ActionDevWake, Target: serverTarget(devID), Result: audit.ResultSuccess, ActorType: audit.ActorSystem,
		ActorName: "system",
	})
}

// admit runs fn under the admission of server id (Deps.Admit).
func (h *Handler) admit(ctx context.Context, id int64, fn func(context.Context) error) error {
	if h.admitFn == nil {
		return fn(ctx)
	}
	return h.admitFn(ctx, id, fn)
}
