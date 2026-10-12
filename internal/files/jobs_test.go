package files

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// TestJobsOnePerInstanceAcrossKinds: extract, backup, restore, forget and the pin/purge lock
// exclude each other per instance (spec 4 §4.1); other instances are not affected.
func TestJobsOnePerInstanceAcrossKinds(t *testing.T) {
	kinds := []filesapi.JobKind{filesapi.JobExtract, filesapi.JobBackup, filesapi.JobRestore, filesapi.JobForget, jobLock}
	for _, first := range kinds {
		for _, second := range kinds {
			j := newJobs(4)
			if err := j.acquire(1, first); err != nil {
				t.Fatalf("%s: %v", first, err)
			}
			if err := j.acquire(1, second); !errors.Is(err, filesapi.ErrBusy) {
				t.Errorf("%s then %s on the same instance: %v, want busy", first, second, err)
			}
			if err := j.acquire(2, second); err != nil {
				t.Errorf("%s then %s on another instance: %v", first, second, err)
			}
			j.release(1)
			if err := j.acquire(1, second); err != nil {
				t.Errorf("%s after release: %v", second, err)
			}
		}
	}
}

// TestJobsResticSlots: restic jobs count against max_backup_jobs, extracts against maxJobsTotal;
// the two limits are independent, the pin/purge lock counts against neither.
func TestJobsResticSlots(t *testing.T) {
	j := newJobs(1)
	if err := j.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatal(err)
	}
	for _, k := range []filesapi.JobKind{filesapi.JobBackup, filesapi.JobRestore, filesapi.JobForget} {
		if err := j.acquire(2, k); !errors.Is(err, filesapi.ErrBusy) {
			t.Errorf("second restic job (%s): %v, want busy", k, err)
		}
	}
	for id := int64(2); id <= 3; id++ {
		if err := j.acquire(id, filesapi.JobExtract); err != nil {
			t.Fatalf("extract %d: %v", id, err)
		}
	}
	if err := j.acquire(4, filesapi.JobExtract); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("third extract: %v", err)
	}
	if err := j.acquire(5, jobLock); err != nil {
		t.Fatalf("lock: %v", err)
	}
	j.release(1)
	if err := j.acquire(6, filesapi.JobForget); err != nil {
		t.Fatalf("restic slot after release: %v", err)
	}
}

func TestJobsStartRecordsKindAndSnapshot(t *testing.T) {
	j := newJobs(1)
	if err := j.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	job, err := j.start(1, jobSpec{kind: filesapi.JobBackup}, time.Now, func(ctx context.Context) error {
		<-release
		j.progress(1, 10)
		j.skip(1)
		j.setSnapshot(1, "abc")
		return nil
	})
	if err != nil || job.Kind != filesapi.JobBackup || job.State != jobRunning {
		t.Fatalf("start = %+v %v", job, err)
	}
	close(release)
	j.close()
	got := j.get(1)
	if got.State != jobDone || got.Bytes != 10 || got.Entries != 1 || got.Skipped != 1 || got.Snapshot != "abc" {
		t.Fatalf("job = %+v", got)
	}
}
