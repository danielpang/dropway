// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"time"

	"github.com/danielpang/dropway/internal/customdomains"
	"github.com/danielpang/dropway/internal/edgerevoke"
	"github.com/danielpang/dropway/internal/edgetoken"
	"github.com/danielpang/dropway/internal/openrouter"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/storage"
	aipkg "github.com/danielpang/dropway/services/api/internal/ai"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// SiteStore is the data-layer surface the handlers depend on. The concrete
// implementation is *store.Store (tx-per-call over pgx with the SET LOCAL RLS
// context); defining it as an interface keeps the handlers unit-testable with a
// fake (no live database) while the integration test exercises the real Store.
type SiteStore interface {
	// Phase 1.
	EnsureOrgProvisioned(ctx context.Context, t store.Tenant) error
	// OrgSlug returns the org's slug (identity.organization), the org half of the
	// canonical content host (projection.HostForSite) — used to render display URLs.
	OrgSlug(ctx context.Context, t store.Tenant) (string, error)
	CreateSite(ctx context.Context, t store.Tenant, slug, accessMode string) (store.Site, error)
	ListSites(ctx context.Context, t store.Tenant) ([]store.Site, error)
	GetSite(ctx context.Context, t store.Tenant, id string) (store.Site, error)

	// Org feed: ListFeedSites / ListFeedSkills list the active org's non-private
	// sites / skills (newest first, each with vote score / the caller's vote /
	// comment count) — the two halves of the unified feed; SetSiteFeedVisible /
	// SetSkillFeedVisible flip one post's share-to-feed flag (owner/admin);
	// SetSiteFeedMeta sets the owner-facing site title/description shown in the feed.
	ListFeedSites(ctx context.Context, t store.Tenant) ([]store.FeedSite, error)
	ListFeedSkills(ctx context.Context, t store.Tenant) ([]store.FeedSkill, error)
	SetSiteFeedVisible(ctx context.Context, t store.Tenant, siteID string, visible bool) (store.Site, error)
	SetSkillFeedVisible(ctx context.Context, t store.Tenant, skillID string, visible bool) (store.Skill, error)
	SetSiteFeedMeta(ctx context.Context, t store.Tenant, siteID, title, description string) (store.Site, error)

	// Feed post social: a single up/down vote and a single @mention comment thread,
	// polymorphic over the subject (a site or a skill).
	SetPostVote(ctx context.Context, t store.Tenant, subjectType, subjectID string, value int) (score int64, myVote int, err error)
	CreatePostComment(ctx context.Context, t store.Tenant, p store.CreatePostCommentParams) (store.PostComment, error)
	ListPostComments(ctx context.Context, t store.Tenant, subjectType, subjectID string) ([]store.PostComment, error)
	CreateSiteVersion(ctx context.Context, t store.Tenant, p store.CreateSiteVersionParams) (store.SiteVersion, error)
	GetSiteVersion(ctx context.Context, t store.Tenant, id string) (store.SiteVersion, error)
	ListSiteVersions(ctx context.Context, t store.Tenant, siteID string) ([]store.SiteVersion, error)
	Publish(ctx context.Context, t store.Tenant, siteID, versionID string) (store.PublishResult, error)

	// Logical storage (current-version size; NOT deduplicated) for the usage views:
	// per site on the site page, aggregated per user on the members page. The
	// authoritative deduplicated org footprint is OrgStorageBytes.
	SiteStorageBytes(ctx context.Context, t store.Tenant, siteID string) (int64, error)
	ListSiteStorage(ctx context.Context, t store.Tenant) ([]store.SiteStorage, error)

	// Phase 2 — access control & domains.
	SetSiteAccess(ctx context.Context, t store.Tenant, p store.SetAccessParams) (store.PublishResult, error)
	GetSiteAccessPolicy(ctx context.Context, t store.Tenant, siteID string) (store.AccessPolicy, error)
	AddAllowlistEntry(ctx context.Context, t store.Tenant, p store.AddAllowlistEntryParams) (store.AllowlistEntry, error)
	RemoveAllowlistEntry(ctx context.Context, t store.Tenant, siteID, email string) error
	ListAllowlistEntries(ctx context.Context, t store.Tenant, siteID string) ([]store.AllowlistEntry, error)

	GetOrgPolicy(ctx context.Context, t store.Tenant) (store.OrgPolicy, error)
	SetAllowExternalSharing(ctx context.Context, t store.Tenant, enabled bool) (store.ReconcileResult, error)
	SetMcpEnabled(ctx context.Context, t store.Tenant, enabled bool) (store.OrgPolicy, error)

	MemberRole(ctx context.Context, orgID, userID string) (string, error)
	ListMembers(ctx context.Context, orgID string) ([]store.Member, error)
	// PreflightMembers is the members_per_org cap gate (H8); returns a
	// *quota.ExceededError when the org is at/over its member cap.
	PreflightMembers(ctx context.Context, t store.Tenant) error

	// Authz exchange (the /authz mint + password endpoints).
	AuthorizeMint(ctx context.Context, v store.MintViewer, host string) (store.MintDecision, error)
	ResolveForPassword(ctx context.Context, host string) (store.PasswordDecision, string, error)

	// Custom domains.
	// PreflightCustomDomain is the custom-domains entitlement gate; returns a
	// *quota.ExceededError when the org's plan tier doesn't include custom domains
	// (the free tier), so AddDomain can 402 before provisioning at Cloudflare.
	PreflightCustomDomain(ctx context.Context, t store.Tenant) error
	CreateDomain(ctx context.Context, t store.Tenant, p store.CreateDomainParams) (store.Domain, error)
	GetDomain(ctx context.Context, t store.Tenant, id string) (store.Domain, error)
	ListDomainsForSite(ctx context.Context, t store.Tenant, siteID string) ([]store.Domain, error)
	UpdateDomainStatus(ctx context.Context, t store.Tenant, id, verifyStatus, tlsStatus string) (store.MarkDomainVerifiedResult, error)
	DeleteDomain(ctx context.Context, t store.Tenant, id string) (store.DeleteDomainResult, error)

	// Global host registry (canonical + verified custom hosts) for a site — used to
	// rewrite EVERY route on an access/policy change (FIX 1).
	ListHostRoutesForSite(ctx context.Context, t store.Tenant, siteID string) ([]store.HostRoute, error)

	// Version previews: time-limited hosts pinned to one draft version. Create
	// also renews/extends; the returned route is projected by the handler.
	CreatePreviewRoute(ctx context.Context, t store.Tenant, siteID, versionID string, ttl time.Duration) (store.PreviewResult, error)
	DeletePreviewRoutes(ctx context.Context, t store.Tenant, siteID, versionID string) ([]string, error)

	// AI builder: sessions, transcript, cost/spend, and the org AI settings.
	StartAISession(ctx context.Context, t store.Tenant, siteID, model string, baseVersionID *string, maxConcurrent int) (store.AISession, error)
	GetAISession(ctx context.Context, t store.Tenant, id string) (store.AISession, error)
	// TryBeginAITurn atomically claims a session for a turn (rejects a second
	// concurrent turn); claimed=false means a turn is already running.
	TryBeginAITurn(ctx context.Context, t store.Tenant, id string) (claimed bool, err error)
	SetAISessionStatus(ctx context.Context, t store.Tenant, id, status string) error
	ListAISessionsForSite(ctx context.Context, t store.Tenant, siteID string) ([]store.AISession, error)
	DeleteAISession(ctx context.Context, t store.Tenant, id string) error
	ListAIMessages(ctx context.Context, t store.Tenant, sessionID string, afterSeq int32) ([]store.AIMessage, error)
	AISpendSince(ctx context.Context, t store.Tenant, since time.Time) (float64, error)
	ListAIUsage(ctx context.Context, t store.Tenant, since time.Time, limit int32) ([]store.AIUsageRow, error)
	GetAISettings(ctx context.Context, t store.Tenant) (store.AISettings, error)
	SetAIEnabled(ctx context.Context, t store.Tenant, enabled bool) error
	SetAIMonthlyCap(ctx context.Context, t store.Tenant, capUSD float64) error

	// Phase 4 — audit logging.
	WriteAudit(ctx context.Context, t store.Tenant, rec store.AuditRecord) (store.AuditEntry, error)
	ListAudit(ctx context.Context, t store.Tenant, p store.ListAuditParams) ([]store.AuditEntry, error)

	// Org-wide skill sharing: skills (content-addressed uploads, latest-only
	// versions) + admin-curated folders with preset flags + lazy per-org seeding.
	CreateSkill(ctx context.Context, t store.Tenant, slug, title string, folderIDs []string) (store.Skill, error)
	ListSkills(ctx context.Context, t store.Tenant, q, folderSlug string, presetsOnly bool) ([]store.Skill, error)
	GetSkill(ctx context.Context, t store.Tenant, id string) (store.Skill, error)
	DeleteSkill(ctx context.Context, t store.Tenant, id string) error
	SetSkillMeta(ctx context.Context, t store.Tenant, id, title, description string) (store.Skill, error)
	SetSkillFolders(ctx context.Context, t store.Tenant, skillID string, folderIDs []string) (store.Skill, error)
	CreateSkillVersion(ctx context.Context, t store.Tenant, p store.CreateSkillVersionParams) (store.SkillVersion, error)
	// PublishSkillVersion flips a skill's current version AFTER its manifest is
	// written (the GC-safe ordering).
	PublishSkillVersion(ctx context.Context, t store.Tenant, skillID, versionID string) error
	ListSkillFolders(ctx context.Context, t store.Tenant) ([]store.SkillFolder, error)
	GetSkillFolder(ctx context.Context, t store.Tenant, id string) (store.SkillFolder, error)
	CreateSkillFolder(ctx context.Context, t store.Tenant, slug, title string) (store.SkillFolder, error)
	RenameSkillFolder(ctx context.Context, t store.Tenant, id, title string) (store.SkillFolder, error)
	DeleteSkillFolder(ctx context.Context, t store.Tenant, id string) error
	AddSkillToFolder(ctx context.Context, t store.Tenant, folderID, skillID string, isPreset bool) error
	RemoveSkillFromFolder(ctx context.Context, t store.Tenant, folderID, skillID string) error
	SetSkillFolderItemPreset(ctx context.Context, t store.Tenant, folderID, skillID string, isPreset bool) error
	ListFolderSkills(ctx context.Context, t store.Tenant, folderID string) ([]store.Skill, error)
	SkillsSeeded(ctx context.Context, t store.Tenant) (bool, error)
	// Lazy preset seeding, split so manifests are written between staging and
	// publishing (GC-safe): stage materializes rows without flipping live
	// pointers, publish flips them + marks the org seeded.
	SeedOrgSkillsStage(ctx context.Context, t store.Tenant, seeds []store.SkillSeed) ([]store.SeededSkill, bool, error)
	SeedOrgSkillsPublish(ctx context.Context, t store.Tenant, created []store.SeededSkill) error
}

