//go:build !acceptance

package timescale

// Factor is always 1 outside acceptance builds.
func Factor() int64 { return 1 }

// Err is always nil outside acceptance builds.
func Err() error { return nil }
