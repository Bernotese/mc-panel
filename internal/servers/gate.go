package servers

import "context"

// DataGate is consulted before a user's power start/restart and by the reconcile loop before
// it starts a server: while a job rewrites the data directory (an archive is extracted, files
// spec §5.1, or a backup is restored, backups spec §5.5), the server must not start.
// serverfiles.Handler and backups.Handler implement it.
type DataGate interface {
	DataJobRunning(ctx context.Context, serverID int64) bool
}

// noGate never blocks (nothing wired).
type noGate struct{}

func (noGate) DataJobRunning(context.Context, int64) bool { return false }

// anyGate holds a start while one of its gates does; the gates are asked in order.
type anyGate []DataGate

func (g anyGate) DataJobRunning(ctx context.Context, serverID int64) bool {
	for _, gate := range g {
		if gate.DataJobRunning(ctx, serverID) {
			return true
		}
	}
	return false
}

// AnyGate combines gates (nil entries are skipped). Put cheap gates first: the first one that
// holds decides, later ones are not asked.
func AnyGate(gates ...DataGate) DataGate {
	out := make(anyGate, 0, len(gates))
	for _, g := range gates {
		if g != nil {
			out = append(out, g)
		}
	}
	return out
}
