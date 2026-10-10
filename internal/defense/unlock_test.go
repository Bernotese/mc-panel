package defense

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/audit"
	"mcpanel/internal/store"
)

func TestUnlock(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.insertJail(t, "203.0.113.1/32", t0, t0.Add(time.Hour))
	e.insertJail(t, "2001:db8:1:2::/64", t0, t0.Add(time.Hour))
	e.insertJail(t, "203.0.113.2/32", t0.Add(-2*time.Hour), t0.Add(-time.Hour))
	e.saveSettings(t, store.SecuritySettings{Enforce2FAForUsers: true, AdminAllowlistOnly: true, AllowedCountries: []string{"DE", "AT"}})
	rec := &audit.MemoryRecorder{}
	var out bytes.Buffer

	res, err := Unlock(ctx, e.db, rec, e.clk, &out)
	if err != nil {
		t.Fatal(err)
	}
	if res != (UnlockResult{JailsLifted: 2, AdminAllowlistDisabled: true, CountryFilterCleared: true}) {
		t.Fatalf("result = %+v", res)
	}
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("active jails left: %+v", js)
	}
	var liftedBy string
	if err := e.db.QueryRow("SELECT lifted_by FROM jails WHERE ip_key = '203.0.113.1/32'").Scan(&liftedBy); err != nil || liftedBy != "cli" {
		t.Fatalf("lifted_by = %q, %v", liftedBy, err)
	}
	st, err := store.LoadSecuritySettings(ctx, e.q)
	if err != nil || st.AdminAllowlistOnly || len(st.AllowedCountries) != 0 || !st.Enforce2FAForUsers {
		t.Fatalf("settings = %+v, %v", st, err)
	}
	if len(rec.Entries) != 2 {
		t.Fatalf("audit = %+v", rec.Entries)
	}
	for _, en := range rec.Entries {
		if en.ActorType != audit.ActorCLI || en.ActorName != "cli" || en.Result != audit.ResultSuccess {
			t.Fatalf("audit entry = %+v", en)
		}
	}
	if rec.Entries[0].Action != audit.ActionJailLift || rec.Entries[1].Action != audit.ActionSettingsUpdate {
		t.Fatalf("actions = %v", rec.Actions())
	}
	for _, want := range []string{"Lifted 2 active jail(s).", "Disabled", "Cleared country filter (was: DE, AT).", "Deny rules were not changed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q missing %q", out.String(), want)
		}
	}
}

func TestUnlockNothingToDo(t *testing.T) {
	e := newEnv(t)
	rec := &audit.MemoryRecorder{}
	var out bytes.Buffer
	res, err := Unlock(context.Background(), e.db, rec, e.clk, &out)
	if err != nil || res != (UnlockResult{}) {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if len(rec.Entries) != 0 {
		t.Fatalf("no audit entries expected: %+v", rec.Entries)
	}
	if !strings.Contains(out.String(), "Lifted 0 active jail(s).") {
		t.Fatalf("output = %q", out.String())
	}
}
