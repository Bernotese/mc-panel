// Command panel is the mc-panel web application and its admin CLI.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
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
	_ "time/tzdata" // backup schedules and their time zone on hosts without zoneinfo

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
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

// shutdownTimeout bounds the graceful HTTP shutdown. It outlasts a running console command
// (agent.RconTimeout, 15 s), so a command sent just before the shutdown still gets its answer
// and audit entry.
const shutdownTimeout = 20 * time.Second

const usageText = `usage:
  panel serve        [-config path] [-dev]
  panel admin create [-config path] [-dev] -username NAME   (password from stdin)
  panel admin unlock [-config path] [-dev]
  panel admin list   [-config path] [-dev]
  panel audit verify [-config path] [-dev] [-journal file|-]   (journalctl -u mcpanel-panel -o cat)
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
	case "admin create", "admin unlock", "admin list", "audit verify":
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
	if cmd == "admin list" {
		// Read-only (installer spec §4.10): no data directory, no migration, no new files.
		if len(rest) > 0 {
			return fmt.Errorf("unexpected arguments: %v", rest)
		}
		return adminListCommand(ctx, cfg, stdout)
	}
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
		journal, closeJournal, err := journalArg(rest, stdin)
		if err != nil {
			return err
		}
		defer closeJournal()
		return audit.RunAuditVerify(ctx, db, stdout, journal)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

// journalArg parses the arguments of `audit verify`: an optional -journal with a file name or
// "-" for stdin. It returns nil without -journal.
func journalArg(args []string, stdin io.Reader) (io.Reader, func(), error) {
	fs := flag.NewFlagSet("audit verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("journal", "", "")
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	if fs.NArg() > 0 {
		return nil, nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	switch *path {
	case "":
		return nil, func() {}, nil
	case "-":
		return stdin, func() {}, nil
	}
	f, err := os.Open(*path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
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
	fc := filesapi.NewClient(cfg.FilesSocket)
	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clock.Real{}, Agent: ac, Instances: ac, Console: ac, Files: fc, Backups: fc, Version: version})
	if err != nil {
		return err
	}
	defer a.Close()

	l, err := listen(ctx, cfg) // closed by srv.Serve / srv.Shutdown (removes a socket file the panel created itself; one passed by systemd stays)
	if err != nil {
		return err
	}

	// Background loops get their own context so they outlive the signal until HTTP is drained.
	bgCtx, cancelBg := context.WithCancel(context.Background())
	bgDone := make(chan struct{})
	go func() { a.Run(bgCtx); close(bgDone) }()

	srv := newHTTPServer(a.Handler)
	// Console streams never become idle: without this, srv.Shutdown would wait for them until
	// its timeout and then close the connections hard.
	srv.RegisterOnShutdown(a.Shutdown)
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
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
// proxy) or host:port. For a unix path, a socket handed over by systemd (mcpanel-panel.socket,
// installer spec §4.6) wins: systemd owns path, owner, caddy group and mode, so the panel needs
// no membership in the proxy's group. Without activation the panel creates the socket itself
// (a stale socket is replaced only if nobody accepts on it, mode 0660).
func listen(ctx context.Context, cfg config.Panel) (net.Listener, error) {
	return listenWith(ctx, cfg, func() (net.Listener, error) {
		return unixsock.Activated(os.Getenv, os.Unsetenv, os.Getpid())
	})
}

func listenWith(ctx context.Context, cfg config.Panel, activated func() (net.Listener, error)) (net.Listener, error) {
	path, ok := cfg.ListenUnix()
	if !ok {
		return (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	}
	l, err := activated()
	if err != nil {
		return nil, err
	}
	if l != nil {
		if got := l.Addr().String(); got != path {
			_ = l.Close()
			return nil, fmt.Errorf("socket activation: socket %q does not match listen %q", got, cfg.Listen)
		}
		slog.Info("using the socket passed by systemd", "socket", path)
		return l, nil
	}
	return unixsock.Listen(path, 0o660, -1)
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
