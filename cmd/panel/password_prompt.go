package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// termState returns the terminal attributes of fd, or ok=false if fd is not a terminal.
func termState(fd int) (t *unix.Termios, ok bool) {
	t, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	return t, err == nil
}

// readPassword returns a reader yielding one password line. On a terminal the
// prompt goes to stderr and echo is disabled; otherwise stdin is read as-is.
func readPassword(stdin *os.File, stderr io.Writer) (io.Reader, error) {
	fd := int(stdin.Fd())
	t, isTerm := termState(fd)
	if !isTerm { // pipe or file
		return stdin, nil
	}
	_, _ = fmt.Fprint(stderr, "Password: ")
	noEcho := *t
	noEcho.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &noEcho); err != nil {
		return nil, err
	}
	line, readErr := bufio.NewReader(stdin).ReadString('\n')
	_ = unix.IoctlSetTermios(fd, ioctlSetTermios, t)
	_, _ = fmt.Fprintln(stderr)
	if readErr != nil && readErr != io.EOF {
		return nil, readErr
	}
	return strings.NewReader(strings.TrimRight(line, "\r\n") + "\n"), nil
}
