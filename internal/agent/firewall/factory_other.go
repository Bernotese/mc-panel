//go:build !linux

package firewall

import (
	"log/slog"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func newNFTables(*slog.Logger) (agentapi.Firewall, error) { return nil, ErrUnsupported }
