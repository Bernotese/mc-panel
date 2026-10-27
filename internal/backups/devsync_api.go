package backups

import "context"

// DevSync describes one sync of a dev instance from its source (dev instances spec §4.1),
// queued with QueueDevSync as a job of the backup queue.
type DevSync struct {
	DevID, SourceID     int64
	DevName, SourceName string
	Trigger             string // "scheduled" | "manual" | "create"
	// Apply runs after a successful restore, before the gate opens (servers.ApplyDevSpec).
	Apply func(ctx context.Context) error
	// Done gets the snapshot used and the job's error (nil on success); never nil.
	Done func(snapshot string, err error)
}
