package files

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestServerBackupsStatus(t *testing.T) {
	s, _, _ := newBackupService(t)
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	resp, err := http.Get(ts.URL + filesapi.PathBackupsStatus)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st filesapi.BackupsStatus
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&st) != nil || !st.Available {
		t.Fatalf("status %d %+v", resp.StatusCode, st)
	}
	// Without restic the status still answers.
	s2, _ := newService(t)
	s2.InitBackups(t.Context())
	ts2 := startHTTP(t, NewServer(s2, "test", discardLog()))
	resp2, err := http.Get(ts2.URL + filesapi.PathBackupsStatus)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status without restic: %d", resp2.StatusCode)
	}
}

func TestServerBackupsCreateAndList(t *testing.T) {
	s, data, _ := newBackupService(t)
	put(t, data, "a.txt", "a")
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	status, body := postJSON(t, ts, filesapi.PathBackupsCreate, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual})
	if status != http.StatusAccepted {
		t.Fatalf("create: %d %s", status, body)
	}
	waitJob(t, s, 1)
	status, body = postJSON(t, ts, filesapi.PathBackupsList, filesapi.IDRequest{ID: 1})
	var res filesapi.SnapshotsResponse
	if status != http.StatusOK || json.Unmarshal(body, &res) != nil || len(res.Snapshots) != 1 {
		t.Fatalf("list: %d %s", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathBackupsCreate, map[string]any{"id": 1, "kind": "manual", "extra": 1})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("unknown field: %d %s", status, body)
	}
	// Without restic: 503 unavailable.
	s2, _ := newService(t)
	s2.InitBackups(t.Context())
	ts2 := startHTTP(t, NewServer(s2, "test", discardLog()))
	status, body = postJSON(t, ts2, filesapi.PathBackupsList, filesapi.IDRequest{ID: 1})
	if status != http.StatusServiceUnavailable || errCode(t, body) != filesapi.CodeUnavailable {
		t.Fatalf("list without restic: %d %s", status, body)
	}
}

func TestServerRestoreAndCancel(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "now")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "then")))
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	status, body := postJSON(t, ts, filesapi.PathBackupsRestore, filesapi.SnapshotRequest{ID: 1, Snapshot: sid})
	if status != http.StatusAccepted {
		t.Fatalf("restore: %d %s", status, body)
	}
	waitJob(t, s, 1)
	if got, _ := get(t, data, "a.txt"); got != "then" {
		t.Fatalf("a.txt = %q", got)
	}
	status, body = postJSON(t, ts, filesapi.PathBackupsRestore, filesapi.SnapshotRequest{ID: 1, Snapshot: strings.Repeat("b", 64)})
	if status != http.StatusNotFound || errCode(t, body) != filesapi.CodeNotFound {
		t.Fatalf("unknown snapshot: %d %s", status, body)
	}
	if status, body = postJSON(t, ts, filesapi.PathJobsCancel, filesapi.IDRequest{ID: 1}); status != http.StatusNoContent {
		t.Fatalf("cancel without job: %d %s", status, body)
	}
}

func TestServerForgetPinPurgeDownload(t *testing.T) {
	s, _, f := newBackupService(t)
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "hello")))
	ts := startHTTP(t, NewServer(s, "test", discardLog()))

	resp, err := http.Get(ts.URL + filesapi.PathBackupsDownload + "?id=1&snapshot=" + sid)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get(filesapi.HeaderKind) != "dir" || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("download: %d %v", resp.StatusCode, resp.Header)
	}
	if zipNames(t, body)["a.txt"] != "hello" {
		t.Fatal("download content")
	}
	resp, err = http.Get(ts.URL + filesapi.PathBackupsDownload + "?id=1&snapshot=xyz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad snapshot id: %d", resp.StatusCode)
	}

	status, b := postJSON(t, ts, filesapi.PathBackupsPin, filesapi.PinRequest{ID: 1, Snapshot: sid, Pinned: true})
	if status != http.StatusBadRequest || errCode(t, b) != filesapi.CodeInvalid { // a manual snapshot
		t.Fatalf("pin manual: %d %s", status, b)
	}
	status, b = postJSON(t, ts, filesapi.PathBackupsForget, filesapi.ForgetRequest{ID: 1, Snapshots: []string{sid}})
	if status != http.StatusAccepted {
		t.Fatalf("forget: %d %s", status, b)
	}
	waitJob(t, s, 1)
	if status, b = postJSON(t, ts, filesapi.PathBackupsPurge, filesapi.IDRequest{ID: 1}); status != http.StatusNoContent {
		t.Fatalf("purge: %d %s", status, b)
	}
}
