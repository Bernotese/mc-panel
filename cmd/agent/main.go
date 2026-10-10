// Command agent is the privileged mc-panel helper. It serves a small, strictly
// validated JSON API on a Unix socket that only the panel user (and root) may
// connect to, manages the nftables jail sets and is the only Docker client
// (Minecraft containers and mc-router).
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

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/firewall"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/peercred"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
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
	dev := fs.Bool("dev", false, "development mode: socket ./.dev/agent.sock (0600), noop firewall, Docker off unless MCPANEL_DEV_DOCKER=1, data in ./.dev/mc, text logs")
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
	mode, gid, err := socketPerms(cfg, user.LookupId)
	if err != nil {
		return err
	}
	mcUID, mcGID, err := mcUserIDs(cfg, gid, user.Lookup)
	if err != nil {
		return err
	}
	if cfg.Dev {
		if err := os.MkdirAll(filepath.Dir(cfg.Socket), 0o750); err != nil {
			return err
		}
	}
	// Listen first: it detects an already running agent, so a refused second
	// start never touches the firewall.
	l, err := agent.Listen(cfg.Socket, mode, gid)
	if err != nil {
		return err
	}
	fw, err := firewall.New(cfg, clock.Real{}, log)
	if err != nil {
		_ = l.Close() // removes the socket file again
		return fmt.Errorf("firewall: %w", err)
	}

	// Without docker_socket (dev default) there is no instance manager: the server
	// answers instance writes with 503 docker_unavailable and creates no containers.
	var opts []agent.Option
	stopInstances := func() {}
	if cfg.DockerSocket != "" {
		mgr := instances.New(instances.Config{
			Docker:       docker.New(cfg.DockerSocket),
			InstancesDir: cfg.InstancesDir,
			UID:          mcUID,
			GID:          mcGID,
			Images:       cfg.Images,
			Clock:        clock.Real{},
			Log:          log,
		})
		runCtx, stopRun := context.WithCancel(ctx)
		runDone := make(chan struct{})
		go func() {
			defer close(runDone)
			mgr.Run(runCtx)
		}()
		// stopInstances ends the background loop and the queued operations.
		stopInstances = func() {
			stopRun()
			<-runDone
			mgr.Close()
		}
		opts = append(opts, agent.WithInstances(mgr))
	}

	srv := agent.NewServer(fw, version, log, opts...)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(peercred.Wrap(l, peercred.AllowUIDs(panelUID, 0), log)) }()
	log.Info("agent started", "version", version, "socket", cfg.Socket, "firewall", fw.Name(),
		"panel_uid", panelUID, "socket_mode", fmt.Sprintf("%#o", mode), "dev", cfg.Dev,
		"docker_socket", cfg.DockerSocket, "instances_dir", cfg.InstancesDir, "mc_uid", mcUID, "mc_gid", mcGID)

	select {
	case err := <-errc:
		stopInstances()
		if err == nil {
			err = errors.New("server stopped unexpectedly")
		}
		return err
	case <-ctx.Done():
	}
	log.Info("agent shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	stopInstances()
	if err != nil {
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

// mcUserIDs resolves mc_user to the uid:gid the Minecraft containers run as. In dev
// mode an empty mc_user means the agent's own ids. Outside dev mode root (uid or
// gid 0) is refused: the containers must never run as root on the host.
func mcUserIDs(cfg config.Agent, panelGID int, lookup func(name string) (*user.User, error)) (int, int, error) {
	if cfg.MCUser == "" {
		if !cfg.Dev {
			return 0, 0, errors.New("mc_user must be set")
		}
		return os.Getuid(), os.Getgid(), nil
	}
	u, err := lookup(cfg.MCUser)
	if err != nil {
		return 0, 0, fmt.Errorf("look up mc_user %q (create it with: useradd --system --no-create-home --shell /usr/sbin/nologin %s): %w",
			cfg.MCUser, cfg.MCUser, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil || uid < 0 {
		return 0, 0, fmt.Errorf("mc_user %q has invalid uid %q", cfg.MCUser, u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil || gid < 0 {
		return 0, 0, fmt.Errorf("mc_user %q has invalid gid %q", cfg.MCUser, u.Gid)
	}
	if !cfg.Dev && (uid == 0 || gid == 0) {
		return 0, 0, fmt.Errorf("mc_user %q resolves to uid %d gid %d: Minecraft containers must not run as root", cfg.MCUser, uid, gid)
	}
	return uid, gid, nil
}

// checkImages is a stub (red phase).
func checkImages(config.Images) error { return nil }

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
