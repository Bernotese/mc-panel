package servers

import (
	"context"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/servers/admit"
)

// AdmissionWait is how long a handler waits for a server's admission lock (data safety spec
// §3); then it answers 409 conflict.
const AdmissionWait = 5 * time.Second

// ErrAdmissionBusy: another operation on the server held its admission lock for longer than
// the caller waits.
var ErrAdmissionBusy = admit.ErrAdmissionBusy

// Admission is the per-server admission lock of data safety spec §3: every start and every
// data job (restore, dev sync, extract) checks the data state and makes its agent or files
// call under it. Lock order: Admission(id) -> reconcile.mu -> (backup queue | extract pending)
// -> files/agent I/O. The reconcile pass holds reconcile.mu and only TryLocks Admission(id), so
// it never waits for a data job, and nobody waits for an admission lock while holding
// reconcile.mu. The zero value is not usable; use NewAdmission.
type Admission struct {
	wait  time.Duration
	mu    sync.Mutex
	locks map[int64]*admissionLock
}

type admissionLock struct {
	ch   chan struct{} // holds one token while locked
	refs int           // holders and waiters; the entry goes when it drops to 0
}

func NewAdmission() *Admission {
	return &Admission{wait: AdmissionWait, locks: map[int64]*admissionLock{}}
}

func (a *Admission) ref(id int64) *admissionLock {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.locks[id]
	if l == nil {
		l = &admissionLock{ch: make(chan struct{}, 1)}
		a.locks[id] = l
	}
	l.refs++
	return l
}

func (a *Admission) unref(id int64, l *admissionLock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if l.refs--; l.refs == 0 {
		delete(a.locks, id)
	}
}

// Lock takes the lock of server id, waiting at most AdmissionWait (ErrAdmissionBusy) or until
// ctx is done (its error).
func (a *Admission) Lock(ctx context.Context, id int64) (unlock func(), err error) {
	l := a.ref(id)
	t := time.NewTimer(a.wait)
	defer t.Stop()
	select {
	case l.ch <- struct{}{}:
		return a.unlockFunc(id, l), nil
	case <-t.C:
		err = ErrAdmissionBusy
	case <-ctx.Done():
		err = ctx.Err()
	}
	a.unref(id, l)
	return nil, err
}

// TryLock takes the lock of server id only if it is free (the reconcile pass skips a server
// whose admission is held).
func (a *Admission) TryLock(id int64) (unlock func(), ok bool) {
	l := a.ref(id)
	select {
	case l.ch <- struct{}{}:
		return a.unlockFunc(id, l), true
	default:
		a.unref(id, l)
		return nil, false
	}
}

func (a *Admission) unlockFunc(id int64, l *admissionLock) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			<-l.ch
			a.unref(id, l)
		})
	}
}

// Admit runs fn under the admission lock of server id (waiting at most AdmissionWait) once its
// data state is safe, leaving out the caller's own hold (nil = none). It returns
// ErrAdmissionBusy, the error of a state that is not safe (StateErr) or fn's error; fn runs at
// most once and only while the lock is held, and may take reconcile.mu (lock order).
// backups, serverfiles and devinst admit their data jobs through it.
func (h *Handler) Admit(ctx context.Context, id int64, own Hold, fn func(ctx context.Context) error) error {
	// A state that is not safe is refused at once, without waiting for the lock.
	if err := h.checkData(ctx, id, own); err != nil {
		return err
	}
	unlock, err := h.admission.Lock(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	if err := h.checkData(ctx, id, own); err != nil {
		return err
	}
	return fn(ctx)
}

// checkData returns nil if the data state of server id (without the hold own) is safe or one
// of also, else its admission error (StateErr); the files service's error behind an unknown
// state goes to the debug log.
func (h *Handler) checkData(ctx context.Context, id int64, own Hold, also ...State) error {
	info, err := h.data.Lookup(ctx, id, own)
	if err != nil && ctx.Err() == nil {
		h.log.DebugContext(ctx, "servers: data state unknown, nothing is admitted", "server_id", id, "err", err)
	}
	for _, s := range also {
		if info.State == s {
			return nil
		}
	}
	return StateErr(info.State)
}
