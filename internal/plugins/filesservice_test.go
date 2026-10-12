package plugins_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/principal"
)

// An old files service without /v1/jars answers 404 not_found: that is an unavailable
// service, not a missing server or file.
func TestInventoryOldFileService(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.files.setErr("jars", fmt.Errorf("%w: no such endpoint", filesapi.ErrNotFound))
	a := e.admin()
	rec := e.do(http.MethodGet, srvPath(id, ""), nil, &a)
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != httpx.CodeFilesUnavailable {
		t.Fatalf("old service: %d %s", rec.Code, rec.Body)
	}
}

// slowJars delays every jars listing (a cold hash cache in the service).
type slowJars struct {
	filesapi.Client
	delay time.Duration
}

func (s slowJars) Jars(ctx context.Context, id int64, dir string) (filesapi.JarsResponse, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return filesapi.JarsResponse{}, ctx.Err()
	}
	return s.Client.Jars(ctx, id, dir)
}

// The jars listing may take up to filesapi.JarsTimeout, longer than the panel's WriteTimeout:
// the routes extend their write deadline instead of dropping the connection without an answer.
func TestInventoryOutlivesWriteTimeout(t *testing.T) {
	e := newEnvWith(t, func(d *plugins.Deps) { d.Files = slowJars{Client: d.Files, delay: 300 * time.Millisecond} })
	id := e.paper("survival")
	a := e.admin()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := principal.With(httpx.WithClientIP(r.Context(), netip.MustParseAddr("203.0.113.7")), a)
		e.mux.ServeHTTP(w, r.WithContext(ctx))
	}))
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+srvPath(id, ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s, err %v", resp.StatusCode, body, err)
	}
}
