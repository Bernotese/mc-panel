package servers

import (
	"context"
	"net/http"

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
}

// handleResolveRecovery answers POST /servers/{id}/recovery/resolve; the contract stub until
// data safety plan Task 5 implements it.
func (h *Handler) handleResolveRecovery(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusConflict, httpx.CodeRecoveryRequired, "resolving a stuck restore is not available yet")
}
