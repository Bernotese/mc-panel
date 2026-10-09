//go:build unix

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// restrictUmask makes every file the process creates (SQLite database, WAL/SHM, secret key)
// private to the panel user. It runs first in serve and every CLI subcommand.
func restrictUmask() { syscall.Umask(0o077) }

// secureDataDir chmods the data directory to 0700 and the database files that exist to 0600,
// then verifies that none of them is accessible by group/others (refusing to start otherwise).
func secureDataDir(dir string) error {
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	if err := checkPrivate(dir); err != nil {
		return err
	}
	for _, name := range []string{"panel.db", "panel.db-wal", "panel.db-shm"} {
		p := filepath.Join(dir, name)
		if err := os.Chmod(p, 0o600); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("data dir: %w", err)
		}
		if err := checkPrivate(p); err != nil {
			return err
		}
	}
	return nil
}

// checkPrivate fails if path has any group/other permission bit set.
func checkPrivate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("data dir: %s must not be accessible by group/others (mode %v)", path, info.Mode().Perm())
	}
	return nil
}
