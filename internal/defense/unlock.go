package defense

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/store"
)

// UnlockResult summarizes what Unlock changed.
type UnlockResult struct {
	JailsLifted            int64
	AdminAllowlistDisabled bool
	CountryFilterCleared   bool
}

// Unlock is the emergency escape hatch behind `panel admin unlock`: it lifts all
// active jails, disables "admin login only from allowlist" and clears the
// country filter. It works directly on the database (the panel may be stopped).
func Unlock(ctx context.Context, db *sql.DB, rec audit.Recorder, c clock.Clock, out io.Writer) (UnlockResult, error) {
	q := store.New(db)
	now := c.Now()
	var res UnlockResult
	n, err := q.DefenseLiftAllActiveJails(ctx, store.DefenseLiftAllActiveJailsParams{
		LiftedAt: msNull(now), LiftedBy: sql.NullString{String: "cli", Valid: true}, Now: now.UnixMilli(),
	})
	if err != nil {
		return res, fmt.Errorf("unlock: lift jails: %w", err)
	}
	res.JailsLifted = n

	st, err := store.LoadSecuritySettings(ctx, q)
	if err != nil {
		return res, fmt.Errorf("unlock: load settings: %w", err)
	}
	before := st
	before.AllowedCountries = slices.Clone(st.AllowedCountries)
	res.AdminAllowlistDisabled = st.AdminAllowlistOnly
	res.CountryFilterCleared = len(st.AllowedCountries) > 0
	changed := res.AdminAllowlistDisabled || res.CountryFilterCleared
	if changed {
		st.AdminAllowlistOnly = false
		st.AllowedCountries = []string{}
		if err := store.SaveSecuritySettings(ctx, q, st, now); err != nil {
			return res, fmt.Errorf("unlock: save settings: %w", err)
		}
	}

	entry := func(action, target string, details map[string]any) audit.Entry {
		return audit.Entry{
			Action: action, Target: target, Result: audit.ResultSuccess,
			ActorType: audit.ActorCLI, ActorName: "cli", Details: details,
		}
	}
	if n > 0 {
		if err := rec.Record(ctx, entry(audit.ActionJailLift, "all", map[string]any{"count": n})); err != nil {
			return res, fmt.Errorf("unlock: audit: %w", err)
		}
	}
	if changed {
		details := map[string]any{"before": settingsDetails(before), "after": settingsDetails(st)}
		if err := rec.Record(ctx, entry(audit.ActionSettingsUpdate, "security", details)); err != nil {
			return res, fmt.Errorf("unlock: audit: %w", err)
		}
	}

	_, _ = fmt.Fprintf(out, "Lifted %d active jail(s).\n", n)
	if res.AdminAllowlistDisabled {
		_, _ = fmt.Fprintln(out, `Disabled "admin login only from allowlist".`)
	} else {
		_, _ = fmt.Fprintln(out, `"Admin login only from allowlist" was already disabled.`)
	}
	if res.CountryFilterCleared {
		_, _ = fmt.Fprintf(out, "Cleared country filter (was: %s).\n", strings.Join(before.AllowedCountries, ", "))
	} else {
		_, _ = fmt.Fprintln(out, "Country filter was already empty.")
	}
	_, _ = fmt.Fprintln(out, "Deny rules were not changed. A running panel applies these changes within 30 seconds; firewall bans are removed by the next sync.")
	return res, nil
}
