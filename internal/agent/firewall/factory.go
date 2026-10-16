package firewall

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

// ErrUnsupported is returned when the configured backend cannot run on this OS.
var ErrUnsupported = errors.New("firewall backend not supported on this platform")

// New returns the backend selected by cfg.Firewall for the running OS.
func New(cfg config.Agent, clk clock.Clock, log *slog.Logger) (Backend, error) {
	backend, err := choose(cfg.Firewall, runtime.GOOS)
	if err != nil {
		return nil, err
	}
	if backend == "noop" {
		if !cfg.Dev {
			log.Warn("firewall backend is noop: bans are kept in memory only and NOT enforced")
		}
		return NewNoop(clk, log), nil
	}
	return newNFTables(log, cfg.EgressDenyPrefixes())
}

func choose(name, goos string) (string, error) {
	switch name {
	case "noop":
		return "noop", nil
	case "nftables":
		if goos != "linux" {
			return "", fmt.Errorf("%w: nftables requires linux (running on %s); set firewall = \"noop\" for development", ErrUnsupported, goos)
		}
		return "nftables", nil
	default:
		return "", fmt.Errorf("firewall: unknown backend %q", name)
	}
}
