package serverfiles_test

// Tests for the whole-feature review findings of sub-project 3b (S1, S2, B1, B4, B6).

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// postJSON sends a JSON request to a real test server and returns status and body.
func postJSON(t *testing.T, client *http.Client, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// B1: deleting a large folder or checking a large archive may take longer than the panel's
// WriteTimeout; the answer still reaches the browser.
func TestDeleteAndExtractOutliveWriteTimeout(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	e.files.onCall = func() { time.Sleep(300 * time.Millisecond) }
	srv := e.server(e.admin(), func(s *http.Server) { s.WriteTimeout = 100 * time.Millisecond })
	if code, body := postJSON(t, srv.Client(), http.MethodPost, srv.URL+srvPath(id, "/files/delete"), `{"path":"world"}`); code != http.StatusNoContent {
		t.Fatalf("slow delete: %d %s", code, body)
	}
	if code, body := postJSON(t, srv.Client(), http.MethodPost, srv.URL+srvPath(id, "/files/extract"), `{"path":"w.zip","dest":"w"}`); code != http.StatusAccepted {
		t.Fatalf("slow extract: %d %s", code, body)
	}
}

// B6: saving a large file over a slow connection takes longer than the server's ReadTimeout;
// every read gets its own deadline, and the answer a fresh write deadline.
func TestWriteSurvivesServerTimeouts(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.server(e.admin(), func(s *http.Server) {
		s.ReadTimeout = 200 * time.Millisecond
		s.WriteTimeout = 200 * time.Millisecond
	})
	parts := []string{`{"path":"config.yml",`, `"content":"spawn: true\n",`, `"expected_mtime":null}`}
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	pr, pw := io.Pipe()
	go func() {
		for _, p := range parts {
			if _, err := pw.Write([]byte(p)); err != nil {
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
		_ = pw.Close()
	}()
	req, err := http.NewRequest(http.MethodPut, srv.URL+srvPath(id, "/files/content"), pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = int64(total)
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("slow save: %d %s", resp.StatusCode, raw)
	}
}

// B4: rejections are not audited (spec §5.3): a busy service, and downloads refused with 400 or
// 404. A download the service could not deliver is.
func TestRejectionsAreNotAudited(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	e.files.setErr(fmt.Errorf("%w: too many uploads", filesapi.ErrBusy))
	if rec := e.doRaw(http.MethodPost, srvPath(id, "/files/upload?path=a.jar"), bytes.NewReader([]byte("abc")), &admin); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("busy upload: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodPost, srvPath(id, "/files/extract"), map[string]string{"path": "w.zip", "dest": "w"}, &admin); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("busy extract: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodGet, srvPath(id, "/files/download?path=world"), nil, &admin); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("busy download: %d %s", rec.Code, rec.Body)
	}
	for _, err := range []error{filesapi.ErrNotFound, filesapi.ErrNotRegular, filesapi.ErrInvalid} {
		e.files.setErr(fmt.Errorf("service: %w", err))
		if rec := e.do(http.MethodGet, srvPath(id, "/files/download?path=x"), nil, &admin); rec.Code/100 != 4 {
			t.Fatalf("%v: %d %s", err, rec.Code, rec.Body)
		}
	}
	if n := len(e.rec.Entries); n != 0 {
		t.Fatalf("rejections were audited: %v", e.rec.Actions())
	}
	e.files.setErr(fmt.Errorf("%w: connection refused", filesapi.ErrUnavailable))
	e.do(http.MethodGet, srvPath(id, "/files/download?path=x"), nil, &admin)
	if last := e.lastEntry(); last.Action != audit.ActionFileDownload || last.Result != audit.ResultFailure {
		t.Fatalf("failed download: audit = %+v", last)
	}
}

// blockingBody is a download body whose reads wait for release.
type blockingBody struct{ release chan struct{} }

func (b blockingBody) Read([]byte) (int, error) { <-b.release; return 0, io.EOF }
func (b blockingBody) Close() error             { return nil }

// S1: one account runs at most 4 downloads at a time.
func TestDownloadLimitPerUser(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	release := make(chan struct{})
	// Files: folder zips have their own, lower limit (TestOneFolderZipPerUser).
	e.files.download = filesapi.Download{Body: blockingBody{release}, Size: -1, Name: "world.bin"}
	var started atomic.Int32
	e.files.onCall = func() { started.Add(1) }
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.do(http.MethodGet, srvPath(id, "/files/download?path=world.bin"), nil, &admin)
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for started.Load() < 4 {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("downloads never reached the service")
		}
		time.Sleep(5 * time.Millisecond)
	}
	rec := e.do(http.MethodGet, srvPath(id, "/files/download?path=world.bin"), nil, &admin)
	close(release)
	wg.Wait()
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != httpx.CodeRateLimited || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("fifth download: %d %s", rec.Code, rec.Body)
	}
	if started.Load() != 4 {
		t.Fatalf("the refused download reached the service (%d calls)", started.Load())
	}
	if rec := e.do(http.MethodGet, srvPath(id, "/files/download?path=world.bin"), nil, &admin); rec.Code != http.StatusOK {
		t.Fatalf("after release: %d %s", rec.Code, rec.Body)
	}
}

