package console

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

// Transitional handlers: tasks 10–12 of plan 03 replace them one by one and task 12 deletes
// this file.
func notYet(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "console not implemented yet")
}

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request)       { notYet(w, r) }
func (h *Handler) handlePlayerLists(w http.ResponseWriter, r *http.Request)  { notYet(w, r) }
func (h *Handler) handlePlayerAction(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
