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
