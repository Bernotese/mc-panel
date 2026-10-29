package files

import "context"

// lease takes the mutation lease of server id for a plain file mutation (Write, Mkdir, Rename,
// Delete, Upload, UploadChecked; data safety spec §2.1) for its whole duration: refused at once
// while a restore, extract or resolve holds the server (ErrBusy), while the server needs
// recovery (ErrBusy) and while the restore journal cannot be read (errJournalUnreadable). It is
// taken before the instance root is opened.
//
// The journal is checked after the lease is counted: a restore can only write a journal entry
// after it has waited for every lease to end, so an entry that is not there now does not appear
// while the lease is held.
func (s *Service) lease(id int64) (release func(), err error) {
	release, err = s.jobs.lease(id)
	if err != nil {
		return nil, err
	}
	if err := s.requireNoRecovery(id); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// acquireRewrite finishes taking server id for a job that rewrites its data directory (restore,
// extract) once its slot is held: it waits for the mutation leases to end (jobs.drain; new ones
// are refused meanwhile) and refuses a server that needs recovery. On error the caller gives the
// slot back.
func (s *Service) acquireRewrite(ctx context.Context, id int64) error {
	if err := s.jobs.drain(ctx, id); err != nil {
		return err
	}
	return s.requireNoRecovery(id)
}
