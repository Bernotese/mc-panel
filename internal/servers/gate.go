package servers

import "context"

// ExtractGate is consulted before a user's power start/restart and by the reconcile loop before
// it starts a server: while an archive is being extracted into the data directory, the server
// must not start (files spec §5.1). serverfiles.Handler implements it.
type ExtractGate interface {
	ExtractRunning(ctx context.Context, serverID int64) bool
}

// noGate never blocks (no file service wired).
type noGate struct{}

func (noGate) ExtractRunning(context.Context, int64) bool { return false }
