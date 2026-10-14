package servers_test

import (
	"fmt"
	"net/http"
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

func TestRecommendedMemoryMiB(t *testing.T) {
	cases := []struct {
		typ             instancespec.Type
		host, allocated int64
		want            int
	}{
		{instancespec.TypePaper, 0, 0, 4096},
		{instancespec.TypeVanilla, 0, 99999, 4096}, // unknown host: only the type decides
		{instancespec.TypeFabric, 0, 0, 6144},
		{instancespec.TypeNeoForge, 65536, 0, 6144},
		{instancespec.TypeModrinth, 16384, 0, 6144},
		{instancespec.TypeCurseForge, 16384, 4096, 5632}, // half of 16384 − 1024 − 4096
		{instancespec.TypePurpur, 16384, 12288, 1536},
		{instancespec.TypePaper, 16384, 12000, 1536}, // half of 3360 = 1680, rounded down to 256
		{instancespec.TypePaper, 16384, 15360, 1024}, // nothing left: the minimum
		{instancespec.TypePaper, 16384, 40000, 1024}, // overcommitted
		{instancespec.TypeFabric, 4096, 0, 1536},
		{instancespec.TypePaper, 2048, 0, 1024},
	}
	for _, c := range cases {
		if got := servers.RecommendedMemoryMiB(c.typ, c.host, c.allocated); got != c.want {
			t.Errorf("RecommendedMemoryMiB(%s, %d, %d) = %d, want %d", c.typ, c.host, c.allocated, got, c.want)
		}
		if got := servers.RecommendedMemoryMiB(c.typ, c.host, c.allocated); got > servers.MaxMemoryMiB(c.host) || got%instancespec.MemoryStepMiB != 0 {
			t.Errorf("RecommendedMemoryMiB(%s, %d, %d) = %d breaks the limits", c.typ, c.host, c.allocated, got)
		}
	}
}

type memoryLimitsResp struct {
	MinMiB         int            `json:"min_mib"`
	MaxMiB         int            `json:"max_mib"`
	StepMiB        int            `json:"step_mib"`
	HostTotalMiB   *int64         `json:"host_total_mib"`
	AllocatedMiB   int64          `json:"allocated_mib"`
	RecommendedMiB map[string]int `json:"recommended_mib"`
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
	if got.HostTotalMiB != nil || got.MaxMiB != instancespec.MaxMemoryMiB || got.MinMiB != 1024 || got.StepMiB != 256 || got.AllocatedMiB != 0 {
		t.Fatalf("unknown host: %+v", got)
	}
	if got.RecommendedMiB["paper"] != 4096 || got.RecommendedMiB["fabric"] != 6144 || len(got.RecommendedMiB) != len(instancespec.Types) {
		t.Fatalf("recommended without host: %v", got.RecommendedMiB)
	}

	e.hostMem = 16 * gib
	e.create(t, paper("survival"))
	e.create(t, with(paper("creative"), "memory_mib", 2048))
	got = decode[memoryLimitsResp](t, e.do(http.MethodGet, "/servers/memory", nil, &admin))
	if got.HostTotalMiB == nil || *got.HostTotalMiB != 16384 || got.MaxMiB != 12288 || got.AllocatedMiB != 6144 {
		t.Fatalf("16 GiB host: %+v", got)
	}
	// half of 16384 − 1024 − 6144 = 4608
	if got.RecommendedMiB["vanilla"] != 4096 || got.RecommendedMiB["neoforge"] != 4608 || got.RecommendedMiB["curseforge"] != 4608 {
		t.Fatalf("recommended on 16 GiB: %v", got.RecommendedMiB)
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
