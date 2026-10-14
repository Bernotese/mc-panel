package plugins

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

// handleMrpackPreview is a stub until the import is implemented.
func (h *Handler) handleMrpackPreview(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotImplemented, httpx.CodeInternal, "not implemented yet")
}

// handleMrpackImport is a stub until the import is implemented.
func (h *Handler) handleMrpackImport(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotImplemented, httpx.CodeInternal, "not implemented yet")
}

// Errors of the mrpack routes, exported for the contract test in cmd/panel (ErrorStatus).
var (
	ErrMrpackIncompatible = errMrpackIncompatible
	ErrMrpackMismatch     = errMrpackMismatch
	ErrNothingToImport    = errNothingToImport
)
