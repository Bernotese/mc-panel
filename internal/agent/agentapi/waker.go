package agentapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

const (
	PathWaker = "/v1/waker"
	// MaxWakerTargets bounds one request (dev instances spec §6.2).
	MaxWakerTargets = 500
	// MaxWakerRequestBytes is the body limit of PUT /v1/waker (500 targets of at most 253-byte names).
	MaxWakerRequestBytes = 256 << 10
)

// WakerTarget is a sleeping dev instance: while it is listed, mc-router sends FQDN to the
// waker container instead of the container of server ID.
type WakerTarget struct {
	ID   int64  `json:"id"`
	FQDN string `json:"fqdn"`
}

// WakerRequest is the complete list of sleeping dev instances: a dev missing from it is
// routed to its own container again.
type WakerRequest struct {
	Sleeping []WakerTarget `json:"sleeping"` // never null on the wire; nil is rejected
}

// WakerResponse reports whether the waker runs and the ids of listed devs a player tried to
// join since the previous call (each id at most once).
type WakerResponse struct {
	Ready bool    `json:"ready"`
	Wakes []int64 `json:"wakes"`
}

// WakerClient is the panel-side view of the waker endpoint.
type WakerClient interface {
	Waker(ctx context.Context, req WakerRequest) (WakerResponse, error)
}

// Validate checks the list; every error wraps instancespec.ErrInvalid. Only names of dev
// instances ("dev-<name>.<base domain>") can be listed.
func (r WakerRequest) Validate() error {
	if r.Sleeping == nil {
		return fmt.Errorf("%w: sleeping: required", instancespec.ErrInvalid)
	}
	if len(r.Sleeping) > MaxWakerTargets {
		return fmt.Errorf("%w: sleeping: at most %d", instancespec.ErrInvalid, MaxWakerTargets)
	}
	ids := make(map[int64]struct{}, len(r.Sleeping))
	fqdns := make(map[string]struct{}, len(r.Sleeping))
	for i, t := range r.Sleeping {
		if t.ID <= 0 {
			return fmt.Errorf("%w: sleeping[%d].id: must be > 0", instancespec.ErrInvalid, i)
		}
		if _, dup := ids[t.ID]; dup {
			return fmt.Errorf("%w: sleeping[%d].id: duplicate %d", instancespec.ErrInvalid, i, t.ID)
		}
		ids[t.ID] = struct{}{}
		if err := instancespec.CheckDNSName(t.FQDN); err != nil {
			return fmt.Errorf("sleeping[%d]: %w", i, err)
		}
		name, _, ok := strings.Cut(t.FQDN, ".")
		if !ok || !strings.HasPrefix(name, "dev-") || !instancespec.ValidName(name) {
			return fmt.Errorf("%w: sleeping[%d].fqdn: must be dev-<name>.<base domain>", instancespec.ErrInvalid, i)
		}
		if _, dup := fqdns[t.FQDN]; dup {
			return fmt.Errorf("%w: sleeping[%d].fqdn: duplicate %q", instancespec.ErrInvalid, i, t.FQDN)
		}
		fqdns[t.FQDN] = struct{}{}
	}
	return nil
}
