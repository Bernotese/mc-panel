package filesapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	_ "time/tzdata" // ValidTimezone must not depend on the host's zoneinfo

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

var (
	snapA = strings.Repeat("a1", 32)
	snapB = strings.Repeat("b2", 32)
)

func TestValidSnapshotID(t *testing.T) {
	for _, ok := range []string{snapA, strings.Repeat("0", 64), "0123456789abcdef" + strings.Repeat("f", 48)} {
		if !filesapi.ValidSnapshotID(ok) {
			t.Errorf("ValidSnapshotID(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", snapA[:63], snapA + "0", strings.ToUpper(snapA), strings.Repeat("g", 64),
		snapA[:63] + " ", "../" + snapA[3:], snapA[:8]} {
		if filesapi.ValidSnapshotID(bad) {
			t.Errorf("ValidSnapshotID(%q) = true", bad)
		}
	}
}

func TestValidNote(t *testing.T) {
	for _, ok := range []string{"", "vor Update auf 1.21.11", strings.Repeat("ä", 64), "a b", " leading"} {
		if !filesapi.ValidNote(ok) {
			t.Errorf("ValidNote(%q) = false", ok)
		}
	}
	for _, bad := range []string{strings.Repeat("x", 65), "line\nbreak", "tab\there", "del\x7f", "   ", "\xff\xfe", "nul\x00"} {
		if filesapi.ValidNote(bad) {
			t.Errorf("ValidNote(%q) = true", bad)
		}
	}
}

func TestValidTimezone(t *testing.T) {
	for _, ok := range []string{"Europe/Berlin", "UTC", "America/New_York", "Asia/Kolkata"} {
		if !filesapi.ValidTimezone(ok) {
			t.Errorf("ValidTimezone(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "Local", "Mars/Olympus", "../etc/passwd", "/etc/localtime", "Europe/Berlin\n", strings.Repeat("A", 65)} {
		if filesapi.ValidTimezone(bad) {
			t.Errorf("ValidTimezone(%q) = true", bad)
		}
	}
}

func TestSnapshotKindValid(t *testing.T) {
	for _, k := range []filesapi.SnapshotKind{filesapi.SnapshotScheduled, filesapi.SnapshotManual, filesapi.SnapshotPreRestore} {
		if !k.Valid() {
			t.Errorf("%q invalid", k)
		}
	}
	for _, k := range []filesapi.SnapshotKind{"", "weekly", "Manual", "pre_restore"} {
		if k.Valid() {
			t.Errorf("%q valid", k)
		}
	}
}

func TestForgetPolicyValidate(t *testing.T) {
	for _, ok := range []filesapi.ForgetPolicy{{KeepLast: 1}, {KeepMonthly: 1000}, {KeepLast: 24, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", ok, err)
		}
	}
	for _, bad := range []filesapi.ForgetPolicy{{}, {KeepLast: -1, KeepDaily: 1}, {KeepWeekly: 1001}} {
		if err := bad.Validate(); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("Validate(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestForgetRequestValidate(t *testing.T) {
	pol := &filesapi.ForgetPolicy{KeepLast: 3}
	tooMany := make([]string, filesapi.MaxForgetIDs+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("%064x", i)
	}
	for _, ok := range []filesapi.ForgetRequest{
		{ID: 1, Snapshots: []string{snapA}},
		{ID: 1, Snapshots: []string{snapA, snapB}},
		{ID: 1, Snapshots: tooMany[:filesapi.MaxForgetIDs]},
		{ID: 1, Policy: pol, Timezone: "Europe/Berlin"},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", ok, err)
		}
	}
	for i, bad := range []filesapi.ForgetRequest{
		{ID: 0, Snapshots: []string{snapA}},
		{ID: 1},
		{ID: 1, Snapshots: []string{snapA}, Policy: pol, Timezone: "UTC"},
		{ID: 1, Snapshots: []string{snapA, snapA}},
		{ID: 1, Snapshots: []string{"nope"}},
		{ID: 1, Snapshots: []string{snapA}, Timezone: "UTC"},
		{ID: 1, Snapshots: tooMany},
		{ID: 1, Policy: pol},
		{ID: 1, Policy: pol, Timezone: "Mars/Olympus"},
		{ID: 1, Policy: &filesapi.ForgetPolicy{}, Timezone: "UTC"},
	} {
		if err := bad.Validate(); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("case %d: Validate(%+v) = %v, want ErrInvalid", i, bad, err)
		}
	}
}

func TestCreateBackupRequestValidate(t *testing.T) {
	for _, ok := range []filesapi.CreateBackupRequest{
		{ID: 1, Kind: filesapi.SnapshotManual, Note: "vor Update"},
		{ID: 1, Kind: filesapi.SnapshotManual},
		{ID: 1, Kind: filesapi.SnapshotScheduled},
		{ID: 1, Kind: filesapi.SnapshotPreRestore},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", ok, err)
		}
	}
	for _, bad := range []filesapi.CreateBackupRequest{
		{ID: 0, Kind: filesapi.SnapshotManual},
		{ID: 1, Kind: "weekly"},
		{ID: 1, Kind: filesapi.SnapshotScheduled, Note: "only manual snapshots have notes"},
		{ID: 1, Kind: filesapi.SnapshotManual, Note: "a\nb"},
	} {
		if err := bad.Validate(); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("Validate(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestJobCarriesKindSnapshotAndSkipped(t *testing.T) {
	b, err := json.Marshal(filesapi.Job{ID: "j1", Kind: filesapi.JobBackup, State: filesapi.JobDone, Snapshot: snapA, Skipped: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind":"backup"`, `"snapshot":"` + snapA + `"`, `"skipped":2`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s lacks %s", b, want)
		}
	}
	var old filesapi.Job // an extract job of a service before sub-project 4
	if err := json.Unmarshal([]byte(`{"id":"j","state":"running","path":"w.zip","dest":"w"}`), &old); err != nil || old.Kind != "" {
		t.Fatalf("old job = %+v, %v", old, err)
	}
}

// recorder notes "METHOD URI BODY" of every request (the handler runs on another goroutine).
type recorder struct {
	mu   sync.Mutex
	reqs []string
}

func (rc *recorder) add(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.reqs = append(rc.reqs, r.Method+" "+r.URL.RequestURI()+" "+strings.TrimSpace(string(body)))
}

func (rc *recorder) all() []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]string(nil), rc.reqs...)
}

// backupService answers every backup endpoint like the service would.
func backupService(t *testing.T, rc *recorder) *filesapi.UnixClient {
	return serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.add(r)
		switch r.URL.Path {
		case filesapi.PathBackupsStatus:
			writeJSON(w, 200, filesapi.BackupsStatus{Available: true, Version: "0.19.1"})
		case filesapi.PathBackupsList:
			writeJSON(w, 200, filesapi.SnapshotsResponse{Snapshots: []filesapi.Snapshot{
				{ID: snapA, ShortID: snapA[:8], Time: 5, Kind: filesapi.SnapshotManual, Note: "n", Bytes: 9},
			}})
		case filesapi.PathBackupsCreate, filesapi.PathBackupsRestore, filesapi.PathBackupsForget:
			writeJSON(w, 202, filesapi.Job{ID: "j1", Kind: filesapi.JobBackup, State: filesapi.JobRunning})
		case filesapi.PathBackupsPin:
			writeJSON(w, 200, filesapi.SnapshotResponse{Snapshot: filesapi.Snapshot{ID: snapB, Kind: filesapi.SnapshotScheduled, Pinned: true}})
		case filesapi.PathBackupsPurge, filesapi.PathJobsCancel:
			w.WriteHeader(http.StatusNoContent)
		case filesapi.PathBackupsDownload:
			w.Header().Set(filesapi.HeaderKind, "dir")
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte("PK"))
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
}

func TestClientBackupCalls(t *testing.T) {
	rc := &recorder{}
	c := backupService(t, rc)
	ctx := context.Background()

	if st, err := c.BackupsStatus(ctx); err != nil || !st.Available || st.Version != "0.19.1" {
		t.Fatalf("BackupsStatus = %+v, %v", st, err)
	}
	if list, err := c.Snapshots(ctx, 7); err != nil || len(list) != 1 || list[0].ID != snapA || list[0].Note != "n" {
		t.Fatalf("Snapshots = %+v, %v", list, err)
	}
	if j, err := c.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 7, Kind: filesapi.SnapshotManual, Note: "vor Update"}); err != nil || j.ID != "j1" {
		t.Fatalf("CreateBackup = %+v, %v", j, err)
	}
	if _, err := c.RestoreBackup(ctx, 7, snapA); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	pol := &filesapi.ForgetPolicy{KeepLast: 24, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6}
	if _, err := c.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 7, Policy: pol, Timezone: "Europe/Berlin"}); err != nil {
		t.Fatalf("ForgetBackups(policy): %v", err)
	}
	if _, err := c.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 7, Snapshots: []string{snapA}}); err != nil {
		t.Fatalf("ForgetBackups(ids): %v", err)
	}
	if s, err := c.PinBackup(ctx, 7, snapA, true); err != nil || s.ID != snapB || !s.Pinned {
		t.Fatalf("PinBackup = %+v, %v", s, err)
	}
	d, err := c.DownloadBackup(ctx, 7, snapA)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(d.Body)
	_ = d.Body.Close()
	if string(body) != "PK" || !d.IsDir || d.Size != -1 || d.Name != "" {
		t.Fatalf("DownloadBackup = %+v %q", d, body)
	}
	if err := c.PurgeBackups(ctx, 7); err != nil {
		t.Fatalf("PurgeBackups: %v", err)
	}
	if err := c.CancelJob(ctx, 7); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	want := []string{
		"GET /v1/backups/status ",
		`POST /v1/backups/list {"id":7}`,
		`POST /v1/backups/create {"id":7,"kind":"manual","note":"vor Update"}`,
		`POST /v1/backups/restore {"id":7,"snapshot":"` + snapA + `"}`,
		`POST /v1/backups/forget {"id":7,"policy":{"keep_last":24,"keep_daily":7,"keep_weekly":4,"keep_monthly":6},"timezone":"Europe/Berlin"}`,
		`POST /v1/backups/forget {"id":7,"snapshots":["` + snapA + `"]}`,
		`POST /v1/backups/pin {"id":7,"snapshot":"` + snapA + `","pinned":true}`,
		"GET /v1/backups/download?id=7&snapshot=" + snapA + " ",
		`POST /v1/backups/purge {"id":7}`,
		`POST /v1/jobs/cancel {"id":7}`,
	}
	got := rc.all()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestClientBackupsRejectBadInputWithoutRequest(t *testing.T) {
	var calls atomic.Int32
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx := context.Background()
	errs := map[string]error{}
	_, errs["snapshots id 0"] = c.Snapshots(ctx, 0)
	_, errs["create kind"] = c.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: "weekly"})
	_, errs["create note on scheduled"] = c.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotScheduled, Note: "x"})
	_, errs["restore snapshot"] = c.RestoreBackup(ctx, 1, "abc")
	_, errs["forget empty"] = c.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1})
	_, errs["pin upper case"] = c.PinBackup(ctx, 1, strings.ToUpper(snapA), true)
	_, errs["download snapshot"] = c.DownloadBackup(ctx, 1, "x")
	errs["purge id 0"] = c.PurgeBackups(ctx, 0)
	errs["cancel id -1"] = c.CancelJob(ctx, -1)
	for name, err := range errs {
		if !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d requests sent for invalid input", n)
	}
}

