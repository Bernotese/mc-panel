package servers

import "context"

// BackupPurger deletes the backups of a deleted server (backups spec §5.6); backups.Handler
// implements it.
type BackupPurger interface {
	PurgeBackups(ctx context.Context, serverID int64) error
}

// noPurger has nothing to delete (no backups wired).
type noPurger struct{}

func (noPurger) PurgeBackups(context.Context, int64) error { return nil }
