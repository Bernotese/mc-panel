// Package devinst implements dev instances (dev instances spec): a copy of a server ("prod")
// at dev-<name>.<base domain> that is overwritten from a restic snapshot of prod every night,
// sleeps when nobody plays and wakes when a player connects. It owns the dev API, the nightly
// schedule, sleep detection, the dev ops list and the wake loop; the sync itself runs as a job
// of the backup queue (internal/backups) and the spec copy lives in internal/servers.
package devinst

import (
	"log/slog"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

type Deps struct {
	Log *slog.Logger // nil = slog.Default()
}

type Handler struct {
	log *slog.Logger
}

func NewHandler(d Deps) *Handler {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Handler{log: d.Log}
}

// Routes registers the dev API (paths relative to /api).
func (h *Handler) Routes(api *rbac.Router) {
	api.Post("/servers/{id}/dev", rbac.Admin().WithSudo(), h.notYet)
	api.Get("/servers/{id}/dev", rbac.Server(rbac.PermServerView, "id"), h.notYet)
	api.Patch("/servers/{id}/dev", rbac.Server(rbac.PermServerFiles, "id"), h.notYet)
	api.Post("/servers/{id}/dev/sync", rbac.Server(rbac.PermServerFiles, "id"), h.notYet)
	api.Get("/servers/{id}/dev/ops", rbac.Server(rbac.PermServerConsole, "id"), h.notYet)
	api.Post("/servers/{id}/dev/ops", rbac.Server(rbac.PermServerConsole, "id"), h.notYet)
	api.Delete("/servers/{id}/dev/ops/{name}", rbac.Server(rbac.PermServerConsole, "id"), h.notYet)
}

// notYet answers every dev endpoint until the feature is wired (implementation plan, Task 8).
func (h *Handler) notYet(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotDev, "dev instances are not available yet")
}
