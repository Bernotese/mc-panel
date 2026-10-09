//go:build !linux && !darwin

package main

import (
	"io"
	"os"
)

// readPassword has no terminal handling on this platform: stdin is read as-is.
func readPassword(stdin *os.File, _ io.Writer) (io.Reader, error) { return stdin, nil }
