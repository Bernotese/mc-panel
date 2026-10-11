package serverfiles_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestUploadFile(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	rec := e.doRaw(http.MethodPost, srvPath(id, "/files/upload?path=plugins/Essentials.jar"), bytes.NewReader([]byte("jar")), &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	if got := decode[filesapi.UploadResponse](t, rec); got.Size != 3 {
		t.Fatalf("size = %d", got.Size)
	}
	if calls := e.files.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("upload %d plugins/Essentials.jar overwrite=false size=3", id)}) {
		t.Fatalf("calls = %v", calls)
	}
	if string(e.files.uploads["plugins/Essentials.jar"]) != "jar" {
		t.Fatalf("stored = %q", e.files.uploads["plugins/Essentials.jar"])
	}
	last := e.lastEntry()
	if last.Action != audit.ActionFileUpload || last.Result != audit.ResultSuccess ||
		last.Details["path"] != "plugins/Essentials.jar" || last.Details["size"] != int64(3) {
		t.Fatalf("audit = %+v", last)
	}
}

func TestUploadErrorsAndOverwrite(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	body := func() *bytes.Reader { return bytes.NewReader([]byte("abc")) }
	e.doRaw(http.MethodPost, srvPath(id, "/files/upload?path=a.jar&overwrite=true"), body(), &admin)
	e.doRaw(http.MethodPost, srvPath(id, "/files/upload?path=a.jar&overwrite=false"), body(), &admin)
	e.doRaw(http.MethodPost, srvPath(id, "/files/upload?path=a.jar"), body(), &admin)
	want := []string{
		fmt.Sprintf("upload %d a.jar overwrite=true size=3", id),
		fmt.Sprintf("upload %d a.jar overwrite=false size=3", id),
		fmt.Sprintf("upload %d a.jar overwrite=false size=3", id),
	}
	if calls := e.files.takeCalls(); !slices.Equal(calls, want) {
		t.Fatalf("calls = %v", calls)
	}
	n := len(e.rec.Entries)
	for _, tc := range []struct {
		query string
		code  int
		ec    string
	}{
		{"path=a.jar&overwrite=maybe", http.StatusBadRequest, httpx.CodeValidation},
		{"path=a.jar&overwrite=1", http.StatusBadRequest, httpx.CodeValidation},
		{"path=a.jar&overwrite=TRUE", http.StatusBadRequest, httpx.CodeValidation},
		{"path=a.jar&overwrite=", http.StatusBadRequest, httpx.CodeValidation},
		{"path=a.jar&overwrite=true&overwrite=false", http.StatusBadRequest, httpx.CodeValidation},
		{"path=", http.StatusBadRequest, httpx.CodeValidation},
		{"path=../a.jar", http.StatusBadRequest, httpx.CodeValidation},
		{"path=.rcon-cli.env", http.StatusNotFound, httpx.CodeNotFound},
		{"path=.mcpanel-tmp-x", http.StatusNotFound, httpx.CodeNotFound},
	} {
		rec := e.doRaw(http.MethodPost, srvPath(id, "/files/upload?"+tc.query), body(), &admin)
		if rec.Code != tc.code || errCode(t, rec) != tc.ec {
			t.Errorf("%s: %d %s", tc.query, rec.Code, rec.Body)
		}
	}
	if calls := e.files.takeCalls(); len(calls) != 0 {
		t.Fatalf("rejected uploads reached the service: %v", calls)
	}
	if len(e.rec.Entries) != n {
		t.Fatalf("rejected uploads were audited: %v", e.rec.Actions())
	}

	e.files.setErr(fmt.Errorf("%w: a.jar", filesapi.ErrExists))
	rec := e.doRaw(http.MethodPost, srvPath(id, "/files/upload?path=a.jar"), body(), &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeFileExists {
		t.Fatalf("exists: %d %s", rec.Code, rec.Body)
	}
	if last := e.lastEntry(); last.Action != audit.ActionFileUpload || last.Result != audit.ResultFailure || last.Details["size"] != int64(3) {
		t.Fatalf("audit = %+v", last)
	}
}

// Review Focus #2: a chunked upload without Content-Length is refused before the service.
func TestUploadNeedsContentLength(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.server(e.admin(), nil)
	pr, pw := io.Pipe()
	go func() { _, _ = pw.Write([]byte("abc")); _ = pw.Close() }()
	req, err := http.NewRequest(http.MethodPost, srv.URL+srvPath(id, "/files/upload?path=a.jar"), pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = -1 // chunked transfer encoding
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if calls := e.files.takeCalls(); len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
}

// Review Focus #3: the service receives the first MiB while the browser is still sending –
// the panel must not read the whole body first.
func TestUploadStreamsWithoutBuffering(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.server(e.admin(), nil)
	const chunk = 1 << 20
	gotFirst := make(chan struct{})
	e.files.setUploadHook(func(body io.Reader) (int64, error) {
		var n int64
		buf := make([]byte, 32<<10)
		signalled := false
		for {
			k, err := body.Read(buf)
			n += int64(k)
			if !signalled && n >= chunk {
				close(gotFirst)
				signalled = true
			}
			if errors.Is(err, io.EOF) {
				return n, nil
			}
			if err != nil {
				return n, fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
			}
		}
	})
	pr, pw := io.Pipe()
	go func() {
		if _, err := pw.Write(bytes.Repeat([]byte{'a'}, chunk)); err != nil {
			return
		}
		select {
		case <-gotFirst:
		case <-time.After(5 * time.Second):
			pw.CloseWithError(errors.New("the panel buffered the body: the service saw nothing while the client was sending"))
			return
		}
		_, _ = pw.Write(bytes.Repeat([]byte{'b'}, 3*chunk))
		_ = pw.Close()
	}()
	req, err := http.NewRequest(http.MethodPost, srv.URL+srvPath(id, "/files/upload?path=world.zip"), pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 4 * chunk
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated || !strings.Contains(string(raw), fmt.Sprintf(`"size":%d`, 4*chunk)) {
		t.Fatalf("upload: %d %s", resp.StatusCode, raw)
	}
}

// Review Focus #2: at most two uploads per user at a time.
func TestUploadLimitPerUser(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.server(e.admin(), nil)
	entered := make(chan struct{}, 8)
	hold := make(chan struct{})
	e.files.setUploadHook(func(body io.Reader) (int64, error) {
		entered <- struct{}{}
		<-hold
		return io.Copy(io.Discard, body)
	})
	post := func() (int, string) {
		resp, err := srv.Client().Post(srv.URL+srvPath(id, "/files/upload?path=a.jar"), "application/octet-stream", bytes.NewReader([]byte("abc")))
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); codes[i], _ = post() }()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("uploads never reached the service")
		}
	}
	if code, body := post(); code != http.StatusTooManyRequests || !strings.Contains(body, `"rate_limited"`) {
		t.Fatalf("third upload: %d %s", code, body)
	}
	close(hold)
	wg.Wait()
	if codes[0] != http.StatusCreated || codes[1] != http.StatusCreated {
		t.Fatalf("held uploads = %v", codes)
	}
	if code, body := post(); code != http.StatusCreated {
		t.Fatalf("after release: %d %s", code, body)
	}
}

