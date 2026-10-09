//go:build !linux

package firewall

import (
	"log/slog"

	"mcpanel/internal/agent/agentapi"
)

func newNFTables(*slog.Logger) (agentapi.Firewall, error) { return nil, ErrUnsupported }
