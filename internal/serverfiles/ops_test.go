package serverfiles_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type listJSON struct {
	Path      string           `json:"path"`
	Entries   []filesapi.Entry `json:"entries"`
	Truncated bool             `json:"truncated"`
}

func TestListFiles(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	e.files.list = filesapi.ListResponse{
		Entries:   []filesapi.Entry{{Name: "config.yml", Type: filesapi.TypeFile, Size: 12, MTime: 1}},
		Truncated: true,
	}
	rec := e.do(http.MethodGet, srvPath(id, "/files?path=plugins/Essentials"), nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	got := decode[listJSON](t, rec)
	if got.Path != "plugins/Essentials" || !got.Truncated || len(got.Entries) != 1 || got.Entries[0].Name != "config.yml" {
		t.Fatalf("list = %+v", got)
	}
	if calls := e.files.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("list %d plugins/Essentials", id)}) {
		t.Fatalf("calls = %v", calls)
	}
	// the data directory itself; an empty answer is [] (never null)
	e.files.list = filesapi.ListResponse{}
	rec = e.do(http.MethodGet, srvPath(id, "/files"), nil, &admin)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"entries":[]`) || !strings.Contains(rec.Body.String(), `"path":""`) {
		t.Fatalf("root list: %d %s", rec.Code, rec.Body)
	}
	if len(e.rec.Entries) != 0 {
		t.Fatalf("listing must not be audited: %v", e.rec.Actions())
	}
}

// Review Focus #4: even if the service ever listed them, hidden names never reach the browser.
func TestListHidesHiddenEntries(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	e.files.list = filesapi.ListResponse{Entries: []filesapi.Entry{
		{Name: ".rcon-cli.env", Type: filesapi.TypeFile},
		{Name: ".rcon-cli.yaml", Type: filesapi.TypeFile},
		{Name: ".mcpanel-tmp-abc", Type: filesapi.TypeFile},
		{Name: "server.properties", Type: filesapi.TypeFile},
	}}
	got := decode[listJSON](t, e.do(http.MethodGet, srvPath(id, "/files"), nil, &admin))
	if len(got.Entries) != 1 || got.Entries[0].Name != "server.properties" {
		t.Fatalf("entries = %+v", got.Entries)
	}
	// .rcon-cli.* is only hidden at the top level
	e.files.list = filesapi.ListResponse{Entries: []filesapi.Entry{{Name: ".rcon-cli.env", Type: filesapi.TypeFile}}}
	got = decode[listJSON](t, e.do(http.MethodGet, srvPath(id, "/files?path=backup"), nil, &admin))
	if len(got.Entries) != 1 {
		t.Fatalf("nested .rcon-cli.env must stay visible: %+v", got.Entries)
	}
}

// Review Focus #1/#4 on the panel side: invalid paths are 400, hidden ones 404, and neither
// reaches the service.
func TestPathRules(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	for _, tc := range []struct {
		path string
		code int
		ec   string
	}{
		{"../etc/passwd", http.StatusBadRequest, httpx.CodeValidation},
		{"/etc/passwd", http.StatusBadRequest, httpx.CodeValidation},
		{"a//b", http.StatusBadRequest, httpx.CodeValidation},
		{`a\b`, http.StatusBadRequest, httpx.CodeValidation},
		{"a\x00b", http.StatusBadRequest, httpx.CodeValidation},
		{"", http.StatusBadRequest, httpx.CodeValidation}, // reading the data directory itself
		{".rcon-cli.env", http.StatusNotFound, httpx.CodeNotFound},
		{".rcon-cli.yaml", http.StatusNotFound, httpx.CodeNotFound},
		{"world/.mcpanel-tmp-1", http.StatusNotFound, httpx.CodeNotFound},
	} {
		rec := e.do(http.MethodGet, srvPath(id, "/files/content?path="+url.QueryEscape(tc.path)), nil, &admin)
		if rec.Code != tc.code || errCode(t, rec) != tc.ec {
			t.Errorf("%q: %d %s, want %d %s", tc.path, rec.Code, rec.Body, tc.code, tc.ec)
		}
	}
	if calls := e.files.takeCalls(); len(calls) != 0 {
		t.Fatalf("rejected paths reached the service: %v", calls)
	}
}

func TestFilesPermissions(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	other := createInstance(t, e.db, "creative")
	stub := storetest.CreateServer(t, e.db, "lobby")
	viewer := storetest.CreateUser(t, e.db, "viewer", "user")
	grant(t, e.db, viewer, id, "server.view", "server.console")
	filer := storetest.CreateUser(t, e.db, "filer", "user")
	grant(t, e.db, filer, id, "server.view", "server.files")
	pv, pf, admin := userP(viewer, "viewer"), userP(filer, "filer"), e.admin()
	who := map[string]*principal.Principal{"viewer": &pv, "filer": &pf, "admin": &admin, "": nil}
	cases := []struct {
		name string
		path string
		who  string
		code int
	}{
		{"view+console only", srvPath(id, "/files"), "viewer", http.StatusForbidden},
		{"files on own server", srvPath(id, "/files"), "filer", http.StatusOK},
		{"files on other server", srvPath(other, "/files"), "filer", http.StatusForbidden},
		{"admin on stub server", srvPath(stub, "/files"), "admin", http.StatusNotFound},
		{"anonymous", srvPath(id, "/files"), "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		rec := e.do(http.MethodGet, tc.path, nil, who[tc.who])
		if rec.Code != tc.code {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body, tc.code)
		}
	}
}

type contentJSON struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	MTime   int64  `json:"mtime"`
	Size    int64  `json:"size"`
	Version string `json:"version"`
}

func TestReadFile(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	e.files.read = filesapi.ReadResponse{Content: "motd=hi\nrcon.password=********\n", MTime: 1_760_184_000_000, Size: 31, Version: "v1"}
	rec := e.do(http.MethodGet, srvPath(id, "/files/content?path=server.properties"), nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}
	got := decode[contentJSON](t, rec)
	if got.Path != "server.properties" || got.Content != e.files.read.Content || got.MTime != 1_760_184_000_000 || got.Size != 31 || got.Version != "v1" {
		t.Fatalf("read = %+v", got)
	}
	if calls := e.files.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("read %d server.properties", id)}) {
		t.Fatalf("calls = %v", calls)
	}
	if len(e.rec.Entries) != 0 {
		t.Fatalf("reading must not be audited: %v", e.rec.Actions())
	}
}

func TestWriteFile(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	mt := int64(1_760_184_000_000)
	e.files.writeRes = filesapi.WriteResponse{MTime: mt + 1, Size: 5, Version: "v2"}
	rec := e.do(http.MethodPut, srvPath(id, "/files/content"), map[string]any{"path": "bukkit.yml", "content": "a: 1\n", "expected_mtime": mt, "expected_version": "v1"}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("write: %d %s", rec.Code, rec.Body)
	}
	if got := decode[map[string]any](t, rec); len(got) != 3 || got["mtime"] != float64(mt+1) || got["size"] != float64(5) || got["version"] != "v2" {
		t.Fatalf("write result = %+v", got)
	}
	w := e.files.writes[0]
	if w.ID != id || w.Path != "bukkit.yml" || w.Content != "a: 1\n" || w.ExpectedMTime == nil || *w.ExpectedMTime != mt ||
		w.ExpectedVersion == nil || *w.ExpectedVersion != "v1" {
		t.Fatalf("write request = %+v", w)
	}
	last := e.lastEntry()
	if last.Action != audit.ActionFileWrite || last.Target != fmt.Sprintf("server:%d", id) ||
		last.Result != audit.ResultSuccess || last.Details["path"] != "bukkit.yml" {
		t.Fatalf("audit = %+v", last)
	}

	// a new file: expected_mtime null
	e.do(http.MethodPut, srvPath(id, "/files/content"), map[string]any{"path": "new.txt", "content": "x", "expected_mtime": nil}, &admin)
	if got := e.files.writes[1]; got.ExpectedMTime != nil || got.ExpectedVersion != nil {
		t.Fatalf("new file expected_mtime = %v, expected_version = %v", got.ExpectedMTime, got.ExpectedVersion)
	}

	// conflict → 409 file_modified, audited as failure
	e.files.setErr(fmt.Errorf("%w: mtime differs", filesapi.ErrModified))
	rec = e.do(http.MethodPut, srvPath(id, "/files/content"), map[string]any{"path": "bukkit.yml", "content": "b", "expected_mtime": mt}, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeFileModified {
		t.Fatalf("modified: %d %s", rec.Code, rec.Body)
	}
	if last := e.lastEntry(); last.Action != audit.ActionFileWrite || last.Result != audit.ResultFailure {
		t.Fatalf("audit = %+v", last)
	}
	e.files.setErr(nil)

	// unknown field and the data directory itself are rejected before the service, not audited
	n := len(e.rec.Entries)
	rec = e.do(http.MethodPut, srvPath(id, "/files/content"), map[string]any{"path": "a", "content": "", "expected_mtime": nil, "x": 1}, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeBadRequest {
		t.Fatalf("unknown field: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(http.MethodPut, srvPath(id, "/files/content"), map[string]any{"path": "", "content": "", "expected_mtime": nil}, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
		t.Fatalf("root: %d %s", rec.Code, rec.Body)
	}
	if len(e.rec.Entries) != n {
		t.Fatalf("rejected writes were audited: %v", e.rec.Actions())
	}
}

// The contract requires content and expected_mtime: a missing expected_mtime is not null (a
// client that forgot it must not be treated as "create a new file"), and a missing content
// must not empty the file.
func TestWriteFileRequiresFields(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	for _, tc := range []struct {
		name string
		body string
		code int
		ec   string
	}{
		{"expected_mtime missing", `{"path":"a.yml","content":"x"}`, http.StatusBadRequest, httpx.CodeValidation},
		{"content missing", `{"path":"a.yml","expected_mtime":1}`, http.StatusBadRequest, httpx.CodeValidation},
		{"content null", `{"path":"a.yml","content":null,"expected_mtime":1}`, http.StatusBadRequest, httpx.CodeValidation},
		{"expected_mtime not a number", `{"path":"a.yml","content":"x","expected_mtime":"1"}`, http.StatusBadRequest, httpx.CodeBadRequest},
		{"expected_version with a new file", `{"path":"a.yml","content":"x","expected_mtime":null,"expected_version":"v1"}`, http.StatusBadRequest, httpx.CodeValidation},
		{"expected_version empty", `{"path":"a.yml","content":"x","expected_mtime":1,"expected_version":""}`, http.StatusBadRequest, httpx.CodeValidation},
		{"expected_version too long", `{"path":"a.yml","content":"x","expected_mtime":1,"expected_version":"` + strings.Repeat("a", 65) + `"}`, http.StatusBadRequest, httpx.CodeValidation},
	} {
		rec := e.doRaw(http.MethodPut, srvPath(id, "/files/content"), strings.NewReader(tc.body), &admin)
		if rec.Code != tc.code || errCode(t, rec) != tc.ec {
			t.Errorf("%s: %d %s, want %d %s", tc.name, rec.Code, rec.Body, tc.code, tc.ec)
		}
	}
	if calls := e.files.takeCalls(); len(calls) != 0 {
		t.Fatalf("rejected writes reached the service: %v", calls)
	}
	if len(e.rec.Entries) != 0 {
		t.Fatalf("rejected writes were audited: %v", e.rec.Actions())
	}

	// explicit null creates a new file, a number and an empty content are passed on
	for _, body := range []string{
		`{"path":"new.txt","content":"","expected_mtime":null}`,
		`{"path":"old.txt","content":"","expected_mtime":7}`,
	} {
		if rec := e.doRaw(http.MethodPut, srvPath(id, "/files/content"), strings.NewReader(body), &admin); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if w := e.files.writes; len(w) != 2 || w[0].ExpectedMTime != nil || w[1].ExpectedMTime == nil || *w[1].ExpectedMTime != 7 || w[1].Content != "" {
		t.Fatalf("writes = %+v", w)
	}
}

func TestManageFiles(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	if rec := e.do(http.MethodPost, srvPath(id, "/files/mkdir"), map[string]string{"path": "plugins/new"}, &admin); rec.Code != http.StatusCreated {
		t.Fatalf("mkdir: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodPost, srvPath(id, "/files/rename"), map[string]string{"from": "a.yml", "to": "plugins/a.yml"}, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodPost, srvPath(id, "/files/delete"), map[string]string{"path": "old"}, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	want := []string{fmt.Sprintf("mkdir %d plugins/new", id), fmt.Sprintf("rename %d a.yml plugins/a.yml", id), fmt.Sprintf("delete %d old", id)}
	if calls := e.files.takeCalls(); !slices.Equal(calls, want) {
		t.Fatalf("calls = %v", calls)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionFileMkdir, audit.ActionFileRename, audit.ActionFileDelete}) {
		t.Fatalf("audit = %v", got)
	}
	if d := e.rec.Entries[1].Details; d["from"] != "a.yml" || d["to"] != "plugins/a.yml" {
		t.Fatalf("rename details = %v", d)
	}

	// hidden rename target → 404, the data directory → 400, neither reaches the service
	rec := e.do(http.MethodPost, srvPath(id, "/files/rename"), map[string]string{"from": "x", "to": ".rcon-cli.env"}, &admin)
	if rec.Code != http.StatusNotFound || errCode(t, rec) != httpx.CodeNotFound {
		t.Fatalf("hidden target: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(http.MethodPost, srvPath(id, "/files/delete"), map[string]string{"path": ""}, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
		t.Fatalf("delete root: %d %s", rec.Code, rec.Body)
	}
	if calls := e.files.takeCalls(); len(calls) != 0 {
		t.Fatalf("rejected requests reached the service: %v", calls)
	}

	// mkdir onto an existing name: 409 file_exists, audited as failure
	e.files.setErr(fmt.Errorf("%w: plugins", filesapi.ErrExists))
	rec = e.do(http.MethodPost, srvPath(id, "/files/mkdir"), map[string]string{"path": "plugins"}, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeFileExists {
		t.Fatalf("mkdir exists: %d %s", rec.Code, rec.Body)
	}
	if last := e.lastEntry(); last.Action != audit.ActionFileMkdir || last.Result != audit.ResultFailure {
		t.Fatalf("audit = %+v", last)
	}
}

func TestFilesErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code int
		ec   string
	}{
		{filesapi.ErrNotFound, http.StatusNotFound, httpx.CodeNotFound},
		{filesapi.ErrExists, http.StatusConflict, httpx.CodeFileExists},
		{filesapi.ErrModified, http.StatusConflict, httpx.CodeFileModified},
		{filesapi.ErrNotText, http.StatusUnsupportedMediaType, httpx.CodeNotText},
		{filesapi.ErrNotRegular, http.StatusBadRequest, httpx.CodeNotRegular},
		{filesapi.ErrTooLarge, http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge},
		{filesapi.ErrInsufficientStorage, http.StatusInsufficientStorage, httpx.CodeInsufficientStorage},
		{filesapi.ErrBusy, http.StatusTooManyRequests, httpx.CodeRateLimited},
		{filesapi.ErrInvalid, http.StatusBadRequest, httpx.CodeValidation},
		{filesapi.ErrUnavailable, http.StatusServiceUnavailable, httpx.CodeFilesUnavailable},
		{context.DeadlineExceeded, http.StatusServiceUnavailable, httpx.CodeFilesUnavailable},
		{errors.New("boom"), http.StatusInternalServerError, httpx.CodeInternal},
	}
	for _, tc := range cases {
		e := newEnv(t)
		id := createInstance(t, e.db, "survival")
		admin := e.admin()
		e.files.setErr(fmt.Errorf("service: %w", tc.err))
		rec := e.do(http.MethodGet, srvPath(id, "/files?path=x"), nil, &admin)
		if rec.Code != tc.code || errCode(t, rec) != tc.ec {
			t.Errorf("%v: %d %s, want %d %s", tc.err, rec.Code, rec.Body, tc.code, tc.ec)
		}
		if tc.code == http.StatusTooManyRequests && rec.Header().Get("Retry-After") != "5" {
			t.Errorf("busy: Retry-After = %q", rec.Header().Get("Retry-After"))
		}
	}
}

func TestNoFileService(t *testing.T) {
	e := newEnvWith(t, func(d *serverfiles.Deps) { d.Files = nil })
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	rec := e.do(http.MethodGet, srvPath(id, "/files"), nil, &admin)
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != httpx.CodeFilesUnavailable {
		t.Fatalf("no service: %d %s", rec.Code, rec.Body)
	}
}

// A client that left during the service call gets no answer and no 500 in the log.
func TestClientGoneIsQuiet(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.files.onCall = cancel
	req := httptestRequestWithContext(ctx, http.MethodGet, srvPath(id, "/files?path=x"), admin)
	rec := newRecorder()
	e.mux.ServeHTTP(rec, req)
	if rec.Body.Len() != 0 {
		t.Fatalf("answer to a departed client: %d %s", rec.Code, rec.Body)
	}
}
