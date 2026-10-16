//go:build acceptance

package timescale

import (
	"fmt"
	"os"
	"strconv"
)

// Env names the factor (an integer 1..maxFactor; unset or empty = 1).
const Env = "MCPANEL_ACCEPTANCE_TIMESCALE"

const maxFactor = 60

// Read once at package initialisation, before the importing packages compute their durations.
var factor, parseErr = parse(os.Getenv(Env))

func parse(s string) (int64, error) {
	if s == "" {
		return 1, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 || n > maxFactor {
		return 1, fmt.Errorf("%s=%q: want an integer from 1 to %d", Env, s, maxFactor)
	}
	return n, nil
}

// Factor is the time scale of this acceptance build.
func Factor() int64 { return factor }

// Err reports an invalid factor; the panel refuses to start with it.
func Err() error { return parseErr }
