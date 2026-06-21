// Package router assembles the chi router: public routes, the auth boundary, and
// the versioned /v1 API surface.
package router

import (
	"log/slog"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/danielpang/dropway/internal/logx"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/services/api/internal/handlers"
)

// New builds the HTTP router. `verifier` verifies the Bearer EdDSA JWT for the
// authenticated routes; `api` carries the handler dependencies (quota seam, the
// Store, object storage, and the projection writer). `baseLogger` is the root
// logger the per-request logx middleware derives request_id-tagged loggers from.
//
// It returns the concrete *chi.Mux (not just http.Handler) so the cloud build can
// mount additional routes onto it via mountCloud (the /webhooks/stripe webhook +
// the authed /v1/billing/* group). The OSS build's mountCloud is a no-op, so the
// returned mux is identical to the self-host surface — billing routes simply don't
// exist there (self-host has no billing).
func New(verifier middleware.Verifier, api *handlers.API, baseLogger *slog.Logger) *chi.Mux {
	if baseLogger == nil {
		baseLogger = slog.Default()
	}
	r := chi.NewRouter()

	// Baseline middleware: SANITIZE the inbound X-Request-Id (drop a forgeable value
	// so chi mints a fresh one — prevents log/audit forgery via injected newlines /
	// control chars), THEN request id, real-ip, the structured per-request logger
	// (must run AFTER RequestID so it can tag the id), then panic recovery.
	r.Use(logx.SanitizeRequestID)
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(logx.Middleware(baseLogger))
	r.Use(chimw.Recoverer)

	// Public, unauthenticated.
	r.Get("/healthz", api.Healthz)

	// Public edge JWKS: the serving Worker fetches this to verify the host-scoped
	// edge token (separate keypair from Better Auth's user JWKS). No auth, cacheable.
	r.Get("/.well-known/edge-jwks", api.EdgeJWKS)

	// The password exchange is JWT-FREE: the dashboard renders a platform-controlled
	// password form that an UN-signed-in viewer may submit, so it must not require a
	// Better Auth token. It mints an ANON edge token (no identity). It still lives
	// under /v1/authz/password to match the API contract.
	r.Post("/v1/authz/password", api.AuthzPassword)

	// Authenticated control-plane surface. Everything else under /v1 requires a
	// verified EdDSA JWT (the authz boundary), then ensure-org-provisioned.
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.Auth(verifier))
		r.Use(api.EnsureOrgProvisioned)

		r.Get("/me", api.Me)
		r.Get("/members", api.ListMembers)
		// Logical storage usage per user (the members-page usage column). Any member
		// may read it (org-scoped). Display-only attribution, not an entitlement.
		r.Get("/storage", api.StorageUsage)
		// Members cap gate (H8): the dashboard invite path calls this before adding a
		// member; 402 when the org is at/over its members_per_org cap (cloud bands;
		// OSS unlimited). Any member may call it (read-only check of its own org).
		r.Get("/members/preflight", api.MembersPreflight)

		// Membership audit trail: Better Auth owns invites + joins (in the dashboard),
		// but the Go API is the audit system of record, so the dashboard records them
		// here after the fact. invites = admin/owner only (the inviter); joined = any
		// member recording their OWN join into the active org.
		r.Post("/members/invites", api.RecordMemberInvite)
		r.Post("/members/joined", api.RecordMemberJoin)

		// Hard revocation (Phase 4): admin/owner writes the edge denylist so a
		// removed/banned member's edge tokens are rejected immediately, not just at
		// the short TTL.
		r.Post("/members/{userId}/revoke", api.RevokeMember)

		// Audit log (Phase 4): admin/owner reads the org's sensitive-action trail,
		// newest first, paginated (RLS-scoped).
		r.Get("/audit", api.ListAudit)

		// Org-level policy: read the current value (any member, for the live toggle
		// state — H10); writing it is admin/owner only (re-checked in the handler).
		r.Get("/orgs/policy", api.GetOrgPolicy)
		r.Put("/orgs/allow-external-sharing", api.SetAllowExternalSharing)
		// MCP access toggle (admin/owner only, re-checked in the handler). Default on;
		// disabling immediately stops the Dropway MCP server from serving the org.
		r.Patch("/orgs/mcp", api.SetMcpEnabled)

		// Generic hard-revoke (admin/owner only): {kind:user|site|org, id} → bump the
		// denylist min_iat. The unified "sign-out-everywhere" affordance the dashboard
		// calls; complements the RESTful members/{id}/revoke + sites/{id}/revoke-access.
		r.Post("/orgs/revoke-access", api.RevokeAccess)

		// The cross-domain /authz viewer exchange (Phase 2). mint takes the viewer's
		// Better Auth JWT (so it's behind Auth). password is mounted JWT-free above.
		r.Post("/authz/mint", api.AuthzMint)

		r.Route("/sites", func(r chi.Router) {
			r.Post("/", api.CreateSite)
			r.Get("/", api.ListSites)
			r.Get("/{id}", api.GetSite)
			// Deploy history (newest first) for the rollback picker.
			r.Get("/{id}/versions", api.ListVersions)

			r.Post("/{id}/deployments/prepare", api.PrepareDeployment)
			r.Post("/{id}/deployments", api.FinalizeDeployment)
			r.Post("/{id}/publish", api.Publish)

			// Phase 2 — access control & domains.
			r.Put("/{id}/access", api.SetSiteAccess)

			// Phase 4 — generic admin hard-revoke of a site's edge tokens (a
			// "kill the share now" affordance independent of an access change).
			r.Post("/{id}/revoke-access", api.RevokeSiteAccess)

			r.Post("/{id}/allowlist", api.AddAllowlistEntry)
			r.Delete("/{id}/allowlist", api.RemoveAllowlistEntry)
			r.Get("/{id}/allowlist", api.ListAllowlist)

			r.Post("/{id}/domains", api.AddDomain)
			r.Get("/{id}/domains", api.ListDomains)
		})

		// Poll a custom domain's verification status (drives the state machine).
		r.Get("/domains/{domainID}/status", api.GetDomainStatus)
		// Remove a custom domain (admin/owner): drops the route + Cloudflare hostname.
		r.Delete("/domains/{domainID}", api.DeleteDomain)
	})

	return r
}
