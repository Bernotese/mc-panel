package defense

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

func settingsBody(enforce, allowlistOnly bool, countries []string) map[string]any {
	return map[string]any{"enforce_2fa_for_users": enforce, "admin_allowlist_only": allowlistOnly, "allowed_countries": countries}
}

func TestGetSettings(t *testing.T) {
	e := newEnv(t)
	got := decode[map[string]any](t, e.call(t, http.MethodGet, "/security/settings", nil, callerIP))
	if got["enforce_2fa_for_users"] != jsonFalse || got["admin_allowlist_only"] != jsonFalse || got["geoip_available"] != jsonTrue {
		t.Fatalf("settings = %v", got)
	}
	if c, ok := got["allowed_countries"].([]any); !ok || len(c) != 0 {
		t.Fatalf("allowed_countries must be an empty array, got %#v", got["allowed_countries"])
	}
	noGeo := newEnv(t, func(d *Deps) { d.GeoAvailable = false })
	if got := decode[map[string]any](t, noGeo.call(t, http.MethodGet, "/security/settings", nil, callerIP)); got["geoip_available"] != jsonFalse {
		t.Fatalf("geoip_available = %v", got["geoip_available"])
	}
}

func TestPutSettings(t *testing.T) {
	e := newEnv(t)
	e.geo["203.0.113.7"] = "US"
	rec := e.call(t, http.MethodPut, "/security/settings", settingsBody(true, false, []string{"DE", "AT", "DE"}), callerIP)
	wantStatus(t, rec, http.StatusOK, "")
	got := decode[map[string]any](t, rec)
	if got["enforce_2fa_for_users"] != jsonTrue || len(got["allowed_countries"].([]any)) != 2 || got["geoip_available"] != jsonTrue {
		t.Fatalf("response = %v", got)
	}
	stored, err := store.LoadSecuritySettings(context.Background(), e.q)
	if err != nil || !stored.Enforce2FAForUsers || !slices.Equal(stored.AllowedCountries, []string{"DE", "AT"}) {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	if !slices.Contains(e.audit.Actions(), audit.ActionSettingsUpdate) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	if rec := e.filter("203.0.113.7", "/"); rec.Code != http.StatusForbidden {
		t.Fatalf("country filter must apply immediately, code=%d", rec.Code)
	}
}

func TestPutSettingsValidation(t *testing.T) {
	e := newEnv(t)
	cases := []any{
		`{"enforce_2fa_for_users":true,"admin_allowlist_only":false}`,
		`{"enforce_2fa_for_users":true,"admin_allowlist_only":false,"allowed_countries":null}`,
		`{"admin_allowlist_only":false,"allowed_countries":[]}`,
		settingsBody(false, false, []string{"de"}),
		settingsBody(false, false, []string{"DEU"}),
		settingsBody(false, false, []string{""}),
	}
	for i, body := range cases {
		rec := e.call(t, http.MethodPut, "/security/settings", body, callerIP)
		if rec.Code != http.StatusBadRequest || errCode(rec) != httpx.CodeValidation {
			t.Errorf("case %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	wantStatus(t, e.call(t, http.MethodPut, "/security/settings",
		`{"enforce_2fa_for_users":true,"admin_allowlist_only":false,"allowed_countries":[],"x":1}`, callerIP),
		http.StatusBadRequest, httpx.CodeBadRequest)
}

func TestPutSettingsRejectsSelfLockout(t *testing.T) {
	t.Run("admin allowlist without own rule", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, true, []string{}), callerIP),
			http.StatusConflict, httpx.CodeConflict)
		// production: loopback is not exempt from the admin allowlist
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, true, []string{}), "127.0.0.1"),
			http.StatusConflict, httpx.CodeConflict)
		dev := newEnv(t, func(d *Deps) { d.Dev = true })
		wantStatus(t, dev.call(t, http.MethodPut, "/security/settings", settingsBody(false, true, []string{}), "127.0.0.1"),
			http.StatusOK, "")
	})
	t.Run("admin allowlist with own rule", func(t *testing.T) {
		e := newEnv(t)
		e.addRule(t, "198.51.100.0/24", "allow", nil)
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, true, []string{}), callerIP),
			http.StatusOK, "")
	})
	t.Run("country filter excluding caller", func(t *testing.T) {
		e := newEnv(t)
		e.geo[callerIP] = "US"
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, false, []string{"DE"}), callerIP),
			http.StatusConflict, httpx.CodeConflict)
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, false, []string{"DE", "US"}), callerIP),
			http.StatusOK, "")
		if got, _ := store.LoadSecuritySettings(context.Background(), e.q); len(got.AllowedCountries) != 2 {
			t.Fatalf("stored = %+v", got)
		}
	})
	t.Run("unknown caller country is not locked out", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, false, []string{"DE"}), callerIP),
			http.StatusOK, "")
	})
}

func TestPutSettingsExpiringAllowRuleDoesNotSatisfyAdminAllowlist(t *testing.T) {
	e := newEnv(t)
	exp := t0.Add(time.Hour)
	e.addRule(t, "198.51.100.0/24", "allow", &exp)
	wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, true, []string{}), callerIP),
		http.StatusConflict, httpx.CodeConflict)
}

func TestPutSettingsCountriesRequireGeoIP(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.GeoAvailable = false })
	wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, false, []string{"DE"}), callerIP),
		http.StatusBadRequest, httpx.CodeValidation)
	wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(true, false, []string{}), callerIP),
		http.StatusOK, "")
}
