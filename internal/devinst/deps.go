package devinst

import (
	"context"
	"time"

	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Servers is the part of servers.Handler the dev instances use (the spec copy, the instance
// rows and the power state stay in internal/servers).
type Servers interface {
	// CreateDev writes server, instance and dev_instances row of the dev of prodID.
	CreateDev(ctx context.Context, prodID int64, memoryMiB *int) (store.Server, error)
	// ApplyDevSpec copies prod's spec onto the dev and applies the dev overrides.
	ApplyDevSpec(ctx context.Context, devID int64) error
	// SetDevSleeping puts the dev to sleep (desired stopped) or wakes it (desired running).
	SetDevSleeping(ctx context.Context, devID int64, sleeping bool, now time.Time) error
}

// Syncer queues a dev sync in the backup queue (backups.Handler).
type Syncer interface {
	QueueDevSync(ctx context.Context, s backups.DevSync) error
}

// Errors of Servers.CreateDev and Syncer.QueueDevSync that map to their own API answers.
// Validation errors (name too long, memory) wrap instancespec.ErrInvalid.
var (
	errServerNotFound    = servers.ErrServerNotFound
	errDevExists         = servers.ErrDevExists
	errNoInstance        = servers.ErrNoInstance
	errNameTaken         = servers.ErrNameTaken
	errBaseDomainMissing = servers.ErrBaseDomainMissing
	errSyncBusy          = backups.ErrBackupRunning
)
