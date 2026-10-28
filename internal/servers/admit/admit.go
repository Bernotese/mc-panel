// Package admit holds the admission errors of data safety spec §3 and their HTTP answers, so
// the packages that admit data jobs through servers.Handler.Admit (backups, serverfiles,
// devinst) can tell them apart without importing servers (which imports backups).
package admit

import (
	"context"
	"errors"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

// Func runs fn under the admission lock of server id once its data state is safe (the caller's
// own hold left out); servers.Handler.Admit bound to the caller's hold. It returns one of the
// errors below, ctx's error or fn's error.
type Func func(ctx context.Context, id int64, fn func(context.Context) error) error

var (
	// ErrAdmissionBusy: another operation held the server's admission lock for longer than the
	// caller waits.
	ErrAdmissionBusy = errors.New("another operation on this server is being admitted")
	// ErrDataBusy: a job rewrites the server's data directory (data state busy).
	ErrDataBusy = errors.New("a job is rewriting this server's data")
	// ErrRecoveryRequired: a restore of the server could not be completed.
	ErrRecoveryRequired = errors.New("a restore of this server could not be completed")
	// ErrDataStateUnknown: the files service did not report the data state (fail closed).
	ErrDataStateUnknown = errors.New("the files service did not report whether this server's data is safe")
)

// WriteError answers an admission error (data safety spec §4) and reports whether err was one.
func WriteError(w http.ResponseWriter, r *http.Request, err error) bool {
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
