//go:build linux

package firewall

import (
	"log/slog"
	"net/netip"
)

func newNFTables(log *slog.Logger, egressDeny []netip.Prefix) (Backend, error) {
	fw, err := NewNFTables(log, egressDeny)
	if err != nil {
		return nil, err // never return a typed nil pointer inside the interface
	}
	return fw, nil
}
