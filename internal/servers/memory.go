package servers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

const (
	// memoryReserveMiB stays free for the system, Docker, mc-router and the panel itself.
	memoryReserveMiB = 1024
	// defaultMemoryMiB / defaultModdedMemoryMiB are the suggested sizes of a new server before
	// the host limits apply: plain servers and plugin servers, mod loaders and modpacks.
	defaultMemoryMiB       = 4096
	defaultModdedMemoryMiB = 6144
)

// MaxMemoryMiB is the largest memory_mib a single server may get on a host with hostMiB of
// installed memory: (host − reserve) / 1.25, because the agent lets the container use 25 %
// more than the heap (instances.memoryNumerator/memoryDenominator), rounded down to the
// memory step and kept within instancespec's absolute bounds. hostMiB ≤ 0 means unknown: then
// only instancespec.MaxMemoryMiB applies.
//
// This is the one place of the formula (issue #79); the API, the checks on create/update and
// the form's limits all derive from it.
func MaxMemoryMiB(hostMiB int64) int {
	if hostMiB <= 0 {
		return instancespec.MaxMemoryMiB
	}
	m := (hostMiB - memoryReserveMiB) * 4 / 5
	m -= m % instancespec.MemoryStepMiB
	return int(min(max(m, instancespec.MinMemoryMiB), instancespec.MaxMemoryMiB))
}

// RecommendedMemoryMiB is the suggested memory_mib of a new server of type t: 4 GiB for
// vanilla/paper/purpur, 6 GiB for mod loaders and modpacks, at most half of the memory no
// server has been given yet (host − reserve − allocated), at least instancespec.MinMemoryMiB
// and never above MaxMemoryMiB. allocatedMiB is the sum of memory_mib of all servers.
func RecommendedMemoryMiB(t instancespec.Type, hostMiB, allocatedMiB int64) int {
	rec := int64(defaultMemoryMiB)
	if t.HasLoader() || t.IsModpack() {
		rec = defaultModdedMemoryMiB
	}
	if hostMiB > 0 {
		half := max(hostMiB-memoryReserveMiB-allocatedMiB, 0) / 2
		rec = min(rec, half-half%instancespec.MemoryStepMiB)
	}
	return int(min(max(rec, instancespec.MinMemoryMiB), int64(MaxMemoryMiB(hostMiB))))
}

// memoryLimitsJSON is the OpenAPI schema ServerMemoryLimits.
type memoryLimitsJSON struct {
	MinMiB         int                       `json:"min_mib"`
	MaxMiB         int                       `json:"max_mib"`
	StepMiB        int                       `json:"step_mib"`
	HostTotalMiB   *int64                    `json:"host_total_mib"`
	AllocatedMiB   int64                     `json:"allocated_mib"`
	RecommendedMiB map[instancespec.Type]int `json:"recommended_mib"`
}

// hostMemoryMiB is the installed memory of the host in MiB; 0 when unknown (no reader, macOS,
// unreadable /proc/meminfo).
func (h *Handler) hostMemoryMiB(ctx context.Context) int64 {
	if h.hostMemory == nil {
		return 0
	}
	b, err := h.hostMemory()
	if err != nil {
		h.log.WarnContext(ctx, "servers: host memory unknown, only the static memory limit applies", "err", err)
		return 0
	}
	return int64(b >> 20) //nolint:gosec // G115: MemTotal >> 20 fits into int64 on every real host
}

// maxMemoryMiB is the per-server limit on this host right now.
func (h *Handler) maxMemoryMiB(ctx context.Context) int { return MaxMemoryMiB(h.hostMemoryMiB(ctx)) }

// checkMemoryLimit rejects memory_mib above the host limit (instancespec has checked the
// absolute bounds already).
func checkMemoryLimit(mib, limit int) error {
	if mib > limit {
		return fmt.Errorf("%w: memory_mib: at most %d MiB on this host (installed memory minus %d MiB reserve, divided by 1.25)",
			instancespec.ErrInvalid, limit, memoryReserveMiB)
	}
	return nil
}

func (h *Handler) handleGetMemoryLimits(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := h.q.ServersListInstances(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var allocated int64
	for _, in := range rows {
		allocated += in.MemoryMib
	}
	host := h.hostMemoryMiB(ctx)
	out := memoryLimitsJSON{
		MinMiB: instancespec.MinMemoryMiB, MaxMiB: MaxMemoryMiB(host), StepMiB: instancespec.MemoryStepMiB,
		AllocatedMiB: allocated, RecommendedMiB: make(map[instancespec.Type]int, len(instancespec.Types)),
	}
	if host > 0 {
		out.HostTotalMiB = &host
	}
	for _, t := range instancespec.Types {
		out.RecommendedMiB[t] = RecommendedMemoryMiB(t, host, allocated)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