func TestClientBackupsUnavailable(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, filesapi.ErrorResponse{Error: "restic not found", Code: filesapi.CodeUnavailable})
	}))
	_, err := c.Snapshots(context.Background(), 1)
	if !errors.Is(err, filesapi.ErrBackupsUnavailable) || errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("Snapshots = %v, want ErrBackupsUnavailable only", err)
	}
}

func TestClientSnapshotsNeverNil(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"snapshots":null}`))
	}))
	list, err := c.Snapshots(context.Background(), 1)
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("Snapshots = %#v, %v; want an empty, non-nil list", list, err)
	}
}

// A service that answers a pin without a valid snapshot ID is broken: the panel would
// otherwise lose track of the snapshot (restic gives it a new ID).
func TestClientPinNeedsANewSnapshotID(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, filesapi.SnapshotResponse{})
	}))
	if _, err := c.PinBackup(context.Background(), 1, snapA, true); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("PinBackup = %v, want ErrUnavailable", err)
	}
}

func TestClientDownloadBackupNeedsAZip(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(filesapi.HeaderKind, "file")
		_, _ = w.Write([]byte("data"))
	}))
	if _, err := c.DownloadBackup(context.Background(), 1, snapA); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("DownloadBackup = %v, want ErrUnavailable", err)
	}
}

// List, pin, restore and forget read restic's index and get BackupCallTimeout, not the 10 s of
// the small calls.
func TestClientBackupCallsHaveTheirOwnTimeout(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		writeJSON(w, 200, filesapi.SnapshotsResponse{Snapshots: []filesapi.Snapshot{}})
	}))
	filesapi.SetCallTimeouts(c, 50*time.Millisecond, 50*time.Millisecond)
	filesapi.SetBackupTimeout(c, 5*time.Second)
	if _, err := c.Snapshots(context.Background(), 1); err != nil {
		t.Fatalf("Snapshots with a long backup timeout: %v", err)
	}
	filesapi.SetBackupTimeout(c, 50*time.Millisecond)
	if _, err := c.Snapshots(context.Background(), 1); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("Snapshots past the backup timeout = %v, want ErrUnavailable", err)
	}
}
