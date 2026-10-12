package upstream_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

func TestDefaultHostAllowed(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://cdn.modrinth.com/data/x/versions/y/a.jar":     true,
		"https://hangarcdn.papermc.io/plugins/a/b/PAPER/a.jar": true,
		"https://CDN.Modrinth.com/x":                           true,
		"https://cdn.modrinth.com:443/x":                       true,
		"https://cdn.modrinth.com:8443/x":                      false,
		"http://cdn.modrinth.com/x":                            false,
		"https://user@cdn.modrinth.com/x":                      false,
		"https://cdn.modrinth.com.evil.example/x":              false,
		"https://evil.example/cdn.modrinth.com/x":              false,
		"https://api.modrinth.com/v2/project/x":                false,
		"https://hangar.papermc.io/api/v1/projects/x":          false,
		"ftp://cdn.modrinth.com/x":                             false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := upstream.DefaultHostAllowed(u); got != want {
			t.Errorf("DefaultHostAllowed(%s) = %v, want %v", raw, got, want)
		}
	}
	if upstream.DefaultHostAllowed(nil) {
		t.Error("nil URL allowed")
	}
}

func TestUserAgentAndBases(t *testing.T) {
	c := upstream.New(upstream.Options{Clock: clock.NewFake(t0), Version: "1.2.3"})
	if got := c.UserAgent(); got != "mc-panel/1.2.3 (https://github.com/Bernotese/mc-panel)" {
		t.Errorf("UserAgent = %q", got)
	}
	if c.Bases() != upstream.DefaultBases {
		t.Errorf("Bases = %+v", c.Bases())
	}
	if got := upstream.New(upstream.Options{Clock: clock.NewFake(t0)}).UserAgent(); got != "mc-panel/dev (https://github.com/Bernotese/mc-panel)" {
		t.Errorf("default UserAgent = %q", got)
	}
	u, _ := url.Parse("https://cdn.modrinth.com/x.png")
	if !c.HostAllowed(u) {
		t.Error("default host check refuses cdn.modrinth.com")
	}
}

// Review Focus #1: an API may redirect only to https on its own host, at most 3 times.
func TestAPIRedirects(t *testing.T) {
	project := testdata(t, "modrinth_project_luckperms.json")
	var otherHits atomic.Int64
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits.Add(1)
		_, _ = w.Write(project)
	}))
	t.Cleanup(other.Close)
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/v2/project/")
		switch {
		case p == "luckperms":
			if ua := r.Header.Get("User-Agent"); ua != testUA {
				t.Errorf("User-Agent = %q", ua)
			}
			_, _ = w.Write(project)
		case p == "same":
			http.Redirect(w, r, "/v2/project/luckperms", http.StatusFound)
		case p == "foreign":
			http.Redirect(w, r, other.URL+"/v2/project/luckperms", http.StatusFound)
		case p == "plain":
			http.Redirect(w, r, "http://"+r.Host+"/v2/project/luckperms", http.StatusFound) //nolint:gosec // test server: the downgrade to http is the case under test
		case strings.HasPrefix(p, "hop"):
			n, _ := strconv.Atoi(strings.TrimPrefix(p, "hop"))
			if n >= 3 {
				http.Redirect(w, r, "/v2/project/luckperms", http.StatusFound)
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/v2/project/hop%d", n+1), http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	for _, id := range []string{"same", "hop1"} { // hop1: exactly 3 redirects
		if p, err := c.Project(bg, upstream.Modrinth, id); err != nil || p.ID != "Vebnzrzj" {
			t.Fatalf("%s: %+v, %v", id, p, err)
		}
	}
	for _, id := range []string{"foreign", "plain", "hop0"} { // hop0: 4 redirects
		if _, err := c.Project(bg, upstream.Modrinth, id); !errors.Is(err, upstream.ErrUnavailable) {
			t.Errorf("%s: err = %v, want ErrUnavailable", id, err)
		}
	}
	if n := otherHits.Load(); n != 0 {
		t.Fatalf("the foreign host was contacted %d times", n)
	}
}

// Review Focus #1/#4: status codes, rate limits, broken and huge answers.
func TestAPIErrors(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/v2/project/") {
		case "missing":
			http.NotFound(w, r)
		case "limited":
			w.WriteHeader(http.StatusTooManyRequests)
		case "broken":
			w.WriteHeader(http.StatusInternalServerError)
		case "badjson":
			_, _ = w.Write([]byte(`{"id":`))
		case "huge":
			_, _ = w.Write([]byte(`{"id":"huge","pad":"` + strings.Repeat("a", upstream.MaxJSONBytes) + `"}`))
		case "noid":
			_, _ = w.Write([]byte(`{"slug":"x"}`))
		}
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	_, err := c.Project(bg, upstream.Modrinth, "missing")
	if !errors.Is(err, upstream.ErrNotFound) || errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("404: %v", err)
	}
	_, err = c.Project(bg, upstream.Modrinth, "limited")
	if !errors.Is(err, upstream.ErrUnavailable) || !strings.Contains(err.Error(), "429") {
		t.Fatalf("429: %v", err)
	}
	for _, id := range []string{"broken", "badjson", "huge", "noid"} {
		if _, err := c.Project(bg, upstream.Modrinth, id); !errors.Is(err, upstream.ErrUnavailable) {
			t.Errorf("%s: %v, want ErrUnavailable", id, err)
		}
	}
	// failures are not cached
	_, _ = c.Project(bg, upstream.Modrinth, "broken")
	if n := api.count("GET /v2/project/broken"); n != 2 {
		t.Fatalf("broken fetched %d times, want 2", n)
	}
}

// Review Focus #4: a hanging upstream ends with ErrUnavailable after the API timeout.
func TestAPITimeout(t *testing.T) {
	api := newAPI(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	c := newClient(t, api, nil, clock.NewFake(t0), func(o *upstream.Options) { o.APITimeout = 100 * time.Millisecond })
	start := time.Now()
	if _, err := c.Project(bg, upstream.Modrinth, "slow"); !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("timeout took %s", d)
	}
}