// EdgeRevoker writes the hard-revocation denylist the serving Worker + /authz read
// (projection.Revoker). The concrete impl is the same KV writer as the route
// projection (the "revoked:" prefix); it may be nil in a DB-less/dev deployment, in
// which case the short edge-token TTL is the only revocation backstop.
type EdgeRevoker = projection.Revoker

// EdgeRevocationReader READS the hard-revocation denylist so the /authz mint path
// can refuse to issue a fresh edge token to a viewer whose JWT predates a hard
// revocation of the user/site/org (H2). The edge denylist alone can't stop a
// re-mint — a freshly minted edge token's iat always post-dates min_iat — so the
// mint compares the VIEWER'S JWT iat to min_iat (mirroring the edge's predicate).
// Optional: nil → the check is skipped (the short edge-token TTL + the live
// membership/allowlist re-checks remain). The same KV reader as the route
// projection implements it (CloudflareKV / Local).
type EdgeRevocationReader interface {
	LookupRevoked(ctx context.Context, kind edgerevoke.Kind, id string) (edgerevoke.Value, bool, error)
}

// Ensure the concrete store satisfies the handler surface.
var _ SiteStore = (*store.Store)(nil)

// ObjectStore is the blob/manifest surface (storage.Store).
type ObjectStore = storage.Store

