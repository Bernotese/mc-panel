package defense

import (
	"context"
	"database/sql"
	"net/netip"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/audit"
	"mcpanel/internal/notify"
	"mcpanel/internal/store"
	"mcpanel/internal/store/storetest"
)

func (e *env) fail(t *testing.T, ip string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		err := e.svc.RecordAttempt(context.Background(), Attempt{
			IP: addr(ip), Username: "admin", Stage: StagePassword, Reason: "bad_password", UserAgent: "test",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func (e *env) activeJails(t *testing.T) []store.Jail {
	t.Helper()
	js, err := e.q.DefenseListActiveJails(context.Background(), e.clk.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func TestJailDuration(t *testing.T) {
	cases := map[int64]time.Duration{
		0: 15 * time.Minute, 1: 15 * time.Minute, 2: 30 * time.Minute, 3: time.Hour,
		10: 128 * time.Hour, 11: 168 * time.Hour, 50: 168 * time.Hour,
	}
	for lvl, want := range cases {
		if got := JailDuration(lvl); got != want {
			t.Errorf("JailDuration(%d) = %v, want %v", lvl, got, want)
		}
	}
}

func TestRecordAttemptStoresRow(t *testing.T) {
	e := newEnv(t)
	e.geo["203.0.113.7"] = "DE"
	uid := storetest.CreateUser(t, e.db, "alice", "user")
	err := e.svc.RecordAttempt(context.Background(), Attempt{
		IP: addr("::ffff:203.0.113.7"), Username: strings.Repeat("x", 100), UserID: &uid,
		Stage: StageTOTP, Success: false, Reason: "bad_code", UserAgent: "Firefox",
	})
	if err != nil {
		t.Fatal(err)
	}
	var ip, key, country, stage, username string
	var success int64
	var userID sql.NullInt64
	row := e.db.QueryRow("SELECT ip, ip_key, country, stage, success, user_id, username FROM login_attempts")
	if err := row.Scan(&ip, &key, &country, &stage, &success, &userID, &username); err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.7" || key != "203.0.113.7/32" || country != "DE" || stage != "totp" || success != 0 ||
		!userID.Valid || userID.Int64 != uid || len(username) != 64 {
		t.Fatalf("row = %q %q %q %q %d %v len(username)=%d", ip, key, country, stage, success, userID, len(username))
	}
}

func TestRecordAttemptRejectsInvalidIP(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.RecordAttempt(context.Background(), Attempt{Stage: StagePassword}); err == nil {
		t.Fatal("expected error for missing IP")
	}
}

func TestJailAfterFiveFailures(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 4)
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("jailed after 4 failures: %+v", js)
	}
	e.fail(t, "203.0.113.7", 1)
	js := e.activeJails(t)
	if len(js) != 1 || js[0].IpKey != "203.0.113.7/32" || js[0].Level != 1 || js[0].Manual != 0 ||
		js[0].ExpiresAt != t0.Add(15*time.Minute).UnixMilli() {
		t.Fatalf("jails = %+v", js)
	}
	if _, ok := e.svc.snap.Load().jailedUntil(netip.MustParsePrefix("203.0.113.7/32"), t0); !ok {
		t.Fatal("jail must be visible in the snapshot without reload")
	}
	if len(e.audit.Entries) != 1 {
		t.Fatalf("audit entries = %+v", e.audit.Entries)
	}
	a := e.audit.Entries[0]
	if a.Action != audit.ActionJailAuto || a.ActorType != audit.ActorSystem || a.ActorName != "system" ||
		a.Target != "203.0.113.7/32" || a.Result != audit.ResultSuccess {
		t.Fatalf("audit entry = %+v", a)
	}
	if len(e.notes.Events) != 1 || e.notes.Events[0].Type != notify.EventJailed || e.notes.Events[0].IP != "203.0.113.7/32" {
		t.Fatalf("events = %+v", e.notes.Events)
	}
}

func TestJailIPv6SameSlash64SharesCounter(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "2001:db8:1:2::1", 3)
	e.fail(t, "2001:db8:1:2:ffff::9", 2)
	e.fail(t, "2001:db8:1:3::1", 4)
	js := e.activeJails(t)
	if len(js) != 1 || js[0].IpKey != "2001:db8:1:2::/64" {
		t.Fatalf("jails = %+v", js)
	}
}

func TestJailMappedIPv4SharesCounter(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 3)
	e.fail(t, "::ffff:203.0.113.7", 2)
	js := e.activeJails(t)
	if len(js) != 1 || js[0].IpKey != "203.0.113.7/32" {
		t.Fatalf("jails = %+v", js)
	}
}

func TestJailWindowIsTenMinutes(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 4)
	e.clk.Advance(10*time.Minute + time.Millisecond)
	e.fail(t, "203.0.113.7", 1)
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("failures outside the window must not count: %+v", js)
	}
}

