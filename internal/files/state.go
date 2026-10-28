package files

import (
	"context"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// State reports the data state of server id (data safety spec §2.1): busy while a restore,
// extract or resolve holds it, recovery_required while a restore journal entry exists (stuck,
// unreadable or not recovered at start), safe otherwise. It reads the job slots and the journal
// entry on disk on every call; errJournalUnreadable when the journal folder cannot be read (the
// state of every server is unknown then).
func (s *Service) State(_ context.Context, id int64) (filesapi.StateResponse, error) {
	if id <= 0 {
		return filesapi.StateResponse{}, fmt.Errorf("%w: id must be > 0", filesapi.ErrInvalid)
	}
	// The slots are read before and after the journal: a restore that starts or ends meanwhile
	// shows as busy, never its journal entry as a recovery.
	busy := s.jobs.rewriting(id)
	e, journaled, err := s.journalEntryOf(id)
	if err != nil {
		return filesapi.StateResponse{}, err
	}
	busy = s.jobs.rewriting(id) || busy
	out := filesapi.StateResponse{State: filesapi.StateSafe, Job: s.jobs.get(id)}
	switch {
	case busy:
		out.State = filesapi.StateBusy
	case journaled:
		rec := e.recovery()
		out.State, out.Recovery = filesapi.StateRecoveryRequired, &rec
	}
	return out, nil
}

// requireNoRecovery refuses to change the data directory of server id while a restore journal
// entry exists for it (recovery_required) or the journal cannot be read (unknown: fail closed).
func (s *Service) requireNoRecovery(id int64) error {
	_, journaled, err := s.journalEntryOf(id)
	switch {
	case err != nil:
		return err
	case journaled:
		return fmt.Errorf("%w: a restore of this server could not be completed; resolve it first", filesapi.ErrBusy)
	}
	return nil
}
