package rbac_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/clock"
	"mcpanel/internal/rbac"
)

func noopHandler(http.ResponseWriter, *http.Request) {}

func buildCoverageMux(catchAll bool) (*chi.Mux, *rbac.Router) {
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, fakeChecker{}, clock.Real{})
	rt.Route("/api", func(api *rbac.Router) {
		api.Get("/health", rbac.Public(), noopHandler)
		api.Post("/servers/{id}/power", rbac.Server(rbac.PermServerPower, "id"), noopHandler)
	})
	// Routes registered directly on chi bypass rule enforcement and must be reported.
	mux.Post("/debug/reset", noopHandler)
	mux.Route("/internal", func(r chi.Router) { r.Get("/x", noopHandler) })
	if catchAll {
		mux.Handle("/*", http.HandlerFunc(noopHandler))
	}
	return mux, rt
}

func TestUncoveredReportsRawChiRoutes(t *testing.T) {
	want := []string{"GET /internal/x", "POST /debug/reset"}

	mux, rt := buildCoverageMux(false)
	if got := rbac.Uncovered(mux, rt.Routes(), nil); !slices.Equal(got, want) {
		t.Fatalf("Uncovered = %v, want %v", got, want)
	}

	mux, rt = buildCoverageMux(true)
	ignoreSPA := func(_, pattern string) bool { return pattern == "/*" }
	if got := rbac.Uncovered(mux, rt.Routes(), ignoreSPA); !slices.Equal(got, want) {
		t.Fatalf("Uncovered with ignore = %v, want %v", got, want)
	}
	if got := rbac.Uncovered(mux, rt.Routes(), nil); !slices.Contains(got, "GET /*") {
		t.Fatalf("catch-all must be reported without ignore, got %v", got)
	}
}

func TestUncoveredFullyCovered(t *testing.T) {
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, fakeChecker{}, clock.Real{})
	rt.Route("/api", func(api *rbac.Router) {
		api.Get("/users/{id}", rbac.Admin(), noopHandler)
		api.Delete("/users/{id}", rbac.Admin().WithSudo(), noopHandler)
	})
	if got := rbac.Uncovered(mux, rt.Routes(), nil); len(got) != 0 {
		t.Fatalf("Uncovered = %v, want none", got)
	}
}
