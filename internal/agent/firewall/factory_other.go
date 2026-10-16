//go:build !linux

package firewall

import (
	"log/slog"
	"net/netip"
)

func newNFTables(*slog.Logger, []netip.Prefix) (Backend, error) { return nil, ErrUnsupported }
