// Command files is mcpanel-files, the unprivileged file service of the file browser. It runs
// as the user that owns the Minecraft data (mcpanel-mc), serves a small JSON/stream API on a
// Unix socket (systemd socket activation in production) that only the panel user (and root)
// may connect to, and works on one instance data directory per request through an os.Root.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata" // time zones of the backup retention on hosts without zoneinfo (spec 4)

	"github.com/Bernotese/mc-panel/internal/agent/peercred"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files"
	"github.com/Bernotese/mc-panel/internal/unixsock"
)

// version is set at build time: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

const (
	defaultConfigPath = "/etc/mc-panel-files/files.toml"
	shutdownTimeout   = 10 * time.Second
	activationFD      = 3 // SD_LISTEN_FDS_START
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Geteuid)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "files:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, geteuid func() int) error {
	fs := flag.NewFlagSet("files", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to files.toml (default "+defaultConfigPath+" unless -dev)")
	dev := fs.Bool("dev", false, "development mode: socket ./.dev/files.sock, data in ./.dev/mc, panel uid = own uid, missing data directories are created, text logs")
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
	// Spec §1.2: file operations never run as root.
	if geteuid() == 0 {
		return errors.New("files must not run as root: start it as the user that owns the Minecraft data (mc_user, e.g. mcpanel-mc)")
	}

	cfg, err := loadConfig(*configPath, *dev)
	if err != nil {
		return err
	}
	log := newLogger(cfg.Dev, stderr)
	uid, err := panelUID(cfg)
	if err != nil {
		return err
	}
	l, activated, err := listen(cfg)
	if err != nil {
		return err
	}

	svc := files.New(files.Config{InstancesDir: cfg.InstancesDir, Limits: cfg.Limits, Dev: cfg.Dev, Clock: clock.Real{}, Log: log,
		ResticBinary: cfg.ResticBinary})
	// Spec 4 §4.3: a restore swap that an earlier run did not finish is finished or undone from
	// the restore journal before anything else touches the data directories (cheap: only the
	// journal entries are looked at).
	if n := svc.RecoverRestores(); n > 0 {
		log.Warn("files: recovered interrupted restores", "count", n)
	}
	// Spec 4 §3.2: one restic check at start; without restic the backup endpoints answer 503
	// and everything else works.
	svc.InitBackups(ctx)
	// The temp cleanup walks every data directory; it runs while the service already answers,
	// so a large instances_dir never delays the panel's calls (it leaves the temp entries of
	// this process alone).
	cleanupCtx, stopCleanup := context.WithCancel(context.Background())
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		if n := svc.CleanupTemp(cleanupCtx); n > 0 {
			log.Info("files: removed leftover temp entries", "count", n)
		}
	}()
	// stop ends the background work: running extract jobs are cancelled first (they remove
	// their temp folders), so a slow HTTP shutdown never keeps them writing.
	stop := func() {
		stopCleanup()
		svc.Close()
		<-cleanupDone
	}
	srv := files.NewServer(svc, version, log)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(peercred.Wrap(l, peercred.AllowUIDs(uid, 0), log)) }() // the panel and root, like the agent
	log.Info("files started", "version", version, "socket", cfg.Socket, "socket_activation", activated,
		"panel_uid", uid, "instances_dir", cfg.InstancesDir, "dev", cfg.Dev, "uid", os.Getuid(), "restic_binary", cfg.ResticBinary)

	select {
	case err := <-errc:
		stop()
		if err == nil {
			err = errors.New("server stopped unexpectedly")
		}
		return err
	case <-ctx.Done():
	}
	log.Info("files shutting down")
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return <-errc
}

// loadConfig reads files.toml; -dev without -config uses the dev defaults (V2).
func loadConfig(path string, dev bool) (config.Files, error) {
	if dev && path == "" {
		root, err := os.Getwd()
		if err != nil {
			return config.Files{}, err
		}
		return config.DevFiles(root), nil
	}
	if path == "" {
		path = defaultConfigPath
	}
	cfg, err := config.LoadFiles(path)
	if err != nil {
		return config.Files{}, err
	}
	if dev {
		cfg.Dev = true
	}
	return cfg, nil
}

// panelUID returns the peer UID the panel connects with; -1 means "unset" and is only
// allowed in dev mode (then the service's own UID).
func panelUID(cfg config.Files) (uint32, error) {
	if cfg.PanelUID < 0 {
		if !cfg.Dev {
			return 0, errors.New("panel_uid must be set")
		}
		return uint32(os.Getuid()), nil
	}
	if int64(cfg.PanelUID) > math.MaxUint32 {
		return 0, fmt.Errorf("panel_uid %d out of range", cfg.PanelUID)
	}
	return uint32(cfg.PanelUID), nil
}

// listen prefers the socket systemd passed (spec §3.1: mcpanel-files.socket owns path, mode
// and group, so the service needs no membership in the panel's group); without socket
// activation it creates cfg.Socket itself with socket_mode and its own primary group.
func listen(cfg config.Files) (net.Listener, bool, error) {
	l, err := activatedListener(os.Getenv, os.Unsetenv, os.Getpid())
	if err != nil || l != nil {
		return l, l != nil, err
	}
	if cfg.Dev {
		if err := os.MkdirAll(filepath.Dir(cfg.Socket), 0o750); err != nil {
			return nil, false, err
		}
	}
	l, err = unixsock.Listen(cfg.Socket, cfg.SocketFileMode(), -1)
	return l, false, err
}

// activatedListener returns the listener systemd passed as fd 3 when LISTEN_PID is this
// process and LISTEN_FDS is 1, otherwise nil. It always removes the activation variables, so
// they never leak into child processes.
func activatedListener(getenv func(string) string, unsetenv func(string) error, pid int) (net.Listener, error) {
	lpid, lfds := getenv("LISTEN_PID"), getenv("LISTEN_FDS")
	for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		_ = unsetenv(k)
	}
	if lpid == "" && lfds == "" {
		return nil, nil
	}
	if lpid != strconv.Itoa(pid) {
		return nil, nil // meant for another process
	}
	if n, err := strconv.Atoi(lfds); err != nil || n != 1 {
		return nil, fmt.Errorf("socket activation: expected exactly one socket, got LISTEN_FDS=%q", lfds)
	}
	syscall.CloseOnExec(activationFD)
	f := os.NewFile(activationFD, "files.sock")
	l, err := net.FileListener(f)
	_ = f.Close() // FileListener holds its own duplicate
	if err != nil {
		return nil, fmt.Errorf("socket activation: %w", err)
	}
	if _, ok := l.(*net.UnixListener); !ok {
		_ = l.Close()
		return nil, errors.New("socket activation: fd 3 is not a Unix socket")
	}
	return l, nil
}

func newLogger(dev bool, w io.Writer) *slog.Logger {
	if dev {
		return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(w, nil))
}