// ProjectionWriter is the edge-projection surface (projection.Writer).
type ProjectionWriter = projection.Writer

// EdgeSigner mints the host-scoped edge token (the /authz exchange) and exposes the
// JWKS the Worker verifies against. The concrete type is *edgetoken.Signer.
type EdgeSigner interface {
	Mint(p edgetoken.MintParams) (string, error)
	JWKSJSON() ([]byte, error)
}

// Ensure the concrete signer satisfies the surface.
var _ EdgeSigner = (*edgetoken.Signer)(nil)

// DomainProvider is the Cloudflare-for-SaaS custom-hostname surface
// (customdomains.Provider).
type DomainProvider = customdomains.Provider

// AIGate decides whether an org may use the AI builder beyond the org-level
// ai_enabled switch (the cloud build gates it on a paid plan with a card on
// file; OSS allows all). Reason is a short machine code the dashboard maps to a
// message (e.g. "plan_required"). The open default (aiGateAllowAll) allows all.
type AIGate interface {
	AllowAI(ctx context.Context, t store.Tenant) (allowed bool, reason string, err error)
}

// aiGateAllowAll is the open-core default: AI is allowed for any org (self-host
// with a BYO OpenRouter key). The cloud build swaps in a plan-tier gate.
type aiGateAllowAll struct{}

func (aiGateAllowAll) AllowAI(context.Context, store.Tenant) (bool, string, error) {
	return true, "", nil
}

// AIModelCatalog fetches the OpenRouter model catalog for the picker. The
// concrete impl is *openrouter.Client; nil means the models endpoint 503s.
type AIModelCatalog interface {
	Models(ctx context.Context) ([]openrouter.Model, error)
}

// AITurnRunner runs one builder turn, streaming events to emit. The concrete
// impl is *ai.Runner; defining it as an interface keeps the handlers testable
// with a scripted fake (no OpenRouter, no sandbox).
type AITurnRunner interface {
	RunTurn(ctx context.Context, t store.Tenant, sess store.AISession, userText string, previewTTL time.Duration, contentURL aipkg.ContentURL) error
}
