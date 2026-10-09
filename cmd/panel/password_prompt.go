//go:build linux || darwin

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// termState returns the terminal attributes of fd, or ok=false if fd is not a terminal.
func termState(fd int) (t *unix.Termios, ok bool) {
	t, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	return t, err == nil
}

// readPassword returns a reader yielding one password line. On a terminal the password is
// requested twice (prompts go to stderr) with echo disabled, and must match; otherwise stdin
// is read as-is. Echo is restored on return and when SIGINT/SIGTERM/SIGHUP arrives.
func readPassword(stdin *os.File, stderr io.Writer) (io.Reader, error) {
	fd := int(stdin.Fd())
	t, isTerm := termState(fd)
	if !isTerm { // pipe or file
		return stdin, nil
	}
	noEcho := *t
	noEcho.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &noEcho); err != nil {
		return nil, err
	}
	restore := func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, t) }

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
			restore()
			_, _ = fmt.Fprintln(stderr)
			os.Exit(130)
		case <-done:
		}
	}()
	defer func() {
		signal.Stop(sigCh)
		close(done)
		restore()
	}()

	br := bufio.NewReader(stdin)
	pw, err := askTwice(func(prompt string) (string, error) {
		_, _ = fmt.Fprint(stderr, prompt)
		line, readErr := br.ReadString('\n')
		_, _ = fmt.Fprintln(stderr)
		if readErr != nil && readErr != io.EOF {
			return "", readErr
		}
		return strings.TrimRight(line, "\r\n"), nil
	})
	if err != nil {
		return nil, err
	}
	return strings.NewReader(pw + "\n"), nil
}
