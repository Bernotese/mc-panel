package servers

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
)

// CatalogSource provides the version catalogs (*catalog.Client in production).
type CatalogSource interface {
	Get(ctx context.Context, t instancespec.Type) (catalog.Catalog, error)
}

func (h *Handler) handleGetCatalog(w http.ResponseWriter, r *http.Request) {
	t := instancespec.Type(chi.URLParam(r, "type"))
	if !slices.Contains(instancespec.Types, t) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "unknown server type")
		return
	}
	if t.IsModpack() {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "no version catalog: the modpack decides the version")
		return
	}
	cat, err := h.catalog.Get(r.Context(), t)
	switch {
	case errors.Is(err, catalog.ErrUnsupported):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "no version catalog for this type")
	case err != nil:
		h.log.WarnContext(r.Context(), "servers: version catalog unavailable", "type", string(t), "err", err)
		httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodeCatalogUnavailable, "version catalog unavailable, enter the version manually")
	default:
		httpx.WriteJSON(w, http.StatusOK, cat)
	}
}
