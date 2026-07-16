// Package audit defines the small, dependency-light vocabulary for Dropway's
// audit trail: the canonical Action constants for sensitive mutations and a
// request-provenance Context the Go API attaches to every audit row.
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
	// ActionPreviewCreate records a version-preview host created/renewed
	// (POST /v1/sites/{id}/versions/{versionID}/preview).
	ActionPreviewCreate Action = "deploy.preview_create"
	// ActionPreviewDelete records a version-preview host removed
	// (DELETE /v1/sites/{id}/versions/{versionID}/preview).
	ActionPreviewDelete Action = "deploy.preview_delete"
	// ActionAISessionStart records a new AI builder session
	// (POST /v1/ai/sessions).
	ActionAISessionStart Action = "ai.session_start"
	// ActionAISettings records a change to the org AI kill switch / spend cap
	// (PATCH /v1/orgs/ai).
	ActionAISettings Action = "ai.settings"
	// ActionDomainAdd records a custom domain registered
	// (POST /v1/sites/{id}/domains).
	ActionDomainAdd Action = "domain.add"
	// ActionDomainVerify records a custom domain reaching verified+TLS
	// (GET /v1/domains/{id}/status that transitions it).
	ActionDomainVerify Action = "domain.verify"
	// ActionDomainRemove records a custom domain removed
	// (DELETE /v1/domains/{id}).
	ActionDomainRemove Action = "domain.remove"
	// ActionAllowExternalSharing records the org allow_external_sharing toggle
	// (PUT /v1/orgs/allow-external-sharing).
	ActionAllowExternalSharing Action = "org.allow_external_sharing"
	// ActionMcpToggle records the org mcp_enabled toggle (PATCH /v1/orgs/mcp) —
	// enabling/disabling the Dropway MCP server for the org.
	ActionMcpToggle Action = "org.mcp_toggle"
	// ActionMemberRevoke records an admin revoking a member's edge tokens /
	// removing them (POST /v1/members/{userId}/revoke).
	ActionMemberRevoke Action = "member.revoke"
	// ActionMemberInvite records an admin/owner sending an org invitation
	// (POST /v1/members/invites). Better Auth owns the invitation row + email; the
	// dashboard records this after the invite is created so the trail captures who
	// invited whom, with what role.
	ActionMemberInvite Action = "member.invite"
	// ActionMemberJoin records a user accepting an invitation and joining the org
	// (POST /v1/members/joined). Better Auth owns the membership row; the dashboard
	// records this after the join so the trail captures the new member.
	ActionMemberJoin Action = "member.join"
	// ActionSiteRevokeAccess records an admin force-revoking a site's edge tokens
	// (POST /v1/sites/{id}/revoke-access) or a site unshare/tighten.
	ActionSiteRevokeAccess Action = "site.revoke_access"
	// ActionSiteFeedVisibility records a change to a site's org-feed visibility
	// (PUT /v1/sites/{id}/feed) — sharing it to the org feed or making it private.
	ActionSiteFeedVisibility Action = "site.feed_visibility"
	// ActionSiteFeedMeta records a change to a site's feed title/description
	// (PUT /v1/sites/{id}/feed-meta).
	ActionSiteFeedMeta Action = "site.feed_meta"
	// ActionSkillCreate records a new shared skill (POST /v1/skills).
	ActionSkillCreate Action = "skill.create"
	// ActionSkillUpload records a finalized skill version
	// (POST /v1/skills/{id}/uploads) — in the latest-only model this is also
	// the publish.
	ActionSkillUpload Action = "skill.upload"
	// ActionSkillDelete records a skill removed (DELETE /v1/skills/{id}).
	ActionSkillDelete Action = "skill.delete"
	// ActionSkillDownload records a skill (or a whole folder) downloaded.
	ActionSkillDownload Action = "skill.download"
	// ActionSkillFolderChange records a skill's folder memberships changing
	// (PUT /v1/skills/{id}/folders, or add/remove on /v1/skill-folders items).
	ActionSkillFolderChange Action = "skill.folder_change"
	// ActionSkillFolderCreate / Rename / Delete record admin folder curation.
	ActionSkillFolderCreate Action = "skill_folder.create"
	ActionSkillFolderRename Action = "skill_folder.rename"
	ActionSkillFolderDelete Action = "skill_folder.delete"
	// ActionSkillFolderPresetChange records a preset flag flip on a folder item.
	ActionSkillFolderPresetChange Action = "skill_folder.preset_change"
	// ActionSkillFeedVisibility records a change to a skill's org-feed visibility
	// (PUT /v1/skills/{id}/feed).
	ActionSkillFeedVisibility Action = "skill.feed_visibility"
	// ActionSkillFeedMeta records a change to a skill's feed title/description
	// (PUT /v1/skills/{id}/feed-meta).
	ActionSkillFeedMeta Action = "skill.feed_meta"
	// ActionChatLogCreate records a new shared chat log (POST /v1/chats).
	ActionChatLogCreate Action = "chatlog.create"
	// ActionChatLogDelete records a chat log removed (DELETE /v1/chats/{id}).
	ActionChatLogDelete Action = "chatlog.delete"
	// ActionChatLogAppend records messages appended/imported to a chat log
	// (POST /v1/chats/{id}/messages or /v1/sites/{id}/chat).
	ActionChatLogAppend Action = "chatlog.append"
	// ActionChatLogMessageDelete records one message removed by seq
	// (DELETE /v1/chats/{id}/messages/{seq}) — the pasted-secret escape hatch.
	ActionChatLogMessageDelete Action = "chatlog.message_delete"
	// ActionChatLogAttach records an attach/detach/move of a log's site binding
	// (PUT /v1/chats/{id}/site).
	ActionChatLogAttach Action = "chatlog.attach"
	// ActionChatLogPanel records the served-panel flag flip
	// (PUT /v1/chats/{id}/panel).
	ActionChatLogPanel Action = "chatlog.panel"
	// ActionChatLogSettings records the org chat-log kill switch flip
	// (PATCH /v1/orgs/chat-logs).
	ActionChatLogSettings Action = "chatlog.settings"
	// ActionSiteCollab / ActionSkillCollab / ActionChatLogCollab record a flip
	// of a resource's "allow non-creators to modify" collaboration toggle
	// (PUT /v1/{sites|skills|chats}/{id}/collab).
	ActionSiteCollab    Action = "site.collab"
	ActionSkillCollab   Action = "skill.collab"
	ActionChatLogCollab Action = "chatlog.collab"
)

// Context carries the request provenance attached to an audit row: who acted,
// from where, and the correlation ids tying the row to the structured access log
// and edge/Worker logs for the same request. All fields
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
