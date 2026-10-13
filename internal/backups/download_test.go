package backups_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestDownload(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "") // t0 = 12:00:00 in Berlin
	rec := e.must(http.StatusOK, http.MethodGet, bPath(id, "/"+snap+"/download"), nil, e.admin())
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content type %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename=survival-20261012-120000.zip` /* mime.FormatMediaType quotes only when needed */ {
		t.Fatalf("content disposition %q", cd)
	}
	if !bytes.Equal(rec.Body.Bytes(), e.files.zip) {
		t.Fatalf("body = %q", rec.Body.Bytes())
	}
	if got := e.rec.actions(); len(got) != 1 || got[0] != audit.ActionBackupDownload+":success" {
		t.Fatalf("audit = %v", got)
	}
}

func TestDownloadRejections(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	a := e.admin()
	if rec := e.do(http.MethodGet, bPath(id, "/"+fmt.Sprintf("%064x", 77)+"/download"), nil, &a); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown = %d", rec.Code)
	}
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.files.errs["download"] = filesapi.ErrBusy // a forget job holds the repository
	if rec := e.do(http.MethodGet, bPath(id, "/"+snap+"/download"), nil, &a); rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "rate_limited" {
		t.Fatalf("busy = %d %s", rec.Code, rec.Body)
	}
	e.files.status = filesapi.BackupsStatus{Available: false}
	if rec := e.do(http.MethodGet, bPath(id, "/"+snap+"/download"), nil, &a); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no restic = %d", rec.Code)
	}
	if n := len(e.rec.actions()); n != 0 {
		t.Fatalf("rejections audited: %v", e.rec.actions())
	}
}

// blockingBody blocks reads until release is closed.
type blockingBody struct{ release chan struct{} }

func (b blockingBody) Read([]byte) (int, error) { <-b.release; return 0, io.EOF }
func (b blockingBody) Close() error             { return nil }

func TestDownloadLimitPerUser(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	release := make(chan struct{})
	e.files.dlBody = func() io.ReadCloser { return blockingBody{release: release} }
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := e.admin()
			e.do(http.MethodGet, bPath(id, "/"+snap+"/download"), nil, &a)
		}()
	}
	eventually(t, "two open downloads", func() bool {
		e.files.mu.Lock()
		defer e.files.mu.Unlock()
		return e.files.downloads == 2
	})
	a := e.admin()
	if rec := e.do(http.MethodGet, bPath(id, "/"+snap+"/download"), nil, &a); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third download = %d", rec.Code)
	}
	close(release)
	wg.Wait()
}
