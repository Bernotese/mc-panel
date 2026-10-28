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

// The memory budget of a host (issue #158): the agent gives each container
// instancespec.ContainerMemoryNumerator/Denominator (1.25) times its heap, and memoryReserveMiB
// stays free for the host, so the containers of all running servers share host − reserve.
// Every limit, recommendation and the form's overcommit warning derive from these functions;
// they are configured worst cases (container ceilings), not measured usage.

// ContainerMemoryMiB is the container memory limit of a server with a heap of heapMiB.
func ContainerMemoryMiB(heapMiB int64) int64 {
	return heapMiB * instancespec.ContainerMemoryNumerator / instancespec.ContainerMemoryDenominator
}

// heapForContainerMiB is the largest heap (in memory steps) whose container fits into
// containerMiB; ≤ 0 when none does.
func heapForContainerMiB(containerMiB int64) int64 {
	m := containerMiB * instancespec.ContainerMemoryDenominator / instancespec.ContainerMemoryNumerator
	return m - m%instancespec.MemoryStepMiB
}

// containerBudgetMiB is the memory the containers of all servers may use on a host with
// hostMiB > 0: host − reserve, at least 0.
func containerBudgetMiB(hostMiB int64) int64 { return max(hostMiB-memoryReserveMiB, 0) }

// MaxMemoryMiB is the largest memory_mib a single server may get on a host with hostMiB of
// installed memory: the largest heap whose container fits into host − reserve, kept within
// instancespec's absolute bounds. hostMiB ≤ 0 means unknown: then only
// instancespec.MaxMemoryMiB applies.
//
// This is the one place of the formula (issue #79); the API, the checks on create/update and
// the form's limits all derive from it.
func MaxMemoryMiB(hostMiB int64) int {
	if hostMiB <= 0 {
		return instancespec.MaxMemoryMiB
	}
	m := heapForContainerMiB(containerBudgetMiB(hostMiB))
	return int(min(max(m, instancespec.MinMemoryMiB), instancespec.MaxMemoryMiB))
}

// RecommendedMemoryMiB is the suggested memory_mib of a new server of type t: 4 GiB for
// vanilla/paper/purpur, 6 GiB for mod loaders and modpacks, but at most the heap whose
// container takes half of the budget the containers of all servers have not been given yet
// (host − reserve − allocatedContainerMiB), and never above MaxMemoryMiB. If half is less
// than the minimum, the minimum is suggested as long as its container still fits into the
// whole free budget. ok is false when not even that fits: then there is no safe size, and
// the form says so instead of suggesting one. hostMiB ≤ 0 (unknown) means the type default.
func RecommendedMemoryMiB(t instancespec.Type, hostMiB, allocatedContainerMiB int64) (mib int, ok bool) {
	rec := int64(defaultMemoryMiB)
	if t.HasLoader() || t.IsModpack() {
		rec = defaultModdedMemoryMiB
	}
	if hostMiB > 0 {
		free := max(containerBudgetMiB(hostMiB)-allocatedContainerMiB, 0)
		rec = min(rec, heapForContainerMiB(free/2))
		if rec < instancespec.MinMemoryMiB {
			if heapForContainerMiB(free) < instancespec.MinMemoryMiB {
				return 0, false
			}
			rec = instancespec.MinMemoryMiB
		}
	}
	return int(min(rec, int64(MaxMemoryMiB(hostMiB)))), true
}

// memoryLimitsJSON is the OpenAPI schema ServerMemoryLimits.
type memoryLimitsJSON struct {
	MinMiB                   int                        `json:"min_mib"`
	MaxMiB                   int                        `json:"max_mib"`
	StepMiB                  int                        `json:"step_mib"`
	HostTotalMiB             *int64                     `json:"host_total_mib"`
	HostReserveMiB           int64                      `json:"host_reserve_mib"`
	ContainerOverheadPercent int64                      `json:"container_overhead_percent"`
	ContainerBudgetMiB       *int64                     `json:"container_budget_mib"`
	AllocatedMiB             int64                      `json:"allocated_mib"`
	AllocatedContainerMiB    int64                      `json:"allocated_container_mib"`
	RecommendedMiB           map[instancespec.Type]*int `json:"recommended_mib"`
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
	var allocated, allocatedContainer int64
	for _, in := range rows {
		allocated += in.MemoryMib
		allocatedContainer += ContainerMemoryMiB(in.MemoryMib)
	}
	host := h.hostMemoryMiB(ctx)
	out := memoryLimitsJSON{
		MinMiB: instancespec.MinMemoryMiB, MaxMiB: MaxMemoryMiB(host), StepMiB: instancespec.MemoryStepMiB,
		HostReserveMiB: memoryReserveMiB,
		ContainerOverheadPercent: (instancespec.ContainerMemoryNumerator - instancespec.ContainerMemoryDenominator) * 100 /
			instancespec.ContainerMemoryDenominator,
		AllocatedMiB: allocated, AllocatedContainerMiB: allocatedContainer,
		RecommendedMiB: make(map[instancespec.Type]*int, len(instancespec.Types)),
	}
	if host > 0 {
		budget := containerBudgetMiB(host)
		out.HostTotalMiB, out.ContainerBudgetMiB = &host, &budget
	}
	for _, t := range instancespec.Types {
		if mib, ok := RecommendedMemoryMiB(t, host, allocatedContainer); ok {
			out.RecommendedMiB[t] = &mib
		} else {
			out.RecommendedMiB[t] = nil
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
