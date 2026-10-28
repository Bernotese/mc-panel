package servers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/servers/admit"
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
	ErrDataBusy         = admit.ErrDataBusy
	ErrRecoveryRequired = admit.ErrRecoveryRequired
	ErrDataStateUnknown = admit.ErrDataStateUnknown
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
	return admit.WriteError(w, r, err)
}

// handleResolveRecovery answers POST /servers/{id}/recovery/resolve (admin, sudo; data safety
// spec §2.3): under the server's admission lock, a server in recovery_required gets its
// half-swapped restore folders moved out of the data directory by the files service (never
// deleted; the visible data directory is not touched) and its journal entry removed. The
// operator has checked the data directory before (docs/install.md, "A restore could not be
// completed"). Afterwards the reconcile loop may start the server again.
func (h *Handler) handleResolveRecovery(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	unlock, err := h.admission.Lock(ctx, s.ID)
	if err != nil {
		if !WriteDataStateError(w, r, err) {
			h.log.DebugContext(ctx, "servers: resolve admission ended with the request", "err", err)
		}
		return
	}
	defer unlock()
	info, err := h.data.Lookup(ctx, s.ID, nil)
	switch info.State {
	case StateRecoveryRequired:
	case StateUnknown:
		if ctx.Err() == nil {
			h.log.WarnContext(ctx, "servers: resolve refused, data state unknown", "server_id", s.ID, "err", err)
		}
		WriteDataStateError(w, r, ErrDataStateUnknown)
		return
	default:
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeRecoveryRequired, "this server has no restore to resolve")
		return
	}
	if h.files == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, "the files service is not configured")
		return
	}
	rec, err := h.files.ResolveRecovery(ctx, s.ID)
	details := map[string]any{"name": s.Name}
	if err != nil {
		h.record(ctx, audit.ActionServerRecoveryResolve, serverTarget(s.ID), audit.ResultFailure, details)
		var se *filesapi.StatusError
		switch {
		case errors.As(err, &se) && (se.Status == http.StatusConflict || se.Status == http.StatusTooManyRequests):
			httpx.WriteError(w, r, http.StatusConflict, httpx.CodeRecoveryRequired, "the files service refused the resolve: the server's restore is not unresolved (any more)")
		case ctx.Err() != nil:
			h.log.DebugContext(ctx, "servers: resolve ended with the request", "err", err)
		default:
			// the service's error may name host paths: log it, show a fixed text
			h.log.ErrorContext(ctx, "servers: resolve a stuck restore", "server_id", s.ID, "err", err)
			httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, "the files service could not resolve the restore")
		}
		return
	}
	details["phase"], details["folder"] = rec.Phase, rec.Folder
	h.record(ctx, audit.ActionServerRecoveryResolve, serverTarget(s.ID), audit.ResultSuccess, details)
	h.Trigger()
	httpx.WriteJSON(w, http.StatusOK, rec)
}
