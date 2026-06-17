// Package audit defines the small, dependency-light vocabulary for Dropway's
// audit trail: the canonical Action constants for sensitive mutations and a
// request-provenance Context the Go API attaches to every audit row
// (docs/ARCHITECTURE.md §10 audit logging, §2.3 observability).
//
// The actual row write lives in services/api/internal/store (WriteAudit, in the
// same RLS tenant tx as the action where possible); this package only owns the
// shared types so handlers, the store, and any future consumer agree on the
// action names and the provenance shape without importing the store.
package audit

// Action is the canonical, stable name of a sensitive mutation recorded in the
// audit log. The strings are part of the API contract surfaced by GET /v1/audit
// and consumed by the dashboard audit viewer — do not rename casually.
type Action string

const (
	// ActionSiteCreate records a new site (POST /v1/sites).
	ActionSiteCreate Action = "site.create"
	// ActionSiteAccessChange records an access-mode / policy change
	// (PUT /v1/sites/{id}/access).
	ActionSiteAccessChange Action = "site.access_change"
	// ActionAllowlistAdd records an allowlist grant added
	// (POST /v1/sites/{id}/allowlist).
	ActionAllowlistAdd Action = "site.allowlist_add"
	// ActionAllowlistRemove records an allowlist grant removed
	// (DELETE /v1/sites/{id}/allowlist).
	ActionAllowlistRemove Action = "site.allowlist_remove"
	// ActionDeployFinalize records a finalized immutable version
	// (POST /v1/sites/{id}/deployments).
	ActionDeployFinalize Action = "deploy.finalize"
	// ActionDeployPublish records a publish / rollback pointer flip
	// (POST /v1/sites/{id}/publish).
	ActionDeployPublish Action = "deploy.publish"
	// ActionDomainAdd records a custom domain registered
	// (POST /v1/sites/{id}/domains).
	ActionDomainAdd Action = "domain.add"
	// ActionDomainVerify records a custom domain reaching verified+TLS
	// (GET /v1/domains/{id}/status that transitions it).
	ActionDomainVerify Action = "domain.verify"
	// ActionAllowExternalSharing records the org allow_external_sharing toggle
	// (PUT /v1/orgs/allow-external-sharing).
	ActionAllowExternalSharing Action = "org.allow_external_sharing"
	// ActionMcpToggle records the org mcp_enabled toggle (PATCH /v1/orgs/mcp) —
	// enabling/disabling the Dropway MCP server for the org.
	ActionMcpToggle Action = "org.mcp_toggle"
	// ActionMemberRevoke records an admin revoking a member's edge tokens /
	// removing them (POST /v1/members/{userId}/revoke).
	ActionMemberRevoke Action = "member.revoke"
	// ActionSiteRevokeAccess records an admin force-revoking a site's edge tokens
	// (POST /v1/sites/{id}/revoke-access) or a site unshare/tighten.
	ActionSiteRevokeAccess Action = "site.revoke_access"
)

// Context carries the request provenance attached to an audit row: who acted,
// from where, and the correlation ids tying the row to the structured access log
// and edge/Worker logs for the same request (ARCHITECTURE.md §2.3). All fields
// are optional — an empty field is written as SQL NULL.
type Context struct {
	// ActorUser is the verified Better Auth user id (claims.UserID). Empty for a
	// deploy-token-driven action.
	ActorUser string
	// ActorToken is the deploy-token id (app.deploy_tokens.id) when a deploy token
	// drove the action rather than a user session. Empty otherwise.
	ActorToken string
	// IP is the client IP (from the request RemoteAddr / X-Forwarded-For via chi
	// RealIP). Empty when unknown.
	IP string
	// RequestID is the per-request correlation id (chi RequestID / the propagated
	// X-Request-Id header).
	RequestID string
	// TraceID is an optional distributed-trace id; equals RequestID when no
	// external tracer is wired (a cheap end-to-end tracing hook).
	TraceID string
}
