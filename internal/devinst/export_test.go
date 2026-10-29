package devinst

import (
	"context"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/servers"
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
	ErrDevNameTooLong = servers.ErrDevNameTooLong
	ErrInvalid        = fmt.Errorf("%w: at most 1536 on this host", servers.ErrInvalidMemory)
)

// OpsWindow is how long after a start missing dev ops are sent again on every pass.
var OpsWindow = opsWindow

// Tick runs one schedule tick (Run does it every minute).
func Tick(ctx context.Context, h *Handler) { h.tick(ctx) }

// FastTick runs one pass of the 2-second loop: delayed syncs and the waker.
func FastTick(ctx context.Context, h *Handler) { h.fastTick(ctx) }

// Recover runs the start-up recovery of Run.
func Recover(ctx context.Context, h *Handler) { h.recover(ctx) }
