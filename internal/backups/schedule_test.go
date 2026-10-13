package backups_test

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // fixed zone data: the expected values below must not depend on the host

	"github.com/Bernotese/mc-panel/internal/backups"
)

func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func utc(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
}

func daily(minute int) backups.Schedule {
	s := backups.DefaultSchedule
	s.Mode, s.TimeOfDay = backups.ModeDaily, minute
	return s
}

func TestNextRunDaily(t *testing.T) {
	loc := berlin(t)
	cases := []struct {
		name  string
		after time.Time
		want  time.Time
	}{
		{"later today", utc(2026, 10, 12, 1, 59), utc(2026, 10, 12, 2, 0)}, // 03:59 CEST -> 04:00 CEST
		{"exactly at the slot", utc(2026, 10, 12, 2, 0), utc(2026, 10, 13, 2, 0)},
		{"passed today", utc(2026, 10, 12, 10, 0), utc(2026, 10, 13, 2, 0)},
		{"after the clock change", utc(2026, 10, 25, 0, 0), utc(2026, 10, 25, 3, 0)}, // 04:00 CET = 03:00Z
	}
	for _, c := range cases {
		got, ok := backups.NextRun(daily(240), loc, c.after, time.Time{})
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s: NextRun(%v) = %v %v, want %v", c.name, c.after, got, ok, c.want)
		}
	}
}

// 2026-03-29 02:00 CET jumps to 03:00 CEST: a 02:30 slot moves to 03:30 CEST (time.Date).
func TestNextRunDailyDSTGap(t *testing.T) {
	got, _ := backups.NextRun(daily(150), berlin(t), utc(2026, 3, 28, 23, 0), time.Time{})
	if want := utc(2026, 3, 29, 1, 30); !got.Equal(want) {
		t.Fatalf("gap: %v, want %v (03:30 CEST)", got, want)
	}
}

// 2026-10-25 03:00 CEST falls back to 02:00 CET: 02:30 exists twice and runs once, at the
// earlier instant; the next run is on the following day.
func TestNextRunDailyDSTRepeat(t *testing.T) {
	loc := berlin(t)
	got, _ := backups.NextRun(daily(150), loc, utc(2026, 10, 24, 22, 0), time.Time{})
	if want := utc(2026, 10, 25, 0, 30); !got.Equal(want) {
		t.Fatalf("first 02:30: %v, want %v (02:30 CEST)", got, want)
	}
	got, _ = backups.NextRun(daily(150), loc, utc(2026, 10, 25, 0, 35), time.Time{})
	if want := utc(2026, 10, 26, 1, 30); !got.Equal(want) {
		t.Fatalf("after the run: %v, want %v (next day, not the repeated 02:30 CET)", got, want)
	}
}

func TestNextRunWeekly(t *testing.T) {
	loc := berlin(t)
	s := backups.DefaultSchedule
	s.Mode, s.Weekday, s.TimeOfDay = backups.ModeWeekly, time.Sunday, 240
	cases := []struct{ after, want time.Time }{
		{utc(2026, 10, 12, 10, 0), utc(2026, 10, 18, 2, 0)}, // Monday -> Sunday 04:00 CEST
		{utc(2026, 10, 18, 1, 0), utc(2026, 10, 18, 2, 0)},  // Sunday 03:00 CEST -> same day
		{utc(2026, 10, 18, 2, 0), utc(2026, 10, 25, 3, 0)},  // next Sunday, now 04:00 CET
	}
	for _, c := range cases {
		if got, ok := backups.NextRun(s, loc, c.after, time.Time{}); !ok || !got.Equal(c.want) {
			t.Errorf("NextRun(%v) = %v %v, want %v", c.after, got, ok, c.want)
		}
	}
}

func TestNextRunInterval(t *testing.T) {
	s := backups.DefaultSchedule
	s.Mode, s.IntervalHours = backups.ModeInterval, 6
	now := utc(2026, 10, 12, 10, 0)
	cases := []struct {
		name      string
		lastStart time.Time
		want      time.Time
	}{
		{"no run yet", time.Time{}, now.Add(6 * time.Hour)},
		{"last run 2h ago", now.Add(-2 * time.Hour), now.Add(4 * time.Hour)},
		{"last run exactly 6h ago", now.Add(-6 * time.Hour), now.Add(6 * time.Hour)},
		{"missed", now.Add(-30 * time.Hour), now.Add(6 * time.Hour)},
	}
	for _, c := range cases {
		if got, ok := backups.NextRun(s, time.UTC, now, c.lastStart); !ok || !got.Equal(c.want) {
			t.Errorf("%s: %v %v, want %v", c.name, got, ok, c.want)
		}
	}
}

func TestNextRunOff(t *testing.T) {
	if _, ok := backups.NextRun(backups.DefaultSchedule, time.UTC, utc(2026, 10, 12, 10, 0), time.Time{}); ok {
		t.Fatal("mode off must have no next run")
	}
}

func TestScheduleValidate(t *testing.T) {
	ok := daily(240)
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := backups.DefaultSchedule.Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	off := backups.DefaultSchedule
	off.KeepLast, off.KeepDaily, off.KeepWeekly, off.KeepMonthly = 0, 0, 0, 0
	if err := off.Validate(); err != nil {
		t.Fatalf("off without retention is valid: %v", err)
	}
	bad := []struct {
		name string
		mut  func(*backups.Schedule)
		want string
	}{
		{"mode", func(s *backups.Schedule) { s.Mode = "hourly" }, "mode"},
		{"interval low", func(s *backups.Schedule) { s.IntervalHours = 0 }, "interval_hours"},
		{"interval high", func(s *backups.Schedule) { s.IntervalHours = 169 }, "interval_hours"},
		{"time low", func(s *backups.Schedule) { s.TimeOfDay = -1 }, "time_of_day"},
		{"time high", func(s *backups.Schedule) { s.TimeOfDay = 1440 }, "time_of_day"},
		{"weekday", func(s *backups.Schedule) { s.Weekday = 7 }, "weekday"},
		{"keep high", func(s *backups.Schedule) { s.KeepDaily = 1001 }, "keep_daily"},
		{"keep negative", func(s *backups.Schedule) { s.KeepLast = -1 }, "keep_last"},
		{"no retention", func(s *backups.Schedule) { s.KeepLast, s.KeepDaily, s.KeepWeekly, s.KeepMonthly = 0, 0, 0, 0 }, "keep_"},
	}
	for _, b := range bad {
		s := daily(240)
		b.mut(&s)
		if err := s.Validate(); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%s: err = %v, want mention of %q", b.name, err, b.want)
		}
	}
}
