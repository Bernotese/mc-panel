package catalog_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
)

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// upstream serves the testdata samples under the real paths and records every request.
type upstream struct {
	mu   sync.Mutex
	reqs []*http.Request
	srv  *httptest.Server
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{}
	files := map[string]string{
		"/mc/game/version_manifest_v2.json":                   "mojang_manifest.json",
		"/v3/projects/paper/versions":                         "paper_versions.json",
		"/v2/purpur":                                          "purpur.json",
		"/v2/versions/game":                                   "fabric_game.json",
		"/v2/versions/loader":                                 "fabric_loader.json",
		"/api/maven/versions/releases/net/neoforged/neoforge": "neoforge_versions.json",
	}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.reqs = append(u.reqs, r)
		u.mu.Unlock()
		name, ok := files[r.URL.Path]
		if v, isBuilds := strings.CutPrefix(r.URL.Path, "/v3/projects/paper/versions/"); isBuilds && strings.HasSuffix(v, "/builds") {
			if r.URL.Query().Get("channel") != "STABLE" {
				http.Error(w, "channel missing", http.StatusBadRequest)
				return
			}
			name, ok = "paper_builds_stable.json", true
			if v == "26.3/builds" {
				name = "paper_builds_none.json"
			}
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *upstream) count(prefix string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	n := 0
	for _, r := range u.reqs {
		if strings.HasPrefix(r.URL.Path, prefix) {
			n++
		}
	}
	return n
}

func (u *upstream) client(clk clock.Clock) *catalog.Client {
	url := u.srv.URL
	return catalog.New(catalog.Options{
		Clock: clk, Version: "1.2.3",
		Upstreams: catalog.Upstreams{Mojang: url, Paper: url, Purpur: url, Fabric: url, NeoForge: url},
	})
}

func ids(c catalog.Catalog) []string {
	out := make([]string, len(c.Versions))
	for i, v := range c.Versions {
		out[i] = v.ID + "/" + string(v.Java)
	}
	return out
}

func TestCatalogsFromRealShapedSamples(t *testing.T) {
	u := newUpstream(t)
	c := u.client(clock.NewFake(t0))
	ctx := context.Background()
	cases := []struct {
		typ     instancespec.Type
		want    []string
		loaders []string
	}{
		{instancespec.TypeVanilla, []string{"26.3/java25", "26.2/java25", "26.1.2/java25", "26.1/java25", "1.21.11/java21", "1.20.4/java17", "1.16.5/java8", "1.8.9/java8"}, []string{}},
		// 26.3 has no STABLE build yet; the pre-release 1.21.11-rc3 is skipped.
		{instancespec.TypePaper, []string{"26.2/java25", "26.1.2/java25", "26.1/java25", "1.21.11/java21", "1.21.10/java21", "1.21.8/java21", "1.21.4/java21", "1.21.1/java21", "1.20.6/java21", "1.20.4/java17", "1.19.4/java17", "1.16.5/java8", "1.8.8/java8"}, []string{}},
		// up to metadata.current (26.2), newest first
		{instancespec.TypePurpur, []string{"26.2/java25", "26.1.2/java25", "26.1/java25", "1.21.11/java21", "1.20.4/java17", "1.16.5/java8", "1.14.1/java8"}, []string{}},
		{instancespec.TypeFabric, []string{"26.3/java25", "26.2/java25", "1.21.11/java21", "1.20.4/java17"}, []string{"0.19.5", "0.19.4"}},
		// Minecraft versions with at least one NeoForge release; 26.3 only has betas
		{instancespec.TypeNeoForge, []string{"26.2/java25", "26.1.2/java25", "1.21.11/java21", "1.21.4/java21", "1.21.1/java21", "1.21/java21", "1.20.4/java17"}, []string{}},
	}
	for _, tc := range cases {
		got, err := c.Get(ctx, tc.typ)
		if err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		if !slices.Equal(ids(got), tc.want) {
			t.Errorf("%s versions =\n%v\nwant\n%v", tc.typ, ids(got), tc.want)
		}
		if !slices.Equal(got.Loaders, tc.loaders) {
			t.Errorf("%s loaders = %v, want %v", tc.typ, got.Loaders, tc.loaders)
		}
	}
	// Paper: only the newest 10 versions are checked for a STABLE build.
	if n := u.count("/v3/projects/paper/versions/"); n != 10 {
		t.Errorf("paper build checks = %d, want 10", n)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, r := range u.reqs {
		if ua := r.Header.Get("User-Agent"); ua != "mc-panel/1.2.3 (https://github.com/Bernotese/mc-panel)" {
			t.Fatalf("%s: User-Agent %q", r.URL.Path, ua)
		}
	}
}

func TestCatalogCacheAndUnsupportedTypes(t *testing.T) {
	u := newUpstream(t)
	clk := clock.NewFake(t0)
	c := u.client(clk)
	ctx := context.Background()
	for range 3 {
		if _, err := c.Get(ctx, instancespec.TypeVanilla); err != nil {
			t.Fatal(err)
		}
	}
	if n := u.count("/mc/"); n != 1 {
		t.Fatalf("cached catalog fetched %d times", n)
	}
	clk.Advance(time.Hour)
	if _, err := c.Get(ctx, instancespec.TypeVanilla); err != nil || u.count("/mc/") != 2 {
		t.Fatalf("catalog not refreshed after 1 h: %v, fetches %d", err, u.count("/mc/"))
	}
	for _, typ := range []instancespec.Type{instancespec.TypeModrinth, instancespec.TypeCurseForge, "bukkit"} {
		if _, err := c.Get(ctx, typ); !errors.Is(err, catalog.ErrUnsupported) {
			t.Errorf("%s: %v, want ErrUnsupported", typ, err)
		}
	}
}

// Review focus 5: garbage from upstream never reaches the form.
func TestCatalogRejectsGarbage(t *testing.T) {
	type answer struct {
		body   string
		status int
		delay  time.Duration
	}
	var (
		mu  sync.Mutex
		cur answer
	)
	set := func(a answer) { mu.Lock(); cur = a; mu.Unlock() }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		a := cur
		mu.Unlock()
		time.Sleep(a.delay)
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(srv.Close)
	c := catalog.New(catalog.Options{
		Clock: clock.NewFake(t0), Timeout: 200 * time.Millisecond,
		Upstreams: catalog.Upstreams{Mojang: srv.URL, Paper: srv.URL, Purpur: srv.URL, Fabric: srv.URL, NeoForge: srv.URL},
	})
	ctx := context.Background()
	unavailable := []struct {
		name, body string
		typ        instancespec.Type
		status     int
		delay      time.Duration
	}{
		{"not json", `<html>maintenance</html>`, instancespec.TypeVanilla, 200, 0},
		{"wrong shape", `{"versions":"26.3"}`, instancespec.TypeVanilla, 200, 0},
		{"http 500", `{}`, instancespec.TypeVanilla, 500, 0},
		{"too large", `{"versions":[` + strings.Repeat(`{"id":"1.21.11","type":"release"},`, catalog.MaxBytes/30) + `{}]}`, instancespec.TypeVanilla, 200, 0},
		{"timeout", `{"versions":[{"id":"26.3","type":"release"}]}`, instancespec.TypeVanilla, 200, time.Second},
		{"no usable version", `{"versions":[{"id":"26.3 beta","type":"release"},{"id":"1.21\nX=1","type":"release"}]}`, instancespec.TypeVanilla, 200, 0},
		{"purpur without current", `{"metadata":{"current":"27.1"},"versions":["26.2"]}`, instancespec.TypePurpur, 200, 0},
	}
	for _, tc := range unavailable {
		set(answer{tc.body, tc.status, tc.delay})
		if _, err := c.Get(ctx, tc.typ); !errors.Is(err, catalog.ErrUnavailable) {
			t.Errorf("%s: %v, want ErrUnavailable", tc.name, err)
		}
	}
	// invalid entries are dropped, the rest is served; failures above were not cached
	set(answer{`{"versions":[{"id":"26.3","type":"release"},{"id":"26 .2","type":"release"},{"id":"` + strings.Repeat("9", 70) + `","type":"release"}]}`, http.StatusOK, 0})
	got, err := c.Get(ctx, instancespec.TypeVanilla)
	if err != nil || !slices.Equal(ids(got), []string{"26.3/java25"}) {
		t.Fatalf("got %v, %v", ids(got), err)
	}
}

func TestNeoForgeMC(t *testing.T) {
	for in, want := range map[string]string{
		"20.4.251": "1.20.4", "21.0.167": "1.21", "21.1.219": "1.21.1", "21.11.38": "1.21.11",
		"26.1.2.15": "26.1.2", "26.2.0.89": "26.2",
	} {
		if got, ok := catalog.NeoForgeMC(in); !ok || got != want {
			t.Errorf("NeoForgeMC(%q) = %q, %t, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"21.11.39-beta", "26.3.0.64-beta", "20.2.3-beta", "26.1.0.1+snapshot", "47.1.79", "19.4.1", "26.1.2", "21.4", "", "a.b.c", "21.+4.1", "21.4.99999999999"} {
		if got, ok := catalog.NeoForgeMC(in); ok {
			t.Errorf("NeoForgeMC(%q) = %q, want rejected", in, got)
		}
	}
}
