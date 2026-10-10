//go:build linux

package firewall

import (
	"log/slog"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func newNFTables(log *slog.Logger) (agentapi.Firewall, error) {
	fw, err := NewNFTables(log)
	if err != nil {
		return nil, err // never return a typed nil pointer inside the interface
	}
	return fw, nil
}