// S1: editor reads and saves (up to 5 MiB each, held in memory) are limited panel-wide.
func TestEditorCallsAreLimited(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	release := make(chan struct{})
	var inFlight atomic.Int32
	e.files.onCall = func() {
		inFlight.Add(1)
		<-release
	}
	var wg sync.WaitGroup
	for i := range serverfiles.MaxEditorCalls { // one call per account: the per-user cap is lower
		p := e.otherAdmin(fmt.Sprintf("admin%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.do(http.MethodGet, srvPath(id, "/files/content?path=a.yml"), nil, &p)
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for inFlight.Load() < int32(serverfiles.MaxEditorCalls) {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("reads never reached the service")
		}
		time.Sleep(5 * time.Millisecond)
	}
	read := e.do(http.MethodGet, srvPath(id, "/files/content?path=a.yml"), nil, &admin)
	write := e.do(http.MethodPut, srvPath(id, "/files/content"), map[string]any{"path": "a.yml", "content": "x", "expected_mtime": nil}, &admin)
	close(release)
	wg.Wait()
	for name, rec := range map[string]int{"read": read.Code, "write": write.Code} {
		if rec != http.StatusTooManyRequests {
			t.Errorf("%s over the limit: %d", name, rec)
		}
	}
	if errCode(t, read) != httpx.CodeRateLimited || read.Header().Get("Retry-After") == "" {
		t.Fatalf("read over the limit: %s", read.Body)
	}
	if len(e.rec.Entries) != 0 {
		t.Fatalf("a refused save was audited: %v", e.rec.Actions())
	}
	if rec := e.do(http.MethodGet, srvPath(id, "/files/content?path=a.yml"), nil, &admin); rec.Code != http.StatusOK {
		t.Fatalf("after release: %d %s", rec.Code, rec.Body)
	}
}

// S2: a validation message of the service reaches the browser as the service wrote it, without
// the client's transport prefix.
func TestServiceValidationMessage(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	e.files.setErr(&filesapi.StatusError{Status: http.StatusBadRequest, Code: filesapi.CodeInvalid, Message: `invalid: entry "x" is encrypted`})
	rec := e.do(http.MethodPost, srvPath(id, "/files/extract"), map[string]string{"path": "w.zip", "dest": "w"}, &admin)
	body := decode[httpx.ErrorBody](t, rec)
	if rec.Code != http.StatusBadRequest || body.Message != `invalid: entry "x" is encrypted` {
		t.Fatalf("validation answer: %d %+v", rec.Code, body)
	}
}

func TestErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{filesapi.ErrNotFound, http.StatusNotFound, httpx.CodeNotFound},
		{filesapi.ErrBusy, http.StatusTooManyRequests, httpx.CodeRateLimited},
		{filesapi.ErrUnavailable, http.StatusServiceUnavailable, httpx.CodeFilesUnavailable},
		{fmt.Errorf("boom"), http.StatusInternalServerError, httpx.CodeInternal},
	} {
		if status, code := serverfiles.ErrorStatus(fmt.Errorf("x: %w", tc.err)); status != tc.status || code != tc.code {
			t.Errorf("%v: %d %s, want %d %s", tc.err, status, code, tc.status, tc.code)
		}
	}
}

// otherAdmin creates another admin account and returns its principal.
func (e *env) otherAdmin(name string) principal.Principal {
	e.t.Helper()
	uid := storetest.CreateUser(e.t, e.db, name, "admin")
	return principal.Principal{UserID: uid, Username: name, Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: uid + 100}
}

