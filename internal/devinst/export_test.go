package devinst

import (
	"context"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// Sentinels the fakes return (deps.go).
var (
	ErrServerNotFound    = errServerNotFound
	ErrDevExists         = errDevExists
	ErrNoInstance        = errNoInstance
	ErrNameTaken         = errNameTaken
	ErrBaseDomainMissing = errBaseDomainMissing
	ErrSyncBusy          = errSyncBusy
	// Validation errors of servers wrap instancespec.ErrInvalid.
	ErrDevNameTooLong = fmt.Errorf("%w: the name of a dev instance would exceed 32 characters", instancespec.ErrInvalid)
	ErrInvalid        = fmt.Errorf("%w: memory_mib: at most 1536 on this host", instancespec.ErrInvalid)
)

// Tick runs one schedule tick (Run does it every minute).
func Tick(ctx context.Context, h *Handler) { h.tick(ctx) }

// FastTick runs one pass of the 2-second loop: delayed syncs and the waker.
func FastTick(ctx context.Context, h *Handler) { h.fastTick(ctx) }

// Recover runs the start-up recovery of Run.
func Recover(ctx context.Context, h *Handler) { h.recover(ctx) }
