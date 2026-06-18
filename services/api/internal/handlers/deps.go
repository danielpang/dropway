// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"

	"github.com/danielpang/dropway/internal/customdomains"
	"github.com/danielpang/dropway/internal/edgerevoke"
	"github.com/danielpang/dropway/internal/edgetoken"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/storage"
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
	CreateDomain(ctx context.Context, t store.Tenant, p store.CreateDomainParams) (store.Domain, error)
	GetDomain(ctx context.Context, t store.Tenant, id string) (store.Domain, error)
	ListDomainsForSite(ctx context.Context, t store.Tenant, siteID string) ([]store.Domain, error)
	UpdateDomainStatus(ctx context.Context, t store.Tenant, id, verifyStatus, tlsStatus string) (store.MarkDomainVerifiedResult, error)
	DeleteDomain(ctx context.Context, t store.Tenant, id string) (store.DeleteDomainResult, error)

	// Global host registry (canonical + verified custom hosts) for a site — used to
	// rewrite EVERY route on an access/policy change (FIX 1).
	ListHostRoutesForSite(ctx context.Context, t store.Tenant, siteID string) ([]store.HostRoute, error)

	// Phase 4 — audit logging.
	WriteAudit(ctx context.Context, t store.Tenant, rec store.AuditRecord) (store.AuditEntry, error)
	ListAudit(ctx context.Context, t store.Tenant, p store.ListAuditParams) ([]store.AuditEntry, error)
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
