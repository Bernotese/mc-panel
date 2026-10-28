package servers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

const gib = 1 << 30

func TestMaxMemoryMiB(t *testing.T) {
	for host, want := range map[int64]int{
		0:      instancespec.MaxMemoryMiB, // unknown
		-1:     instancespec.MaxMemoryMiB,
		16384:  12288, // (16 GiB − 1 GiB) / 1.25
		15974:  11776, // a "16 GB" VPS: rounded down to 256 MiB
		4096:   2304,
		2048:   instancespec.MinMemoryMiB,
		1024:   instancespec.MinMemoryMiB,
		65536:  instancespec.MaxMemoryMiB,
		262144: instancespec.MaxMemoryMiB,
	} {
		if got := servers.MaxMemoryMiB(host); got != want {
			t.Errorf("MaxMemoryMiB(%d) = %d, want %d", host, got, want)
		}
	}
}

func TestContainerMemoryMiB(t *testing.T) {
	for heap, want := range map[int64]int64{0: 0, 1024: 1280, 4096: 5120, 7168: 8960, 12288: 15360, 32768: 40960} {
		if got := servers.ContainerMemoryMiB(heap); got != want {
			t.Errorf("ContainerMemoryMiB(%d) = %d, want %d", heap, got, want)
		}
	}
	// The per-server maximum is the largest heap whose container fits into host − reserve.
	for _, host := range []int64{2048, 4096, 15974, 16384, 26624} {
		m := int64(servers.MaxMemoryMiB(host))
		if m > instancespec.MinMemoryMiB && (servers.ContainerMemoryMiB(m) > host-1024 || servers.ContainerMemoryMiB(m+instancespec.MemoryStepMiB) <= host-1024) {
			t.Errorf("MaxMemoryMiB(%d) = %d is not the largest heap within the budget", host, m)
		}
	}
}

func TestRecommendedMemoryMiB(t *testing.T) {
	cases := []struct {
		typ             instancespec.Type
		host, allocated int64 // allocated: container MiB of all servers
		want            int
		ok              bool
	}{
		{instancespec.TypePaper, 0, 0, 4096, true},
		{instancespec.TypeVanilla, 0, 99999, 4096, true}, // unknown host: only the type decides
		{instancespec.TypeFabric, 0, 0, 6144, true},
		{instancespec.TypeNeoForge, 65536, 0, 6144, true},
		{instancespec.TypeModrinth, 16384, 0, 6144, true},
		// half of the free budget 16384 − 1024 − 5120 = 10240 is 5120 MiB of container, 4096 heap
		{instancespec.TypeCurseForge, 16384, 5120, 4096, true},
		{instancespec.TypePaper, 16384, 5120, 4096, true},
		// free 15360 − 12800 = 2560: half 1280 = heap 1024 exactly
		{instancespec.TypePurpur, 16384, 12800, 1024, true},
		// free 2304: half 1152 = heap 921 < minimum, but 1024 (container 1280) still fits
		{instancespec.TypePaper, 16384, 13056, 1024, true},
		// free exactly 1280: the minimum fits exactly
		{instancespec.TypePaper, 16384, 14080, 1024, true},
		// free 1279: not even the minimum fits
		{instancespec.TypePaper, 16384, 14081, 0, false},
		{instancespec.TypePaper, 16384, 15360, 0, false}, // nothing left
		{instancespec.TypePaper, 16384, 40000, 0, false}, // overcommitted
		{instancespec.TypeFabric, 4096, 0, 1024, true},   // free 3072: half 1536 = heap 1228 → 1024
		{instancespec.TypePaper, 2048, 0, 0, false},      // free 1024 < 1280: no server fits this host safely
	}
	for _, c := range cases {
		got, ok := servers.RecommendedMemoryMiB(c.typ, c.host, c.allocated)
		if got != c.want || ok != c.ok {
			t.Errorf("RecommendedMemoryMiB(%s, %d, %d) = %d, %v; want %d, %v", c.typ, c.host, c.allocated, got, ok, c.want, c.ok)
		}
		if ok && (got > servers.MaxMemoryMiB(c.host) || got < instancespec.MinMemoryMiB || got%instancespec.MemoryStepMiB != 0) {
			t.Errorf("RecommendedMemoryMiB(%s, %d, %d) = %d breaks the limits", c.typ, c.host, c.allocated, got)
		}
		if ok && c.host > 0 && servers.ContainerMemoryMiB(int64(got)) > c.host-1024-c.allocated {
			t.Errorf("RecommendedMemoryMiB(%s, %d, %d) = %d does not fit the free budget", c.typ, c.host, c.allocated, got)
		}
	}
}

type memoryLimitsResp struct {
	MinMiB                   int             `json:"min_mib"`
	MaxMiB                   int             `json:"max_mib"`
	StepMiB                  int             `json:"step_mib"`
	HostTotalMiB             *int64          `json:"host_total_mib"`
	HostReserveMiB           int64           `json:"host_reserve_mib"`
	ContainerOverheadPercent int             `json:"container_overhead_percent"`
	ContainerBudgetMiB       *int64          `json:"container_budget_mib"`
	AllocatedMiB             int64           `json:"allocated_mib"`
	AllocatedContainerMiB    int64           `json:"allocated_container_mib"`
	RecommendedMiB           map[string]*int `json:"recommended_mib"`
}

