//go:build !linux && !darwin

package files

import "errors"

func availableBytes(string) (uint64, error) {
	return 0, errors.ErrUnsupported
}
