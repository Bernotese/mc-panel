package plugins

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

func notYet(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodePluginSourceUnavailable, "plugins not implemented yet")
}

// task 11
func (h *Handler) handleInstall(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *Handler) handleUpdates(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request)  { notYet(w, r) }
func (h *Handler) handleJobs(w http.ResponseWriter, r *http.Request)    { notYet(w, r) }
func (h *Handler) jobRunning(int64) bool                                { return false }

// task 12
func (h *Handler) handleToggle(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *Handler) handleIcon(w http.ResponseWriter, r *http.Request)   { notYet(w, r) }
