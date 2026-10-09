package rbac

import (
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
)

// Uncovered returns "METHOD /pattern" for every route reachable in mux that was not
// registered through a Router (and therefore enforces no Rule). ignore may exclude
// deliberate non-API handlers such as the SPA catch-all "/*"; it may be nil.
// The result is sorted; an empty result means every route declares a rule.
func Uncovered(mux chi.Routes, registered []RouteInfo, ignore func(method, pattern string) bool) []string {
	known := make(map[string]bool, len(registered))
	for _, ri := range registered {
		known[ri.Method+" "+ri.Pattern] = true
	}
	seen := make(map[string]bool)
	var out []string
	err := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		if known[key] || seen[key] || (ignore != nil && ignore(method, route)) {
			return nil
		}
		seen[key] = true
		out = append(out, key)
		return nil
	})
	if err != nil {
		// The walk function never fails; surface any chi error so callers' tests fail loudly.
		out = append(out, "WALK-ERROR "+err.Error())
	}
	slices.Sort(out)
	return out
}
