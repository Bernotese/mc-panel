// Command panel is the mc-panel web application.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/clock"
	"mcpanel/internal/config"
	"mcpanel/internal/httpx"
	"mcpanel/internal/rbac"
	"mcpanel/internal/secretbox"
	"mcpanel/internal/store"
	"mcpanel/internal/webui"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: panel serve [-config path] [-dev]")
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "serve":
		if err := serve(args[1:]); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	default:
		usage(stderr)
		return 2
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to panel.toml")
	dev := fs.Bool("dev", false, "development mode (paths under ./.dev)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := config.LoadPanel(*cfgPath, *dev, root)
	if err != nil {
		return err
	}
	setupLogging(cfg.Dev)
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := store.Migrate(ctx, db); err != nil {
		return err
	}
	if _, err := secretbox.LoadOrCreateKey(cfg.SecretKeyPath()); err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           newRouter(cfg),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("panel listening", "addr", cfg.Listen, "dev", cfg.Dev)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func setupLogging(dev bool) {
	var h slog.Handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	if dev {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(h))
}

type noChecker struct{}

func (noChecker) HasServerPermission(context.Context, int64, int64, rbac.Permission) (bool, error) {
	return false, nil
}

// newRouter is replaced in Phase 2 by the full wiring.
func newRouter(cfg config.Panel) http.Handler {
	mux := chi.NewRouter()
	mux.Use(httpx.RequestID, httpx.RealIP(cfg.TrustedPrefixes()), httpx.SecurityHeaders, httpx.BodyLimit(1<<20))
	rt := rbac.NewRouter(mux, noChecker{}, clock.Real{})
	rt.Route("/api", func(api *rbac.Router) {
		api.Get("/health", rbac.Public(), func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
	})
	mux.Handle("/*", webui.Handler())
	return mux
}
