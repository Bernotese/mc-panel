package servers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

// State is the safety of a server's data directory as the panel sees it (data safety spec
// §2): the files service's answer, or StateUnknown when it did not give a valid one. Only
// StateSafe admits a start or a data job; stop and reads are never blocked by it.
type State string

const (
	StateSafe             State = "safe"
	StateBusy             State = "busy"
	StateRecoveryRequired State = "recovery_required"
	StateUnknown          State = "unknown"
)

// DataStates answers the data state of a server. An error comes with StateUnknown; callers
// treat it as "not safe" (fail closed) and may log it.
type DataStates interface {
	State(ctx context.Context, id int64) (State, error)
	// Lookup is State with the kind of the job that holds a busy server, leaving out the
	// caller's own hold skip (nil = none): a restore asks whether anything but itself holds
	// the server.
	Lookup(ctx context.Context, id int64, skip Hold) (DataInfo, error)
}

// DataInfo is a server's data state and, while it is busy, the kind of the job that holds it
// (restore, extract, dev_sync; "" = unknown kind).
type DataInfo struct {
	State State
	Job   string
}

// Hold is a panel-side hold on a server's data directory that the files service does not know
// (yet): the backup queue's waiting or running restore and dev sync, an extract request in
// flight. kind is the job kind shown as data_job. A Hold must be comparable (a pointer):
// Lookup compares it with the caller's own.
type Hold interface {
	Holding(id int64) (kind string, ok bool)
}

// stateTimeout bounds the files service's answer (data safety plan, Global Constraints).
const stateTimeout = 2 * time.Second

// Data state errors of an admission (Admit); WriteDataStateError answers them.
var (
	ErrDataBusy         = errors.New("a job is rewriting this server's data")
	ErrRecoveryRequired = errors.New("a restore of this server could not be completed")
	ErrDataStateUnknown = errors.New("the files service did not report whether this server's data is safe")
)

// StateErr is the admission error of a data state: nil for StateSafe, else one of ErrDataBusy,
// ErrRecoveryRequired and ErrDataStateUnknown.
func StateErr(s State) error {
	switch s {
	case StateSafe:
		return nil
	case StateBusy:
		return ErrDataBusy
	case StateRecoveryRequired:
		return ErrRecoveryRequired
	}
	return ErrDataStateUnknown
}

// FilesDataStates combines the panel's holds with the files service's GET /v1/state (data
// safety spec §2.3). It fails closed: a transport error, a timeout or an invalid answer is
// StateUnknown, never safe.
type FilesDataStates struct {
	files   filesapi.StateClient // nil = no files service: no data job can exist, every server is safe
	holds   []Hold
	timeout time.Duration
}

// NewDataStates returns the data states of files (nil = no files service) and the holds (nil
// entries are skipped; the holds are asked before the service).
func NewDataStates(files filesapi.StateClient, holds ...Hold) *FilesDataStates {
	d := &FilesDataStates{files: files, timeout: stateTimeout}
	for _, h := range holds {
		if h != nil {
			d.holds = append(d.holds, h)
		}
	}
	return d
}

func (d *FilesDataStates) State(ctx context.Context, id int64) (State, error) {
	info, err := d.Lookup(ctx, id, nil)
	return info.State, err
}

func (d *FilesDataStates) Lookup(ctx context.Context, id int64, skip Hold) (DataInfo, error) {
	for _, h := range d.holds {
		if h == skip {
			continue
		}
		if kind, ok := h.Holding(id); ok {
			return DataInfo{State: StateBusy, Job: kind}, nil
		}
	}
	if d.files == nil {
		return DataInfo{State: StateSafe}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	resp, err := d.files.State(ctx, id)
	if err != nil {
		return DataInfo{State: StateUnknown}, err
	}
	switch resp.State {
	case filesapi.StateSafe:
		return DataInfo{State: StateSafe}, nil
	case filesapi.StateBusy:
		info := DataInfo{State: StateBusy}
		if resp.Job != nil {
			info.Job = string(resp.Job.Kind)
			if resp.Job.Kind == "" { // a service before sub-project 4 only had extract jobs
				info.Job = string(filesapi.JobExtract)
			}
		}
		return info, nil
	case filesapi.StateRecoveryRequired:
		return DataInfo{State: StateRecoveryRequired}, nil
	}
	return DataInfo{State: StateUnknown}, filesapi.ErrUnavailable
}

// safeStates stands in for missing DataStates (Deps without them): every server is safe.
type safeStates struct{}

func (safeStates) State(context.Context, int64) (State, error) { return StateSafe, nil }
func (safeStates) Lookup(context.Context, int64, Hold) (DataInfo, error) {
	return DataInfo{State: StateSafe}, nil
}

// WriteDataStateError answers an admission error (ErrAdmissionBusy, ErrDataBusy,
// ErrRecoveryRequired, ErrDataStateUnknown; data safety spec §4) and reports whether err was
// one of them.
func WriteDataStateError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case errors.Is(err, ErrAdmissionBusy):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "another operation on this server is being admitted; try again")
	case errors.Is(err, ErrDataBusy):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "a file or backup job is rewriting this server's data; try again when it is done")
	case errors.Is(err, ErrRecoveryRequired):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeRecoveryRequired, "a restore of this server could not be completed; resolve it first")
	case errors.Is(err, ErrDataStateUnknown):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeDataStateUnknown, "the files service did not report whether this server's data is safe; try again")
	default:
		return false
	}
	return true
}

// handleResolveRecovery answers POST /servers/{id}/recovery/resolve; the contract stub until
// data safety plan Task 5 implements it.
func (h *Handler) handleResolveRecovery(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusConflict, httpx.CodeRecoveryRequired, "resolving a stuck restore is not available yet")
}