func TestJailEscalation(t *testing.T) {
	e := newEnv(t)
	wantLevel := func(level int64, d time.Duration) {
		t.Helper()
		e.fail(t, "203.0.113.7", 5)
		js := e.activeJails(t)
		if len(js) != 1 || js[0].Level != level || js[0].ExpiresAt != e.clk.Now().Add(d).UnixMilli() {
			t.Fatalf("want level %d / %v, got %+v", level, d, js)
		}
		e.clk.Advance(d)
	}
	wantLevel(1, 15*time.Minute)
	wantLevel(2, 30*time.Minute)
	wantLevel(3, time.Hour)
	e.clk.Advance(31 * 24 * time.Hour)
	wantLevel(1, 15*time.Minute)
}

func TestFailuresBeforeLiftDoNotRejail(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 5)
	if _, err := e.db.Exec("UPDATE jails SET lifted_at = ?, lifted_by = 'admin'", t0.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	e.reload(t)
	e.clk.Advance(time.Second)
	e.fail(t, "203.0.113.7", 1)
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("failures before the last jail must not count again: %+v", js)
	}
	e.fail(t, "203.0.113.7", 4)
	js := e.activeJails(t)
	if len(js) != 1 || js[0].Level != 2 {
		t.Fatalf("expected level-2 jail, got %+v", js)
	}
}

func TestAllowlistedAndLoopbackNeverJailed(t *testing.T) {
	e := newEnv(t)
	e.addRule(t, "203.0.113.0/24", "allow", nil)
	e.fail(t, "203.0.113.7", 10)
	e.fail(t, "127.0.0.1", 10)
	e.fail(t, "::1", 10)
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("jails = %+v", js)
	}
	var n int
	_ = e.db.QueryRow("SELECT count(*) FROM login_attempts").Scan(&n)
	if n != 30 {
		t.Fatalf("attempts recorded = %d, want 30", n)
	}
}

func TestSuccessNotifiesNewDeviceOnce(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "alice", "user")
	ok := func(ip string, stage AttemptStage, reason string) {
		t.Helper()
		if err := e.svc.RecordAttempt(context.Background(), Attempt{
			IP: addr(ip), Username: "alice", UserID: &uid, Stage: stage, Success: true, Reason: reason,
		}); err != nil {
			t.Fatal(err)
		}
	}
	ok("203.0.113.7", StagePassword, "second_factor_pending")
	ok("203.0.113.7", StageSudo, "sudo")
	if len(e.notes.Events) != 0 {
		t.Fatalf("pending second factor and sudo are not full logins: %+v", e.notes.Events)
	}
	ok("203.0.113.7", StageTOTP, "complete")
	ok("203.0.113.7", StageWebAuthn, "complete")
	ok("198.51.100.1", StageRecovery, "complete")
	ok("198.51.100.1", StagePassword, "complete") // user without 2FA: full login at password stage
	types := e.notes.Types()
	if len(types) != 2 || types[0] != notify.EventNewDeviceLogin || types[1] != notify.EventNewDeviceLogin {
		t.Fatalf("events = %+v", e.notes.Events)
	}
	if e.notes.Events[0].Username != "alice" {
		t.Fatalf("event = %+v", e.notes.Events[0])
	}
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("successes must never jail: %+v", js)
	}
}

func TestAdminNetworkDenialsCountTowardJail(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "root", "admin")
	for i := 0; i < 5; i++ {
		if err := e.svc.RecordAttempt(context.Background(), Attempt{
			IP: addr("203.0.113.7"), Username: "root", UserID: &uid, Stage: StagePassword,
			Success: false, Reason: "admin_network",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if js := e.activeJails(t); len(js) != 1 {
		t.Fatalf("admin_network denials must count toward the jail: %+v", js)
	}
}
