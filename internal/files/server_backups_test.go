package files

import (
	"encoding/json"
	"net/http"
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
