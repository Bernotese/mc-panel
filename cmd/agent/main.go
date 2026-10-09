// Command agent is the privileged mc-panel helper. It serves a small, strictly
// validated JSON API on a Unix socket that only the panel user (and root) may
// connect to, and manages the nftables jail sets.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"mcpanel/internal/agent"
	"mcpanel/internal/agent/firewall"
	"mcpanel/internal/agent/peercred"
	"mcpanel/internal/clock"
	"mcpanel/internal/config"
)

// version is set at build time: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

const (
	defaultConfigPath = "/etc/mc-panel/agent.toml"
	shutdownTimeout   = 5 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to agent.toml (default "+defaultConfigPath+" unless -dev)")
	dev := fs.Bool("dev", false, "development mode: socket ./.dev/agent.sock (0600), noop firewall, text logs")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if *showVersion {
		_, err := fmt.Fprintln(stdout, version)
		return err
	}

	path := *configPath
	if path == "" && !*dev {
		path = defaultConfigPath
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := config.LoadAgent(path, *dev, root)
	if err != nil {
		return err
	}
	log := newLogger(cfg.Dev, stderr)

	panelUID, err := uidFromConfig(cfg.PanelUID)
	if err != nil {
		return err
	}
	fw, err := firewall.New(cfg, clock.Real{}, log)
	if err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	mode, gid, err := socketPerms(cfg, user.LookupId)
	if err != nil {
		return err
	}
	if cfg.Dev {
		if err := os.MkdirAll(filepath.Dir(cfg.Socket), 0o750); err != nil {
			return err
		}
	}
	l, err := agent.Listen(cfg.Socket, mode, gid)
	if err != nil {
		return err
	}

	srv := agent.NewServer(fw, version, log)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(peercred.Wrap(l, peercred.AllowUIDs(panelUID, 0), log)) }()
	log.Info("agent started", "version", version, "socket", cfg.Socket, "firewall", fw.Name(),
		"panel_uid", panelUID, "socket_mode", fmt.Sprintf("%#o", mode), "dev", cfg.Dev)

	select {
	case err := <-errc:
		if err == nil {
			err = errors.New("server stopped unexpectedly")
		}
		return err
	case <-ctx.Done():
	}
	log.Info("agent shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return <-errc
}

// socketPerms returns the socket mode and group: dev 0600 without chown; prod
// 0660 with the primary group of the panel user, so only that group (and
// root) can even open the socket.
func socketPerms(cfg config.Agent, lookup func(uid string) (*user.User, error)) (os.FileMode, int, error) {
	if cfg.Dev {
		return 0o600, -1, nil
	}
	u, err := lookup(strconv.Itoa(cfg.PanelUID))
	if err != nil {
		return 0, 0, fmt.Errorf("look up panel_uid %d: %w", cfg.PanelUID, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("panel_uid %d has non-numeric gid %q: %w", cfg.PanelUID, u.Gid, err)
	}
	return 0o660, gid, nil
}

func uidFromConfig(uid int) (uint32, error) {
	if uid < 0 || int64(uid) > math.MaxUint32 {
		return 0, fmt.Errorf("panel_uid %d out of range", uid)
	}
	return uint32(uid), nil //nolint:gosec // G115: range checked above
}

func newLogger(dev bool, w io.Writer) *slog.Logger {
	if dev {
		return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(w, nil))
}