// Review Focus #3: an upload longer than the server's ReadTimeout/WriteTimeout succeeds,
// because every read and the answer get a fresh deadline.
func TestUploadSurvivesServerTimeouts(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.server(e.admin(), func(s *http.Server) {
		s.ReadTimeout = 300 * time.Millisecond
		s.WriteTimeout = 300 * time.Millisecond
	})
	pr, pw := io.Pipe()
	go func() {
		for range 3 {
			if _, err := pw.Write(bytes.Repeat([]byte{'x'}, 10)); err != nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		_ = pw.Close()
	}()
	req, err := http.NewRequest(http.MethodPost, srv.URL+srvPath(id, "/files/upload?path=slow.txt"), pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 30
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("slow upload: %d %s", resp.StatusCode, raw)
	}
}

// Review Focus #3: a stalled upload is aborted after the silence timeout and audited as failure.
func TestUploadAbortsAfterSilence(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	serverfiles.SetIOTimeout(e.h, 150*time.Millisecond)
	srv := e.server(e.admin(), nil)
	readErr := make(chan error, 1)
	e.files.setUploadHook(func(body io.Reader) (int64, error) {
		n, err := io.Copy(io.Discard, body)
		readErr <- err
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
	if resp, err := srv.Client().Do(req); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusCreated {
			t.Fatal("a stalled upload succeeded")
		}
	}
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("the service read the stalled body without an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stalled upload was never aborted")
	}
	waitAudit(t, e, audit.ActionFileUpload, audit.ResultFailure)
}

// waitAudit waits until the newest audit entry has action and result (the handler records after
// the service call returns, possibly after the client saw an error).
func waitAudit(t *testing.T, e *env, action string, res audit.Result) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if n := len(e.rec.Actions()); n > 0 {
			last := e.rec.Entries[n-1]
			if last.Action == action && last.Result == res {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no audit entry %s/%s: %v", action, res, e.rec.Actions())
}

func TestDownloadFile(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	e.files.download = filesapi.Download{Body: io.NopCloser(strings.NewReader("hello")), Size: 5, Name: "Wélt.txt"}
	rec := e.do(http.MethodGet, srvPath(id, "/files/download?path=docs/W%C3%A9lt.txt"), nil, &admin)
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("download: %d %q", rec.Code, rec.Body)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "application/octet-stream" || h.Get("Content-Length") != "5" ||
		h.Get("Content-Disposition") != `attachment; filename="W_lt.txt"; filename*=UTF-8''W%C3%A9lt.txt` {
		t.Fatalf("headers = %v", h)
	}
	if calls := e.files.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("download %d docs/Wélt.txt", id)}) {
		t.Fatalf("calls = %v", calls)
	}
	if last := e.lastEntry(); last.Action != audit.ActionFileDownload || last.Result != audit.ResultSuccess || last.Details["path"] != "docs/Wélt.txt" {
		t.Fatalf("audit = %+v", last)
	}
}

