package main

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// waitBackup polls the server's backup job until it is in step (or ended, step "").
func waitBackup(t *testing.T, c *client, id int64, step string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) // the panel polls the service every 2 s
	for {
		out := c.must(200, "GET", fmt.Sprintf("/servers/%d/backups/job", id), nil)
		if j, _ := out["job"].(map[string]any); j != nil {
			ended := j["state"] == "done" || j["state"] == "failed"
			if (step == "" && ended) || (step != "" && j["state"] == "running" && j["step"] == step) {
				return j
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup job never reached %q: %v", step, out)
		}
		time.Sleep(100 * time.Millisecond)
		// The API rate limit (one request per 200 ms, burst 60) runs on the fake clock: give each
		// poll its token back, the wait spans several real 2-s service polls.
		c.e.clk.Advance(200 * time.Millisecond)
	}
}

// The backup routes through the real middleware chain (CSRF, rbac, sudo, audit) against the
// fake files service; the restore holds the start gate of the servers handler.
func TestBackupsEndToEnd(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	srv := admin.must(201, "POST", "/servers", paperServer("survival"))
	id := int64(srv["id"].(float64))
	base := fmt.Sprintf("/servers/%d/backups", id)

	ov := admin.must(200, "GET", base, nil)
	if avail, _ := ov["available"].(bool); !avail || len(ov["snapshots"].([]any)) != 0 || ov["job"] != nil {
		t.Fatalf("overview = %v", ov)
	}

	// creating needs the CSRF token like every POST
	csrf := admin.csrf
	admin.csrf = ""
	if code, _ := admin.do("POST", base, map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("without CSRF = %d", code)
	}
	admin.csrf = csrf
	admin.must(202, "POST", base, map[string]any{"note": "vor Update"})
	if j := waitBackup(t, admin, id, ""); j["state"] != "done" {
		t.Fatalf("backup = %v", j)
	}
	snaps := admin.must(200, "GET", base, nil)["snapshots"].([]any)
	if len(snaps) != 1 || snaps[0].(map[string]any)["note"] != "vor Update" {
		t.Fatalf("snapshots = %v", snaps)
	}
	snap := snaps[0].(map[string]any)["id"].(string)

	sched := admin.must(200, "PUT", base+"/schedule", map[string]any{
		"mode": "daily", "interval_hours": 24, "time_of_day": 240, "weekday": 0,
		"keep_last": 24, "keep_daily": 7, "keep_weekly": 4, "keep_monthly": 6,
	})["schedule"].(map[string]any)
	if sched["timezone"] != "Europe/Berlin" || sched["next_run_at"] == nil {
		t.Fatalf("schedule = %v", sched)
	}

	// restore: while it runs, power start answers 409 (servers.AnyGate)
	release := e.backups.holdPlan("restore")
	admin.must(202, "POST", base+"/"+snap+"/restore", nil)
	// right after the 202 (the job may still wait for the worker) the gate is already closed
	if code, out := admin.do("POST", fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "start"}); code != http.StatusConflict {
		t.Fatalf("start right after the restore request = %d %v", code, out)
	}
	waitBackup(t, admin, id, "restoring")
	if code, out := admin.do("POST", fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "start"}); code != http.StatusConflict {
		t.Fatalf("start during restore = %d %v", code, out)
	}
	release()
	if j := waitBackup(t, admin, id, ""); j["state"] != "done" {
		t.Fatalf("restore = %v", j)
	}
	if n := len(admin.must(200, "GET", base, nil)["snapshots"].([]any)); n != 2 { // + pre-restore
		t.Fatalf("snapshots after restore = %d", n)
	}

	resp, body := admin.raw(base + "/" + snap + "/download")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/zip" || string(body) != string(e.backups.zip) {
		t.Fatalf("download = %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}

	admin.must(202, "DELETE", fmt.Sprintf("/servers/%d?purge_backups=1", id), nil)
	if !e.backups.purged(id) {
		t.Fatal("deleting the server with purge_backups did not purge the repository")
	}
}
