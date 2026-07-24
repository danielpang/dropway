// Package router assembles the chi router: public routes, the auth boundary, and
// the versioned /v1 API surface.
package router

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/danielpang/dropway/internal/errtrack"
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
	// errtrack.Recoverer replaces chi's Recoverer: same recover→500, but it logs
	// the panic via slog.Error so the error-tracking-wrapped default logger mirrors
	// it to the sink (carrying the stack as a property).
	r.Use(errtrack.Recoverer)

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
		// Accept either a Better Auth JWT or an org-scoped API key (api.KeyAuth,
		// nil in a DB-less/dev build → JWT only). A keyed request resolves to
		// synthesized claims, so it flows through the same tenant context, quota,
		// and handlers as a session — subject to the member-level role ceiling.
		r.Use(middleware.AuthWithKeys(verifier, api.KeyAuth))
		// Attribute any downstream exception to the authenticated user (runs after
		// Auth, before EnsureOrgProvisioned, so even provisioning errors carry the id).
		r.Use(attributeErrors)
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

		// Org feed: any member reads their org's shared (non-private) sites, newest
		// first — the cross-user discovery surface (RLS-scoped).
		r.Get("/feed", api.ListFeed)

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
			// Permanently delete a site (owner/admin; the handler re-checks). The
			// DB cascade drops its versions/routes/domains; GC reclaims the blobs.
			r.Delete("/{id}", api.DeleteSite)
			// Deploy history (newest first) for the rollback picker.
			r.Get("/{id}/versions", api.ListVersions)

			r.Post("/{id}/deployments/prepare", api.PrepareDeployment)
			r.Post("/{id}/deployments", api.FinalizeDeployment)
			r.Post("/{id}/publish", api.Publish)

			// Version previews: time-limited hosts pinned to one draft version.
			// POST creates, renews, or re-creates an expired preview; DELETE
			// removes it immediately. Publishing a version deletes its preview.
			r.Post("/{id}/versions/{versionID}/preview", api.CreatePreview)
			r.Delete("/{id}/versions/{versionID}/preview", api.DeletePreview)

			// Phase 2 — access control & domains.
			r.Put("/{id}/access", api.SetSiteAccess)

			// Org feed visibility: share a site to the org feed or make it private.
			// Owner-or-admin (the handler re-checks: a site owner may toggle their
			// own site; everyone else must be an org admin/owner).
			r.Put("/{id}/feed", api.SetSiteFeedVisibility)
			// Feed metadata (title + description), same owner-or-admin gate.
			r.Put("/{id}/feed-meta", api.SetSiteFeedMeta)
			// Up/down vote a feed post (any member; +1/-1/0 to clear).
			r.Put("/{id}/vote", api.SetSiteVote)

			// Site comments: any org member may read + post (org-internal thread,
			// RLS-scoped). Posting can tag teammates (@mentions).
			r.Get("/{id}/comments", api.ListComments)
			r.Post("/{id}/comments", api.AddComment)

			// Phase 4 — generic admin hard-revoke of a site's edge tokens (a
			// "kill the share now" affordance independent of an access change).
			r.Post("/{id}/revoke-access", api.RevokeSiteAccess)

			r.Post("/{id}/allowlist", api.AddAllowlistEntry)
			r.Delete("/{id}/allowlist", api.RemoveAllowlistEntry)
			r.Get("/{id}/allowlist", api.ListAllowlist)

			r.Post("/{id}/domains", api.AddDomain)
			r.Get("/{id}/domains", api.ListDomains)

			// Site-scoped chat-log convenience: read the attached "How this
			// was made" log, or append to it (creating one if absent) — the
			// one-call agent flow after a deploy.
			r.Get("/{id}/chat", api.GetSiteChat)
			r.Post("/{id}/chat", api.AppendSiteChat)

			// Collaboration toggle: "allow non-creators to modify" (default
			// on). Creator-or-admin only (re-checked in the handler).
			r.Put("/{id}/collab", api.SetSiteCollab)
		})

		// AI website builder: chat sessions whose LLM (via OpenRouter) edits the
		// site in an isolated sandbox, landing results as time-limited preview
		// drafts the user publishes by hand. Messages stream the turn as SSE.
		r.Route("/ai", func(r chi.Router) {
			r.Post("/sessions", api.CreateAISession)
			r.Get("/sessions", api.ListAISessions)
			r.Get("/sessions/{id}", api.GetAISession)
			r.Delete("/sessions/{id}", api.DeleteAISession)
			r.Post("/sessions/{id}/messages", api.PostAIMessage)
			r.Get("/sessions/{id}/events", api.GetAIEvents)
			r.Get("/models", api.ListAIModels)

			// Org memory ("your agent knows your company"): the curation list,
			// manual create, semantic search (shared by the dashboard, the MCP
			// search_memory tool, and the CLI), and admin edit/delete.
			r.Route("/memories", func(r chi.Router) {
				r.Get("/", api.ListMemories)
				r.Post("/", api.CreateMemory)
				r.Post("/search", api.SearchMemories)
				r.Patch("/{id}", api.PatchMemory)
				r.Delete("/{id}", api.DeleteMemory)
			})
		})

		// Org AI settings: the kill switch + spend cap + current-period spend.
		r.Get("/orgs/ai", api.GetAIOrgSettings)
		r.Patch("/orgs/ai", api.PatchAIOrgSettings)

		// Org memory settings: the memory kill switch + row count/cap. Reachable
		// while the flag is off (that's how it gets turned on).
		r.Get("/orgs/memory", api.GetMemorySettings)
		r.Patch("/orgs/memory", api.PatchMemorySettings)

		// Shared chat logs (Share This Session): append-only conversation
		// histories with optional site attachment. The attached log serves as
		// the site's "How this was made" panel under the site's own access
		// tier; unattached logs are an org-internal library. Depth policy:
		// free = rolling last-10 window (prune, never 402), pro = 100 hard
		// cap, business+ unlimited.
		r.Route("/chats", func(r chi.Router) {
			r.Post("/", api.CreateChatLog)
			r.Get("/", api.ListChatLogs)
			r.Get("/{id}", api.GetChatLog)
			// Owner-or-admin (re-checked in the handlers).
			r.Delete("/{id}", api.DeleteChatLog)
			r.Post("/{id}/messages", api.AppendChatMessages)
			r.Get("/{id}/messages", api.ListChatMessages)
			r.Delete("/{id}/messages/{seq}", api.DeleteChatMessage)
			r.Put("/{id}/site", api.SetChatLogSite)
			r.Put("/{id}/panel", api.SetChatLogPanel)
			// Collaboration toggle (creator-or-admin, re-checked in the handler).
			r.Put("/{id}/collab", api.SetChatLogCollab)
		})

		// Org chat-log settings: the kill switch (mirrors /orgs/ai).
		r.Get("/orgs/chat-logs", api.GetChatSettings)
		r.Patch("/orgs/chat-logs", api.PatchChatSettings)

		// Org-wide skill sharing: content-addressed skill uploads (latest-only
		// versions; finalize = publish) organized into admin-curated folders.
		// Uploads reuse the deploy prepare→presigned-PUT→finalize contract.
		r.Route("/skills", func(r chi.Router) {
			r.Post("/", api.CreateSkill)
			r.Get("/", api.ListSkills) // ?q= &folder= &presets=true
			r.Get("/{id}", api.GetSkill)
			// Owner-or-admin (re-checked in the handlers).
			r.Delete("/{id}", api.DeleteSkill)
			r.Post("/{id}/uploads/prepare", api.PrepareSkillUpload)
			r.Post("/{id}/uploads", api.FinalizeSkillUpload)
			r.Put("/{id}/folders", api.SetSkillFolders)
			r.Get("/{id}/files", api.ListSkillFiles)
			r.Get("/{id}/download", api.DownloadSkill)
			// Feed surface for skills (mirrors the site feed endpoints): share/
			// unshare, owner-set title/description, up/down vote, and the comment
			// thread. A skill auto-joins the feed on publish (feed_visible default).
			r.Put("/{id}/feed", api.SetSkillFeedVisibility)
			r.Put("/{id}/feed-meta", api.SetSkillFeedMeta)
			r.Put("/{id}/vote", api.SetSkillVote)
			r.Get("/{id}/comments", api.ListSkillComments)
			r.Post("/{id}/comments", api.AddSkillComment)
			// Collaboration toggle (creator-or-admin, re-checked in the handler).
			r.Put("/{id}/collab", api.SetSkillCollab)
		})

		// Skill folders: reads for every member; curation (create/rename/delete,
		// preset flags) is admin/owner-only, re-checked in the handlers. The
		// bulk download is the "install the whole preset folder" affordance.
		r.Route("/skill-folders", func(r chi.Router) {
			r.Get("/", api.ListSkillFolders)
			r.Post("/", api.CreateSkillFolder)
			r.Patch("/{id}", api.RenameSkillFolder)
			r.Delete("/{id}", api.DeleteSkillFolder)
			r.Post("/{id}/items", api.AddSkillFolderItem)
			r.Delete("/{id}/items/{skillID}", api.RemoveSkillFolderItem)
			r.Patch("/{id}/items/{skillID}", api.SetSkillFolderItemPreset)
			r.Get("/{id}/download", api.DownloadSkillFolder)
		})

		// Org-scoped API keys (SDK / CLI / CI credentials). Management is
		// session-only + admin/owner (re-checked live in requireAdmin, which also
		// refuses keyed callers — a key can never manage keys). The secret is
		// returned only in the create response.
		r.Route("/api-keys", func(r chi.Router) {
			r.Post("/", api.CreateAPIKey)
			r.Get("/", api.ListAPIKeys)
			r.Delete("/{id}", api.RevokeAPIKey)
		})
		// Org API-keys kill switch (admin/owner only, re-checked in the handler;
		// mirrors /orgs/mcp). The read is on GET /orgs/policy.
		r.Patch("/orgs/api-keys", api.SetApiKeysEnabled)

		// Poll a custom domain's verification status (drives the state machine).
		r.Get("/domains/{domainID}/status", api.GetDomainStatus)
		// Remove a custom domain (admin/owner): drops the route + Cloudflare hostname.
		r.Delete("/domains/{domainID}", api.DeleteDomain)
	})

	return r
}

// attributeErrors stamps the request context with the authenticated user's id so
// any exception captured downstream (the slog bridge or errtrack.Recoverer) is
// attributed to that user in error tracking. It runs after middleware.Auth, so
// the verified claims are present; absent claims leave the context untouched (the
// exception is then attributed to "system").
func attributeErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if claims, ok := middleware.ClaimsFromContext(r.Context()); ok && claims != nil {
			if id := claims.UserID(); id != "" {
				r = r.WithContext(errtrack.WithDistinctID(r.Context(), id))
			}
		}
		next.ServeHTTP(w, r)
	})
}
