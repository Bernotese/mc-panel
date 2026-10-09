// Command panel is the mc-panel web application and its admin CLI.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mcpanel/internal/agent"
	"mcpanel/internal/audit"
	"mcpanel/internal/clock"
	"mcpanel/internal/config"
	"mcpanel/internal/defense"
	"mcpanel/internal/password"
	"mcpanel/internal/store"
	"mcpanel/internal/users"
)

// version is set at link time via -ldflags "-X main.version=...".
var version = "dev"

const usageText = `usage:
  panel serve        [-config path] [-dev]
  panel admin create [-config path] [-dev] -username NAME   (password from stdin)
  panel admin unlock [-config path] [-dev]
  panel audit verify [-config path] [-dev]
  panel version`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	cmd := ""
	switch {
	case len(args) >= 1 && (args[0] == "serve" || args[0] == "version"):
		cmd, args = args[0], args[1:]
	case len(args) >= 2 && (args[0] == "admin" || args[0] == "audit"):
		cmd, args = args[0]+" "+args[1], args[2:]
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
// (e.g. -username), which parse their own flags.
func commonFlags(args []string) (config.Panel, []string, error) {
	var cfgPath string
	dev := false
	var rest []string
	for i := 0; i < len(args); i++ {
		a := strings.TrimPrefix(args[i], "-")
		a = strings.TrimPrefix(a, "-")
		switch {
		case a == "config":
			if i+1 >= len(args) {
				return config.Panel{}, nil, errors.New("-config needs a value")
			}
			cfgPath = args[i+1]
			i++
		case strings.HasPrefix(a, "config=") && strings.HasPrefix(args[i], "-"):
			cfgPath = strings.TrimPrefix(a, "config=")
		case a == "dev" && strings.HasPrefix(args[i], "-"):
			dev = true
		case (a == "dev=true" || a == "dev=false") && strings.HasPrefix(args[i], "-"):
			dev = a == "dev=true"
		default:
			rest = append(rest, args[i])
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return config.Panel{}, nil, err
	}
	cfg, err := config.LoadPanel(cfgPath, dev, root)
	return cfg, rest, err
}

func openDB(ctx context.Context, cfg config.Panel) (*sql.DB, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
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

	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clock.Real{}, Agent: agent.NewClient(cfg.AgentSocket)})
	if err != nil {
		return err
	}
	defer a.Close()

	// Background loops get their own context so they outlive the signal until HTTP is drained.
	bgCtx, cancelBg := context.WithCancel(context.Background())
	bgDone := make(chan struct{})
	go func() { a.Run(bgCtx); close(bgDone) }()

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           a.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("panel listening", "addr", cfg.Listen, "dev", cfg.Dev, "version", version)
		errCh <- srv.ListenAndServe()
	}()
	var serveErr error
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	case <-ctx.Done():
	}
	// 1. stop HTTP, 2. cancel background loops, 3. wait for them, 4. (deferred) close DB.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	cancelBg()
	<-bgDone
	return serveErr
}

func setupLogging(dev bool) {
	var h slog.Handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	if dev {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(h))
}
