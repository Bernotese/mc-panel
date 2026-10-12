package upstream_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

var t0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

var bg = context.Background()

const testUA = "mc-panel/test (https://github.com/Bernotese/mc-panel)"

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// serveFile answers every request with a testdata file as JSON.
func serveFile(t *testing.T, name string) http.HandlerFunc {
	b := testdata(t, name)
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
}

// apiServer is a TLS test server that counts requests per "METHOD /path" and keeps the last
// request (with its body) for assertions.
type apiServer struct {
	*httptest.Server
	mu       sync.Mutex
	hits     map[string]int
	last     *http.Request
	lastBody []byte
}

func newAPI(t *testing.T, h http.HandlerFunc) *apiServer {
	t.Helper()
	a := &apiServer{hits: map[string]int{}}
	a.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		a.mu.Lock()
		a.hits[r.Method+" "+r.URL.Path]++
		a.last = r.Clone(context.Background())
		a.lastBody = body
		a.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		h(w, r)
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *apiServer) count(key string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hits[key]
}

func (a *apiServer) total() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, v := range a.hits {
		n += v
	}
	return n
}

func (a *apiServer) lastRequest() (*http.Request, []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last, a.lastBody
}

// newClient builds a client whose API bases point at api and whose download host check accepts
// exactly the host of cdn (https only). Either may be nil (not both).
func newClient(t *testing.T, api *apiServer, cdn *httptest.Server, clk clock.Clock, opts ...func(*upstream.Options)) *upstream.Client {
	t.Helper()
	o := upstream.Options{Clock: clk, Version: "test"}
	switch {
	case api != nil:
		o.HTTPClient = api.Client()
		o.Bases = upstream.Bases{Modrinth: api.URL, Hangar: api.URL}
	case cdn != nil:
		o.HTTPClient = cdn.Client()
		o.Bases = upstream.Bases{Modrinth: cdn.URL, Hangar: cdn.URL}
	}
	if cdn != nil {
		host := cdn.Listener.Addr().String()
		o.AllowHost = func(u *url.URL) bool { return u.Scheme == "https" && u.Host == host && u.User == nil }
	}
	for _, f := range opts {
		f(&o)
	}
	return upstream.New(o)
}
