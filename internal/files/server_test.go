package files

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestServerHealthAndRouting(t *testing.T) {
	s, _ := newService(t)
	ts := startHTTP(t, NewServer(s, "1.2.3", discardLog()))

	resp, err := http.Get(ts.URL + filesapi.PathHealth)
	if err != nil {
		t.Fatal(err)
	}
	var h filesapi.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || h.Status != "ok" || h.Version != "1.2.3" {
		t.Fatalf("health: %d %+v", resp.StatusCode, h)
	}

	resp, err = http.Get(ts.URL + "/v1/nope")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path: %d", resp.StatusCode)
	}

	resp, err = http.Post(ts.URL+filesapi.PathHealth, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodGet {
		t.Fatalf("wrong method: %d allow=%q", resp.StatusCode, resp.Header.Get("Allow"))
	}
}
