package defense

import "mcpanel/internal/rbac"

// Routes registers the security-center endpoints with full paths relative to /api.
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/security/jails", rbac.Admin(), h.listJails)
	api.Post("/security/jails", rbac.Admin().WithSudo(), h.createJail)
	api.Delete("/security/jails/{id}", rbac.Admin().WithSudo(), h.liftJail)
}
