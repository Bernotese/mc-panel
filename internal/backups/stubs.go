package backups

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

// notImplemented answers the routes that later tasks of phase B2 implement (deleted in task 13).
func (h *Handler) notImplemented(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeBackupsUnavailable, "backups not implemented yet")
}
