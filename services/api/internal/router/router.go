// Package router assembles the chi router: public routes, the auth boundary, and
// the versioned /v1 API surface.
package router

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/danielpang/shipped/internal/logx"
	"github.com/danielpang/shipped/internal/middleware"
	"github.com/danielpang/shipped/services/api/internal/handlers"
)

// New builds the HTTP handler. `verifier` verifies the Bearer EdDSA JWT for the
// authenticated routes; `api` carries the handler dependencies (quota seam, the
// Store, object storage, and the projection writer). `baseLogger` is the root
// logger the per-request logx middleware derives request_id-tagged loggers from.
func New(verifier middleware.Verifier, api *handlers.API, baseLogger *slog.Logger) http.Handler {
	if baseLogger == nil {
		baseLogger = slog.Default()
	}
	r := chi.NewRouter()

	// Baseline middleware: request id, real-ip, then the structured per-request
	// logger (must run AFTER RequestID so it can tag the id), then panic recovery.
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(logx.Middleware(baseLogger))
	r.Use(chimw.Recoverer)

	// Public, unauthenticated.
	r.Get("/healthz", api.Healthz)

	// Authenticated control-plane surface. Everything under /v1 requires a
	// verified EdDSA JWT (the authz boundary, §3), then ensure-org-provisioned.
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.Auth(verifier))
		r.Use(api.EnsureOrgProvisioned)

		r.Get("/me", api.Me)

		r.Route("/sites", func(r chi.Router) {
			r.Post("/", api.CreateSite)
			r.Get("/", api.ListSites)
			r.Get("/{id}", api.GetSite)

			r.Post("/{id}/deployments/prepare", api.PrepareDeployment)
			r.Post("/{id}/deployments", api.FinalizeDeployment)
			r.Post("/{id}/publish", api.Publish)
		})
	})

	return r
}
