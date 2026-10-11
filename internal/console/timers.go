package console

import "time"

// timers creates the stream's timers; tests replace them (export_test.go).
type timers interface {
	// Every returns a channel that ticks every d and a stop function.
	Every(d time.Duration) (<-chan time.Time, func())
	// After returns a channel that fires once after d and a stop function.
	After(d time.Duration) (<-chan time.Time, func())
}

type realTimers struct{}

func (realTimers) Every(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

func (realTimers) After(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}
