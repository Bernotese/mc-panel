package defense

import "mcpanel/internal/rbac"

// Routes registers the security-center endpoints with full paths relative to /api.
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/security/overview", rbac.Admin(), h.overview)
	api.Get("/security/attempts", rbac.Admin(), h.attempts)
	api.Get("/security/jails", rbac.Admin(), h.listJails)
	api.Post("/security/jails", rbac.Admin().WithSudo(), h.createJail)
	api.Delete("/security/jails/{id}", rbac.Admin().WithSudo(), h.liftJail)
	api.Get("/security/ip-rules", rbac.Admin(), h.listIPRules)
	api.Post("/security/ip-rules", rbac.Admin().WithSudo(), h.createIPRule)
	api.Delete("/security/ip-rules/{id}", rbac.Admin().WithSudo(), h.deleteIPRule)
	api.Get("/security/settings", rbac.Admin(), h.getSettings)
	api.Put("/security/settings", rbac.Admin().WithSudo(), h.putSettings)
	api.Get("/security/agent", rbac.Admin(), h.agentStatus)
}
