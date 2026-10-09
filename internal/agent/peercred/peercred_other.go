//go:build !linux && !darwin

package peercred

import (
	"errors"
	"runtime"
)

func peerUID(int) (uint32, error) {
	return 0, errors.New("peer credentials are not supported on " + runtime.GOOS)
}