func recommended(t *testing.T, got memoryLimitsResp, typ string) int {
	t.Helper()
	v, ok := got.RecommendedMiB[typ]
	if !ok || v == nil {
		t.Fatalf("recommended_mib[%s] = %v", typ, v)
	}
	return *v
}

func TestGetMemoryLimits(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "play.example.org")
	admin := e.admin()

	rec := e.do(http.MethodGet, "/servers/memory", nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown host: %d %s", rec.Code, rec.Body)
	}
	got := decode[memoryLimitsResp](t, rec)
	if got.HostTotalMiB != nil || got.ContainerBudgetMiB != nil || got.MaxMiB != instancespec.MaxMemoryMiB || got.MinMiB != 1024 || got.StepMiB != 256 ||
		got.AllocatedMiB != 0 || got.AllocatedContainerMiB != 0 || got.HostReserveMiB != 1024 || got.ContainerOverheadPercent != 25 {
		t.Fatalf("unknown host: %+v", got)
	}
	if recommended(t, got, "paper") != 4096 || recommended(t, got, "fabric") != 6144 || len(got.RecommendedMiB) != len(instancespec.Types) {
		t.Fatalf("recommended without host: %v", got.RecommendedMiB)
	}

	e.hostMem = 16 * gib
	e.create(t, paper("survival"))
	e.create(t, with(paper("creative"), "memory_mib", 2048))
	got = decode[memoryLimitsResp](t, e.do(http.MethodGet, "/servers/memory", nil, &admin))
	if got.HostTotalMiB == nil || *got.HostTotalMiB != 16384 || got.MaxMiB != 12288 || got.AllocatedMiB != 6144 ||
		got.AllocatedContainerMiB != 7680 || got.ContainerBudgetMiB == nil || *got.ContainerBudgetMiB != 15360 {
		t.Fatalf("16 GiB host: %+v", got)
	}
	// free 15360 − 7680 = 7680, half 3840 = heap 3072
	if recommended(t, got, "vanilla") != 3072 || recommended(t, got, "neoforge") != 3072 || recommended(t, got, "curseforge") != 3072 {
		t.Fatalf("recommended on 16 GiB: %v", got.RecommendedMiB)
	}

	// Two 7 GiB heaps on 16 GiB (issue #158): 14 GiB of heap, but 17.5 GiB of container limits
	// exceed the 15 GiB budget. Nothing safe is left, so no recommendation.
	e2 := newEnv(t)
	setBaseDomain(t, e2.db, "play.example.org")
	e2.hostMem = 16 * gib
	e2.create(t, with(paper("alpha"), "memory_mib", 7168))
	e2.create(t, with(paper("beta"), "memory_mib", 7168))
	admin2 := e2.admin()
	got = decode[memoryLimitsResp](t, e2.do(http.MethodGet, "/servers/memory", nil, &admin2))
	if got.AllocatedMiB != 14336 || got.AllocatedContainerMiB != 17920 || *got.ContainerBudgetMiB != 15360 {
		t.Fatalf("two 7 GiB servers: %+v", got)
	}
	if v := got.RecommendedMiB["paper"]; v != nil || len(got.RecommendedMiB) != len(instancespec.Types) {
		t.Fatalf("exhausted budget recommends %v", got.RecommendedMiB)
	}
	if !strings.Contains(e2.do(http.MethodGet, "/servers/memory", nil, &admin2).Body.String(), `"paper":null`) {
		t.Fatal("no safe recommendation must be null, not absent")
	}

	uid := storetest.CreateUser(t, e.db, "alice", "user")
	alice := userP(uid, "alice")
	if rec := e.do(http.MethodGet, "/servers/memory", nil, &alice); rec.Code != http.StatusForbidden {
		t.Fatalf("user: %d", rec.Code)
	}
}

func TestCreateRespectsHostMemoryLimit(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "play.example.org")
	e.hostMem = 16 * gib
	admin := e.admin()
	rec := e.do(http.MethodPost, "/servers", with(paper("big"), "memory_mib", 12544), &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("above the host limit: %d %s", rec.Code, rec.Body)
	}
	if n := len(e.rec.Actions()); n != 0 {
		t.Fatalf("a rejected create must not be audited: %d entries", n)
	}
	e.create(t, with(paper("big"), "memory_mib", 12288))
}

func TestUpdateRespectsHostMemoryLimit(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "play.example.org")
	admin := e.admin()
	s := e.create(t, with(paper("big"), "memory_mib", 16384)) // host memory unknown: 32 GiB apply
	e.hostMem = 16 * gib                                      // now the limit is 12 GiB
	path := fmt.Sprintf("/servers/%d", s.ID)

	// Saving checks the stored value like a create does, also when it does not change.
	rec := e.do(http.MethodPatch, path, map[string]any{"properties": map[string]any{"motd": "Neu"}}, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("unchanged memory above the limit: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(http.MethodPatch, path, map[string]any{"memory_mib": 14336}, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("changed memory above the limit: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(http.MethodPatch, path, map[string]any{"memory_mib": 12288}, &admin)
	if rec.Code != http.StatusOK || decode[serverResp](t, rec).Instance.MemoryMiB != 12288 {
		t.Fatalf("memory within the limit: %d %s", rec.Code, rec.Body)
	}
}
