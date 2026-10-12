package plugins_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
)

func (e *env) icon(id int64, raw string) *httptest.ResponseRecorder {
	e.t.Helper()
	a := e.admin()
	return e.do(http.MethodGet, srvPath(id, "/icon?url="+url.QueryEscape(raw)), nil, &a)
}

func TestIconProxy(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	vanilla := createInstance(t, e.db, "plain", instancespec.TypeVanilla, "1.21.11")
	for _, sid := range []int64{id, vanilla} { // the icon needs no plugin-capable type
		rec := e.icon(sid, e.up.iconURL("A"))
		h := rec.Header()
		if rec.Code != http.StatusOK || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Content-Security-Policy") != "default-src 'none'" || h.Get("Cache-Control") != "private, max-age=86400" ||
			!bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG")) {
			t.Fatalf("icon: %d %v", rec.Code, h)
		}
	}
}

func TestIconRejects(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	for _, raw := range []string{
		"https://evil.example/icon.png", "http://" + e.up.cdnHost() + "/icons/A.png",
		e.up.cdn.URL + "/icons/" + strings.Repeat("a", 512) + ".png", "",
	} {
		if rec := e.icon(id, raw); rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%.60s: %d %s", raw, rec.Code, rec.Body)
		}
	}
	for _, raw := range []string{e.up.cdn.URL + "/icons/evil.svg", e.up.cdn.URL + "/icons/big.png"} {
		if rec := e.icon(id, raw); rec.Code != http.StatusBadGateway || errCode(t, rec) != httpx.CodePluginSourceUnavailable {
			t.Errorf("%s: %d %s", raw, rec.Code, rec.Body)
		}
	}
	if rec := e.icon(999, e.up.iconURL("A")); rec.Code != http.StatusNotFound {
		t.Errorf("unknown server: %d", rec.Code)
	}
	if n := e.up.count("CDN /icons/" + strings.Repeat("a", 512) + ".png"); n != 0 {
		t.Fatal("a too long URL reached the CDN")
	}
}

func TestIconCache(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	for range 3 {
		e.icon(id, e.up.iconURL("A"))
	}
	if n := e.up.count("CDN /icons/A.png"); n != 1 {
		t.Fatalf("fetched %d times", n)
	}
	e.clk.Advance(24 * time.Hour)
	e.icon(id, e.up.iconURL("A"))
	if n := e.up.count("CDN /icons/A.png"); n != 2 {
		t.Fatalf("fetched %d times after 24 h", n)
	}
	// room for two icons: the least recently used one goes
	plugins.SetIconCacheBytes(e.h, int64(2*len(iconPNG)))
	e.icon(id, e.up.iconURL("B"))
	e.icon(id, e.up.iconURL("A"))
	e.icon(id, e.up.iconURL("C")) // evicts B
	e.icon(id, e.up.iconURL("A"))
	e.icon(id, e.up.iconURL("B"))
	if a, b := e.up.count("CDN /icons/A.png"), e.up.count("CDN /icons/B.png"); a != 2 || b != 2 {
		t.Fatalf("fetches A=%d B=%d", a, b)
	}
}

func TestIconConcurrency(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	block := make(chan struct{})
	e.up.setBlock(block)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() { e.icon(id, e.up.iconURL(string(rune('a'+i)))) })
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.up.peak() < 8 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // a ninth fetch would show up now
	if p := e.up.peak(); p != 8 {
		t.Fatalf("parallel icon fetches = %d, want 8", p)
	}
	close(block)
	wg.Wait()
}
