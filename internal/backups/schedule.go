package backups

import (
	"errors"
	"fmt"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// Mode is how a server's backups are scheduled (spec §5.1).
type Mode string

const (
	ModeOff      Mode = "off"
	ModeInterval Mode = "interval"
	ModeDaily    Mode = "daily"
	ModeWeekly   Mode = "weekly"
)

const (
	maxIntervalHours = 168
	maxKeep          = 1000
	minutesPerDay    = 24 * 60
)

// Schedule is the editable part of a backup_schedules row.
type Schedule struct {
	Mode          Mode
	IntervalHours int
	TimeOfDay     int          // minutes after midnight in the panel time zone
	Weekday       time.Weekday // weekly only; 0 = Sunday
	KeepLast      int
	KeepDaily     int
	KeepWeekly    int
	KeepMonthly   int
}

// DefaultSchedule are the column defaults of migration 00006.
var DefaultSchedule = Schedule{
	Mode: ModeOff, IntervalHours: 24, TimeOfDay: 240, Weekday: time.Sunday,
	KeepLast: 24, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6,
}

var errInvalidSchedule = errors.New("invalid schedule")

// Validate checks the ranges of spec §5.1; every value is checked in every mode, so a later
// switch of the mode never meets a stored value out of range.
func (s Schedule) Validate() error {
	switch s.Mode {
	case ModeOff, ModeInterval, ModeDaily, ModeWeekly:
	default:
		return fmt.Errorf("%w: mode must be off, interval, daily or weekly", errInvalidSchedule)
	}
	if s.IntervalHours < 1 || s.IntervalHours > maxIntervalHours {
		return fmt.Errorf("%w: interval_hours must be 1..%d", errInvalidSchedule, maxIntervalHours)
	}
	if s.TimeOfDay < 0 || s.TimeOfDay >= minutesPerDay {
		return fmt.Errorf("%w: time_of_day must be 0..%d", errInvalidSchedule, minutesPerDay-1)
	}
	if s.Weekday < time.Sunday || s.Weekday > time.Saturday {
		return fmt.Errorf("%w: weekday must be 0..6", errInvalidSchedule)
	}
	keeps := []struct {
		name string
		v    int
	}{{"keep_last", s.KeepLast}, {"keep_daily", s.KeepDaily}, {"keep_weekly", s.KeepWeekly}, {"keep_monthly", s.KeepMonthly}}
	total := 0
	for _, k := range keeps {
		if k.v < 0 || k.v > maxKeep {
			return fmt.Errorf("%w: %s must be 0..%d", errInvalidSchedule, k.name, maxKeep)
		}
		total += k.v
	}
	if s.Mode != ModeOff && total == 0 {
		return fmt.Errorf("%w: at least one keep_* value must be > 0", errInvalidSchedule)
	}
	return nil
}

// Policy is the retention of the schedule as the files service expects it.
func (s Schedule) Policy() filesapi.ForgetPolicy {
	return filesapi.ForgetPolicy{KeepLast: s.KeepLast, KeepDaily: s.KeepDaily, KeepWeekly: s.KeepWeekly, KeepMonthly: s.KeepMonthly}
}

// NextRun returns the first scheduled time strictly after `after` (spec §5.2); ok is false for
// ModeOff. lastStart is the start of the last scheduled run (zero = none); only ModeInterval
// uses it. Daily and weekly slots are wall-clock times in loc: a slot in a DST gap moves
// forward like time.Date (02:30 -> 03:30), a repeated hour counts once (the earlier instant).
func NextRun(s Schedule, loc *time.Location, after, lastStart time.Time) (time.Time, bool) {
	switch s.Mode {
	case ModeInterval:
		every := time.Duration(s.IntervalHours) * time.Hour
		if !lastStart.IsZero() {
			if t := lastStart.Add(every); t.After(after) {
				return t, true
			}
		}
		return after.Add(every), true
	case ModeDaily, ModeWeekly:
		y, m, d := after.In(loc).Date()
		// Eight days always contain the next weekly slot (today's may have passed).
		for i := 0; i <= 7; i++ {
			// Calendar arithmetic at noon UTC: no zone transition can shift the date.
			day := time.Date(y, m, d+i, 12, 0, 0, 0, time.UTC)
			if s.Mode == ModeWeekly && day.Weekday() != s.Weekday {
				continue
			}
			dy, dm, dd := day.Date()
			if t := slot(dy, dm, dd, s.TimeOfDay, loc); t.After(after) {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// slot is the earliest instant whose wall clock in loc shows the date at minute.
func slot(y int, m time.Month, d, minute int, loc *time.Location) time.Time {
	t := time.Date(y, m, d, minute/60, minute%60, 0, 0, loc)
	// time.Date picks the later instant of a repeated hour; step back to the earlier one.
	for _, back := range []time.Duration{time.Hour, 30 * time.Minute} {
		if e := t.Add(-back); sameWall(e.In(loc), t.In(loc)) {
			return e
		}
	}
	return t
}

func sameWall(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd && a.Hour() == b.Hour() && a.Minute() == b.Minute()
}
