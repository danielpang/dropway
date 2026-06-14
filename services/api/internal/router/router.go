// Package router assembles the chi router: public routes, the auth boundary, and
// the versioned /v1 API surface.
package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/danielpang/shipped/internal/middleware"
	"github.com/danielpang/shipped/services/api/internal/handlers"
)

// New builds the HTTP handler. `verifier` verifies the Bearer EdDSA JWT for the
// authenticated routes; `api` carries the handler dependencies (the quota seam).
func New(verifier middleware.Verifier, api *handlers.API) http.Handler {
	r := chi.NewRouter()

	// Baseline middleware: request id, panic recovery, real-ip. These are
	// observability/safety, not authz.
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)

	// Public, unauthenticated.
	r.Get("/healthz", api.Healthz)

	// Authenticated control-plane surface. Everything under /v1 requires a
	// verified EdDSA JWT (the authz boundary, §3).
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.Auth(verifier))
		r.Get("/me", api.Me)
		r.Post("/sites", api.CreateSite)
	})

	return r
}
