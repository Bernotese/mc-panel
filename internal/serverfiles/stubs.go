package serverfiles

import (
	"context"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

// Transitional handlers: Task 10 replaces upload and download, Task 11 extract and jobs and
// deletes this file.

func writeNotYet(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, "files not implemented yet")
}

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request)   { writeNotYet(w, r) }
func (h *Handler) handleDownload(w http.ResponseWriter, r *http.Request) { writeNotYet(w, r) }
func (h *Handler) handleExtract(w http.ResponseWriter, r *http.Request)  { writeNotYet(w, r) }
func (h *Handler) handleJobs(w http.ResponseWriter, r *http.Request)     { writeNotYet(w, r) }

// uploadSlots is defined in Task 10; until then a placeholder keeps NewHandler compiling.
type uploadSlots struct{}

func newUploadSlots() *uploadSlots { return &uploadSlots{} }

// ExtractRunning is replaced in Task 11 (servers.ExtractGate); until then: never.
func (h *Handler) ExtractRunning(context.Context, int64) bool { return false }
