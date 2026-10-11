package filesapi_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// Review B1: deleting a large folder and the extract pre-check may take minutes; they have
// their own timeouts instead of the 10 s of the small calls.
func TestClientLongOperationsHaveTheirOwnTimeout(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		if r.URL.Path == filesapi.PathExtract {
			writeJSON(w, http.StatusAccepted, filesapi.Job{ID: "j", State: filesapi.JobRunning})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx := context.Background()
	filesapi.SetCallTimeouts(c, 100*time.Millisecond, 5*time.Second)
	if err := c.Delete(ctx, 1, "big"); err != nil {
		t.Fatalf("slow delete: %v", err)
	}
	if j, err := c.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "w.zip", Dest: "w"}); err != nil || j.ID != "j" {
		t.Fatalf("slow extract: %+v %v", j, err)
	}
	if err := c.Mkdir(ctx, 1, "x"); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("small calls keep their short timeout: %v", err)
	}
	filesapi.SetCallTimeouts(c, 100*time.Millisecond, 150*time.Millisecond)
	if err := c.Delete(ctx, 1, "big"); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("the long timeout is a bound as well: %v", err)
	}
	if filesapi.DeleteTimeout < time.Minute || filesapi.ExtractTimeout < time.Minute {
		t.Fatalf("long timeouts too short: delete %s, extract %s", filesapi.DeleteTimeout, filesapi.ExtractTimeout)
	}
}
