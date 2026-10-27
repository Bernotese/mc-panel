package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/waker"
)

// Fixed texts of the waker (dev instances spec §6.2); nothing in them comes from outside.
const (
	wakerMOTD = "Dev schläft – verbinden zum Aufwecken"
	wakerKick = "Dev startet (oder wird gerade synchronisiert). Bitte in ca. 1 Minute neu verbinden."
)

// getuid is os.Getuid; tests replace it.
var getuid = os.Getuid

// runWaker is "agent waker": the listener of the mcpanel-waker container, which runs this
// binary read-only as an unprivileged user on mc-router's network. It reads no config file
// and touches nothing on disk; its only output is the wake lines on stdout, which the agent
// reads from the container log.
func runWaker(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("agent waker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", ":25565", "TCP address to listen on")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	// It handles untrusted input from the internet: never with root's rights.
	if getuid() == 0 {
		return errors.New("waker: refusing to run as root")
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", *listen)
	if err != nil {
		return fmt.Errorf("waker: %w", err)
	}
	return waker.Serve(ctx, ln, waker.Config{
		Listen:   *listen,
		MOTD:     wakerMOTD,
		Kick:     wakerKick,
		MaxConns: 64,
		// Every peer is mc-router (no PROXY protocol), so a lower per-address cap would only be
		// a global cap that a few slow clients could hold. mc-router's -connection-rate-limit
		// and the 5-s Deadline bound abuse instead.
		MaxPerIP:  64,
		Deadline:  5 * time.Second,
		WakeEvery: 10 * time.Second,
		Out:       stdout,
		Clock:     clock.Real{},
	})
}
