// Package timescale shortens the panel's background waits (monitoring passes, alert windows,
// the backup scheduler tick) in acceptance builds only. Built with the tag acceptance, the
// factor comes from the environment variable named in timescale_acceptance.go; every other
// build (Makefile, goreleaser) has the constant factor 1 and never reads the environment.
// The logic stays the same code path: only the durations shrink, so CI can check the
// integration in minutes while the unit tests cover the timing with clock.Fake.
package timescale

import "time"

// D returns d divided by the time scale factor (d itself in a production build).
func D(d time.Duration) time.Duration {
	return d / time.Duration(Factor())
}
