package files

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// getJob asks GET /v1/jobs?id= and fails the test unless it answers within limit.
func getJob(t *testing.T, ts string, id int64, limit time.Duration) *filesapi.Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts+filesapi.PathJobs+"?id="+strconv.FormatInt(id, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/jobs did not answer within %s: %v", limit, err)
	}
	defer resp.Body.Close()
	var jr filesapi.JobsResponse
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&jr) != nil {
		t.Fatalf("GET /v1/jobs: %d", resp.StatusCode)
	}
	return jr.Job
}

// The panel's start gate relies on these guarantees (power start/restart and the reconcile
// loop ask GET /v1/jobs before starting a server):
//  1. a 202 from POST /v1/extract means the job is already registered as running;
//  2. a second extract for the same instance is refused with 429 busy while one runs;
//  3. GET /v1/jobs is never throttled by the upload or job limits and answers at once,
//     also while every upload slot is taken and a job is busy.
func TestJobsGuaranteesForThePanel(t *testing.T) {
	s, data := newService(t)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	s.jobs.hook = func(ctx context.Context, _ int64, _ string) {
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "f.txt", body: "x"}))
	ts := startHTTP(t, NewServer(s, "test", discardLog()))

	// (1) Registered before the answer: the very next GET sees the running job.
	status, body := postJSON(t, ts, filesapi.PathExtract, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out"})
	var started filesapi.Job
	if status != http.StatusAccepted || json.Unmarshal(body, &started) != nil || started.State != jobRunning {
		t.Fatalf("extract: %d %s", status, body)
	}
	if j := getJob(t, ts.URL, 1, 2*time.Second); j == nil || j.ID != started.ID || j.State != jobRunning {
		t.Fatalf("job right after the 202: %+v, want the running job %s", j, started.ID)
	}

	// (2) One job per instance.
	status, body = postJSON(t, ts, filesapi.PathExtract, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out2"})
	if status != http.StatusTooManyRequests || errCode(t, body) != filesapi.CodeBusy {
		t.Fatalf("second extract for the same server: %d %s", status, body)
	}

	// (3) Fill every upload slot with stalled uploads, then ask for the job again.
	stall := make(chan struct{})
	done := make(chan struct{}, maxUploads)
	for i := range maxUploads {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = s.Upload(context.Background(), 1, "slow"+string(rune('a'+i)), false, 5, blockingReader{stall})
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.uploads) < maxUploads {
		if time.Now().After(deadline) {
			t.Fatal("uploads did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for range 20 {
		if j := getJob(t, ts.URL, 1, time.Second); j == nil || j.State != jobRunning {
			t.Fatalf("job while uploads and the job are busy: %+v", j)
		}
	}
	if j := getJob(t, ts.URL, 2, time.Second); j != nil {
		t.Fatalf("instance without a job: %+v", j)
	}
	close(stall)
	for range maxUploads {
		<-done
	}

	close(release)
	if j := waitJob(t, s, 1); j.State != jobDone || j.ID != started.ID {
		t.Fatalf("finished job: %+v", j)
	}
	if j := getJob(t, ts.URL, 1, time.Second); j == nil || j.State != jobDone {
		t.Fatalf("job after the end: %+v", j)
	}
}