// Review round 2: one account holds at most 2 editor slots, so it cannot take all of them.
func TestEditorCallsPerUser(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	other := e.otherAdmin("second")
	release := make(chan struct{})
	var inFlight atomic.Int32
	e.files.onCall = func() {
		inFlight.Add(1)
		<-release
	}
	var wg sync.WaitGroup
	for range serverfiles.MaxEditorCallsPerUser {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.do(http.MethodGet, srvPath(id, "/files/content?path=a.yml"), nil, &admin)
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for inFlight.Load() < int32(serverfiles.MaxEditorCallsPerUser) {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("reads never reached the service")
		}
		time.Sleep(5 * time.Millisecond)
	}
	third := e.do(http.MethodGet, srvPath(id, "/files/content?path=a.yml"), nil, &admin)
	otherDone := make(chan int, 1)
	go func() { otherDone <- e.do(http.MethodGet, srvPath(id, "/files/content?path=a.yml"), nil, &other).Code }()
	for inFlight.Load() < int32(serverfiles.MaxEditorCallsPerUser)+1 {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("another account's read was held back")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	wg.Wait()
	if third.Code != http.StatusTooManyRequests || errCode(t, third) != httpx.CodeRateLimited {
		t.Fatalf("third read of one account: %d %s", third.Code, third.Body)
	}
	if code := <-otherDone; code != http.StatusOK {
		t.Fatalf("read of another account: %d", code)
	}
}

// Review round 2: a save that trickles in is bounded overall (not only per read), so it
// cannot hold an editor slot for long.
func TestSaveHasAnOverallDeadline(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	serverfiles.SetSaveTimeout(e.h, 300*time.Millisecond)
	serverfiles.SetIOTimeout(e.h, 5*time.Second)
	srv := e.server(e.admin(), nil)
	body := `{"path":"a.yml","content":"x","expected_mtime":null}`
	pr, pw := io.Pipe()
	go func() {
		for i := range len(body) {
			if _, err := pw.Write([]byte{body[i]}); err != nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		_ = pw.Close()
	}()
	defer pw.Close()
	req, err := http.NewRequest(http.MethodPut, srv.URL+srvPath(id, "/files/content"), pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := srv.Client().Do(req)
	if err == nil {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("trickling save: %d %s", resp.StatusCode, raw)
		}
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the save was read for %s", d)
	}
	deadline := time.Now().Add(3 * time.Second)
	for serverfiles.EditorSlotsInUse(e.h) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the trickling save keeps its editor slot")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, c := range e.files.takeCalls() {
		if strings.HasPrefix(c, "write") {
			t.Fatalf("an incomplete save reached the service: %s", c)
		}
	}
}

// Review round 2: one account streams at most one folder zip at a time (the service has only
// a few zip slots in total); file downloads are not affected.
func TestOneFolderZipPerUser(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	release := make(chan struct{})
	e.files.download = filesapi.Download{Body: blockingBody{release}, Size: -1, IsDir: true, Name: "world"}
	first := make(chan int, 1)
	go func() { first <- e.do(http.MethodGet, srvPath(id, "/files/download?path=world"), nil, &admin).Code }()
	deadline := time.Now().Add(5 * time.Second)
	for !e.calledDownloads(1) {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("the first zip never reached the service")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // the first handler takes its zip slot after the service answered
	second := e.do(http.MethodGet, srvPath(id, "/files/download?path=plugins"), nil, &admin)
	_, closed := e.files.lastDownload()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the refused zip was not cancelled at the service")
	}
	e.files.mu.Lock()
	e.files.download = filesapi.Download{Body: io.NopCloser(strings.NewReader("abc")), Size: 3, Name: "a.txt"}
	e.files.mu.Unlock()
	file := e.do(http.MethodGet, srvPath(id, "/files/download?path=a.txt"), nil, &admin)
	close(release)
	if code := <-first; code != http.StatusOK {
		t.Fatalf("first zip: %d", code)
	}
	if second.Code != http.StatusTooManyRequests || errCode(t, second) != httpx.CodeRateLimited {
		t.Fatalf("second zip: %d %s", second.Code, second.Body)
	}
	if file.Code != http.StatusOK || file.Body.String() != "abc" {
		t.Fatalf("file download while a zip runs: %d %s", file.Code, file.Body)
	}
	for _, a := range e.rec.Actions() {
		if a != audit.ActionFileDownload {
			t.Fatalf("unexpected audit entries: %v", e.rec.Actions())
		}
	}
	if n := len(e.rec.Entries); n != 2 { // the two delivered downloads, not the refused zip
		t.Fatalf("audit entries: %v", e.rec.Actions())
	}
}

// calledDownloads reports whether the fake service saw at least n downloads.
func (e *env) calledDownloads(n int) bool {
	e.files.mu.Lock()
	defer e.files.mu.Unlock()
	c := 0
	for _, call := range e.files.calls {
		if strings.HasPrefix(call, "download ") {
			c++
		}
	}
	return c >= n
}

// Review round 2: a browser that stops sending its upload gets 400 validation_failed, not
// 503 files_unavailable.
func TestUploadStallIsAValidationError(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	serverfiles.SetIOTimeout(e.h, 150*time.Millisecond)
	srv := e.server(e.admin(), nil)
	e.files.setUploadHook(func(body io.Reader) (int64, error) {
		n, err := io.Copy(io.Discard, body)
		if err != nil {
			return n, fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
		}
		return n, nil
	})
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write(bytes.Repeat([]byte{'x'}, 10))
		time.Sleep(time.Second)
		_ = pw.Close()
	}()
	req, err := http.NewRequest(http.MethodPost, srv.URL+srvPath(id, "/files/upload?path=stall.txt"), pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 20
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Skipf("the client saw the closed connection instead of the answer: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), `"validation_failed"`) || !strings.Contains(string(raw), "stalled") {
		t.Fatalf("stalled upload: %d %s", resp.StatusCode, raw)
	}
}
