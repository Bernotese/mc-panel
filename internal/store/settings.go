package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const securitySettingsKey = "security"

// SecuritySettings is stored as JSON under settings.key = "security".
// Owned by stream B (API), read by stream A (2FA enforcement).
type SecuritySettings struct {
	Enforce2FAForUsers bool     `json:"enforce_2fa_for_users"`
	AdminAllowlistOnly bool     `json:"admin_allowlist_only"`
	AllowedCountries   []string `json:"allowed_countries"` // ISO 3166-1 alpha-2; empty = no country filter
}

func DefaultSecuritySettings() SecuritySettings {
	return SecuritySettings{AllowedCountries: []string{}}
}

func LoadSecuritySettings(ctx context.Context, q *Queries) (SecuritySettings, error) {
	v, err := q.GetSetting(ctx, securitySettingsKey)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultSecuritySettings(), nil
	}
	if err != nil {
		return SecuritySettings{}, err
	}
	s := DefaultSecuritySettings()
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		return SecuritySettings{}, err
	}
	if s.AllowedCountries == nil {
		s.AllowedCountries = []string{}
	}
	return s, nil
}

func SaveSecuritySettings(ctx context.Context, q *Queries, s SecuritySettings, now time.Time) error {
	if s.AllowedCountries == nil {
		s.AllowedCountries = []string{}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return q.UpsertSetting(ctx, UpsertSettingParams{Key: securitySettingsKey, Value: string(b), UpdatedAt: now.UnixMilli()})
}