func TestDownloadDirectoryAsZip(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	e.files.download = filesapi.Download{Body: io.NopCloser(strings.NewReader("PK")), Size: -1, IsDir: true, Name: "world"}
	rec := e.do(http.MethodGet, srvPath(id, "/files/download?path=world"), nil, &admin)
	h := rec.Header()
	if rec.Code != http.StatusOK || h.Get("Content-Type") != "application/zip" || h.Get("Content-Length") != "" ||
		!strings.HasPrefix(h.Get("Content-Disposition"), `attachment; filename="world.zip"`) {
		t.Fatalf("dir download: %d %v", rec.Code, h)
	}
	// the whole data directory: named after the server
	e.files.download = filesapi.Download{Body: io.NopCloser(strings.NewReader("PK")), Size: -1, IsDir: true}
	rec = e.do(http.MethodGet, srvPath(id, "/files/download"), nil, &admin)
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, `attachment; filename="survival.zip"`) {
		t.Fatalf("root download: %d %s", rec.Code, got)
	}
}

func TestDownloadErrors(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	rec := e.do(http.MethodGet, srvPath(id, "/files/download?path=.rcon-cli.yaml"), nil, &admin)
	if rec.Code != http.StatusNotFound || len(e.files.takeCalls()) != 0 {
		t.Fatalf("hidden: %d %s", rec.Code, rec.Body)
	}
	e.files.setErr(fmt.Errorf("%w: current", filesapi.ErrNotRegular))
	rec = e.do(http.MethodGet, srvPath(id, "/files/download?path=current"), nil, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeNotRegular {
		t.Fatalf("symlink: %d %s", rec.Code, rec.Body)
	}
	if len(e.rec.Entries) != 0 { // a refused download is a rejection, not audited (review B4)
		t.Fatalf("audit = %v", e.rec.Actions())
	}
}

// Review Focus #3: the browser receives the first part while the service is still sending.
func TestDownloadStreams(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.server(e.admin(), nil)
	pr, pw := io.Pipe()
	e.files.download = filesapi.Download{Body: pr, Size: -1, IsDir: true, Name: "world"}
	go func() { _, _ = pw.Write([]byte("first")) }()
	resp, err := srv.Client().Get(srv.URL + srvPath(id, "/files/download?path=world"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 5)
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(resp.Body, buf); done <- err }()
	select {
	case err := <-done:
		if err != nil || string(buf) != "first" {
			t.Fatalf("first part: %q %v", buf, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the panel held the download back until the service finished")
	}
	go func() { _, _ = pw.Write([]byte("second")); _ = pw.Close() }()
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(rest) != "second" {
		t.Fatalf("rest: %q %v", rest, err)
	}
	waitAudit(t, e, audit.ActionFileDownload, audit.ResultSuccess)
}

// The two user ids must not share upload slots.
func TestUploadSlotsArePerUser(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	u := storetest.CreateUser(t, e.db, "filer", "user")
	grant(t, e.db, u, id, "server.view", "server.files")
	adminSrv := e.server(e.admin(), nil)
	userSrv := e.server(userP(u, "filer"), nil)
	entered := make(chan struct{}, 8)
	hold := make(chan struct{})
	e.files.setUploadHook(func(body io.Reader) (int64, error) {
		entered <- struct{}{}
		<-hold
		return io.Copy(io.Discard, body)
	})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := adminSrv.Client().Post(adminSrv.URL+srvPath(id, "/files/upload?path=a"), "application/octet-stream", bytes.NewReader([]byte("a")))
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		<-entered
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, err := userSrv.Client().Post(userSrv.URL+srvPath(id, "/files/upload?path=b"), "application/octet-stream", bytes.NewReader([]byte("b")))
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("another user's upload was blocked by the admin's slots")
	}
	close(hold)
	wg.Wait()
}

// Smoke test (review finding C1): a download longer than the server's ReadTimeout and
// WriteTimeout succeeds. net/http (Go 1.27) already clears the read deadline before a handler
// for a request without body, so this test also passes without handleDownload's safeguard.
func TestDownloadSurvivesServerTimeouts(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.server(e.admin(), func(s *http.Server) {
		s.ReadTimeout = 300 * time.Millisecond
		s.WriteTimeout = 300 * time.Millisecond
	})
	pr, pw := io.Pipe()
	e.files.download = filesapi.Download{Body: pr, Size: 50, Name: "slow.txt"}
	go func() {
		for range 5 {
			if _, err := pw.Write(bytes.Repeat([]byte{'x'}, 10)); err != nil {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		_ = pw.Close()
	}()
	resp, err := srv.Client().Get(srv.URL + srvPath(id, "/files/download?path=slow.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || err != nil || len(body) != 50 {
		t.Fatalf("slow download: %d, %d bytes, %v", resp.StatusCode, len(body), err)
	}
	waitAudit(t, e, audit.ActionFileDownload, audit.ResultSuccess)
}

// Review finding I1: like net/http's Transport, the service client reads once more after the
// announced length; a deadline armed for that read would cancel the request while the service
// is still storing the file.
func TestUploadSurvivesSlowServiceAfterBody(t *testing.T) {
	for _, size := range []int{3, 0} {
		e := newEnv(t)
		id := createInstance(t, e.db, "survival")
		serverfiles.SetIOTimeout(e.h, 150*time.Millisecond)
		srv := e.server(e.admin(), func(s *http.Server) { s.ReadTimeout = 300 * time.Millisecond })
		e.files.setUploadHook(func(body io.Reader) (int64, error) {
			n, err := io.Copy(io.Discard, io.LimitReader(body, int64(size)))
			if err != nil {
				return n, fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
			}
			if extra, err := io.Copy(io.Discard, body); err != nil {
				return n, fmt.Errorf("%w: extra read: %w", filesapi.ErrUnavailable, err)
			} else if extra != 0 {
				return n, fmt.Errorf("%w: %d bytes after the announced length", filesapi.ErrInvalid, extra)
			}
			time.Sleep(700 * time.Millisecond) // fsync and rename
			return n, nil
		})
		resp, err := srv.Client().Post(srv.URL+srvPath(id, "/files/upload?path=a.jar"), "application/octet-stream", bytes.NewReader(make([]byte, size)))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%d bytes, slow service: %d %s", size, resp.StatusCode, raw)
		}
		waitAudit(t, e, audit.ActionFileUpload, audit.ResultSuccess)
	}
}

// failingDeadline is a ResponseWriter whose connection refuses write deadlines.
type failingDeadline struct{ *httptest.ResponseRecorder }

func (failingDeadline) SetWriteDeadline(time.Time) error { return errors.New("connection gone") }

// Review finding M2: if the first write deadline fails, the answer is an error, not a 200
// with the file's headers and no body.
func TestDownloadWriteDeadlineFails(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	e.files.download = filesapi.Download{Body: io.NopCloser(strings.NewReader("hello")), Size: 5, Name: "a.txt"}
	w := failingDeadline{httptest.NewRecorder()}
	e.mux.ServeHTTP(w, httptestRequestWithContext(t.Context(), http.MethodGet, srvPath(id, "/files/download?path=a.txt"), admin))
	h := w.Header()
	if w.Code != http.StatusInternalServerError || h.Get("Content-Disposition") != "" || h.Get("Content-Length") != "" {
		t.Fatalf("download: %d %v %q", w.Code, h, w.Body)
	}
	if last := e.lastEntry(); last.Action != audit.ActionFileDownload || last.Result != audit.ResultFailure {
		t.Fatalf("audit = %+v", last)
	}
	if _, closed := e.files.lastDownload(); !isClosed(closed) {
		t.Fatal("the service body was not closed")
	}
}

func isClosed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// Review finding M3: a browser that leaves mid-download ends the service request and closes
// its body.
func TestDownloadClientAbortEndsService(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.server(e.admin(), nil)
	pr, pw := io.Pipe()
	e.files.download = filesapi.Download{Body: pr, Size: -1, IsDir: true, Name: "world"}
	go func() { _, _ = pw.Write([]byte("first")) }()
	resp, err := srv.Client().Get(srv.URL + srvPath(id, "/files/download?path=world"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(resp.Body, make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() // the browser leaves
	ctx, closed := e.files.lastDownload()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the service request was not cancelled")
	}
	// the handler notices on its next read or write; the service keeps sending meanwhile
	deadline := time.Now().Add(5 * time.Second)
	for !isClosed(closed) {
		if time.Now().After(deadline) {
			t.Fatal("the service body was never closed")
		}
		_, _ = pw.Write([]byte("more"))
		time.Sleep(10 * time.Millisecond)
	}
	waitAudit(t, e, audit.ActionFileDownload, audit.ResultFailure)
}

// Review round 2: a service that stops sending while the browser stays connected must not hold
// the download forever; the browser sees a failed (not a truncated but complete) download.
func TestDownloadAbortsWhenServiceStalls(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	serverfiles.SetIOTimeout(e.h, 150*time.Millisecond)
	srv := e.server(e.admin(), nil)
	pr, pw := io.Pipe()
	defer pw.Close()
	e.files.download = filesapi.Download{Body: pr, Size: -1, IsDir: true, Name: "world"}
	go func() { _, _ = pw.Write([]byte("first")) }() // then the service stalls
	resp, err := srv.Client().Get(srv.URL + srvPath(id, "/files/download?path=world"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(resp.Body); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stalled download ended like a complete one")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a stalled download was never aborted")
	}
	waitAudit(t, e, audit.ActionFileDownload, audit.ResultFailure)
	if ctx, _ := e.files.lastDownload(); ctx.Err() == nil {
		t.Fatal("the service request was not cancelled")
	}
}
