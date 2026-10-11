// Command panel is the mc-panel web application and its admin CLI.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/unixsock"
	"github.com/Bernotese/mc-panel/internal/users"
)

// version is set at link time via -ldflags "-X main.version=...".
var version = "dev"

// defaultConfigPath is read when neither -config nor -dev is given (a var for tests).
var defaultConfigPath = "/etc/mc-panel/panel.toml"

const usageText = `usage:
  panel serve        [-config path] [-dev]
  panel admin create [-config path] [-dev] -username NAME   (password from stdin)
  panel admin unlock [-config path] [-dev]
  panel audit verify [-config path] [-dev]
  panel version

  -config defaults to /etc/mc-panel/panel.toml unless -dev is given.`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	cmd := ""
	switch {
	case len(args) >= 1 && (args[0] == "serve" || args[0] == "version"):
		cmd, args = args[0], args[1:]
	case len(args) >= 2 && (args[0] == "admin" || args[0] == "audit"):
		cmd, args = args[0]+" "+args[1], args[2:]
	}
	if cmd != "" && cmd != "version" {
		restrictUmask() // before any file (database, WAL/SHM, secret key) is created
	}
	var err error
	switch cmd {
	case "version":
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	case "serve":
		err = serve(args)
	case "admin create", "admin unlock", "audit verify":
		err = runCLI(cmd, args, stdin, stdout, stderr)
	default:
		_, _ = fmt.Fprintln(stderr, usageText)
		return 2
	}
	if err != nil {
		if !errors.Is(err, audit.ErrChainBroken) { // verify already printed its report
			_, _ = fmt.Fprintln(stderr, "error:", err)
		}
		return 1
	}
	return 0
}

// commonFlags extracts -config/-dev and returns the remaining args for the subcommand
// (e.g. -username), which parse their own flags. Only arguments that start with a dash are
// treated as flags; a positional "config" or "dev" (e.g. a username) is left alone.
func commonFlags(args []string) (config.Panel, []string, error) {
	var cfgPath string
	cfgSet := false
	dev := false
	var rest []string
	setConfig := func(v string) error {
		switch {
		case cfgSet:
			return errors.New("-config given more than once")
		case v == "":
			return errors.New("-config needs a non-empty value")
		case strings.HasPrefix(v, "-"):
			return fmt.Errorf("-config value %q looks like a flag", v)
		}
		cfgPath, cfgSet = v, true
		return nil
	}
	configPending := false // "-config" seen, its value is the next argument
	for _, arg := range args {
		if configPending {
			configPending = false
			if err := setConfig(arg); err != nil {
				return config.Panel{}, nil, err
			}
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			rest = append(rest, arg)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		switch name {
		case "config":
			if !hasVal {
				configPending = true
				continue
			}
			if err := setConfig(val); err != nil {
				return config.Panel{}, nil, err
			}
		case "dev":
			switch {
			case !hasVal || val == "true":
				dev = true
			case val == "false":
				dev = false
			default:
				return config.Panel{}, nil, fmt.Errorf("invalid value %q for -dev", val)
			}
		default:
			rest = append(rest, arg)
		}
	}
	if configPending {
		return config.Panel{}, nil, errors.New("-config needs a value")
	}
	if cfgPath == "" && !dev {
		cfgPath = defaultConfigPath
	}
	root, err := os.Getwd()
	if err != nil {
		return config.Panel{}, nil, err
	}
	cfg, err := config.LoadPanel(cfgPath, dev, root)
	return cfg, rest, err
}

// openDB creates/tightens the data directory, opens and migrates the database. Directory and
// database files are forced to 0700/0600 before and after opening (WAL/SHM appear on open);
// a remaining group/other bit refuses the start.
func openDB(ctx context.Context, cfg config.Panel) (*sql.DB, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	if err := secureDataDir(cfg.DataDir); err != nil {
		return nil, err
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := secureDataDir(cfg.DataDir); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func runCLI(cmd string, args []string, stdin *os.File, stdout, stderr io.Writer) error {
	cfg, rest, err := commonFlags(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	clk := clock.Real{}
	rec := audit.NewSQLRecorder(db, clk)
	switch cmd {
	case "admin create":
		if _, err := users.ParseAdminCreateArgs(rest); err != nil {
			return err
		}
		pw, err := readPassword(stdin, stderr)
		if err != nil {
			return err
		}
		return users.RunAdminCreate(ctx, db, clk, rec, rest, pw, stdout)
	case "admin unlock":
		if len(rest) > 0 {
			return fmt.Errorf("unexpected arguments: %v", rest)
		}
		_, err := defense.Unlock(ctx, db, rec, clk, stdout)
		return err
	case "audit verify":
		if len(rest) > 0 {
			return fmt.Errorf("unexpected arguments: %v", rest)
		}
		return audit.RunAuditVerify(ctx, db, stdout)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func serve(args []string) error {
	cfg, rest, err := commonFlags(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unexpected arguments: %v", rest)
	}
	setupLogging(cfg.Dev)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }() // runs last: after HTTP server and background loops stopped

	password.Warmup()

	ac := agent.NewClient(cfg.AgentSocket) // firewall and instance API share one client
	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clock.Real{}, Agent: ac, Instances: ac, Console: ac, Version: version})
	if err != nil {
		return err
	}
	defer a.Close()

	l, err := listen(ctx, cfg) // closed by srv.Serve / srv.Shutdown (a unix socket file is removed)
	if err != nil {
		return err
	}

	// Background loops get their own context so they outlive the signal until HTTP is drained.
	bgCtx, cancelBg := context.WithCancel(context.Background())
	bgDone := make(chan struct{})
	go func() { a.Run(bgCtx); close(bgDone) }()

	srv := newHTTPServer(a.Handler)
	errCh := make(chan error, 1)
	go func() {
		slog.Info("panel listening", "addr", cfg.Listen, "dev", cfg.Dev, "version", version)
		errCh <- srv.Serve(l)
	}()
	var serveErr error
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	case <-ctx.Done():
	}
	stop() // a second SIGINT/SIGTERM during shutdown gets the default (terminating) handling
	// 1. stop HTTP, 2. cancel background loops, 3. wait for them, 4. (deferred) close DB.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown failed, closing connections", "err", err)
		_ = srv.Close()
		if serveErr == nil {
			serveErr = err
		}
	}
	cancelBg()
	<-bgDone
	return serveErr
}

// listen opens the configured listener: "unix:/path" (production default, behind the reverse
// proxy; a stale socket is replaced only if nobody accepts on it, mode 0660 so the proxy's
// group can connect) or host:port.
func listen(ctx context.Context, cfg config.Panel) (net.Listener, error) {
	if path, ok := cfg.ListenUnix(); ok {
		return unixsock.Listen(path, 0o660, -1)
	}
	return (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
}

// newHTTPServer returns the panel's HTTP server. UnixConnContext marks connections from the
// unix socket so RealIP treats them as coming from the trusted reverse proxy.
func newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ConnContext:       httpx.UnixConnContext,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

func setupLogging(dev bool) {
	var h slog.Handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	if dev {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(h))
}
