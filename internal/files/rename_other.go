//go:build !linux && !darwin

package files

import (
	"errors"
	"os"
)

// renameat: no atomic no-replace rename here; renameNoReplace uses its fallback.
func renameat(*os.File, string, *os.File, string) error { return errors.ErrUnsupported }
