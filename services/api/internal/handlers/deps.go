// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"

	"github.com/danielpang/shipped/internal/customdomains"
	"github.com/danielpang/shipped/internal/edgetoken"
	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/storage"
	"github.com/danielpang/shipped/services/api/internal/store"
)

// SiteStore is the data-layer surface the handlers depend on. The concrete
// implementation is *store.Store (tx-per-call over pgx with the SET LOCAL RLS
// context); defining it as an interface keeps the handlers unit-testable with a
// fake (no live database) while the integration test exercises the real Store.
type SiteStore interface {
	// Phase 1.
	EnsureOrgProvisioned(ctx context.Context, t store.Tenant) error
	CreateSite(ctx context.Context, t store.Tenant, slug, accessMode string) (store.Site, error)
	ListSites(ctx context.Context, t store.Tenant) ([]store.Site, error)
	GetSite(ctx context.Context, t store.Tenant, id string) (store.Site, error)
	CreateSiteVersion(ctx context.Context, t store.Tenant, p store.CreateSiteVersionParams) (store.SiteVersion, error)
	GetSiteVersion(ctx context.Context, t store.Tenant, id string) (store.SiteVersion, error)
	Publish(ctx context.Context, t store.Tenant, siteID, versionID string) (store.PublishResult, error)

	// Phase 2 — access control & domains.
	SetSiteAccess(ctx context.Context, t store.Tenant, p store.SetAccessParams) (store.PublishResult, error)
	GetSiteAccessPolicy(ctx context.Context, t store.Tenant, siteID string) (store.AccessPolicy, error)
	AddAllowlistEntry(ctx context.Context, t store.Tenant, p store.AddAllowlistEntryParams) (store.AllowlistEntry, error)
	RemoveAllowlistEntry(ctx context.Context, t store.Tenant, siteID, email string) error
	ListAllowlistEntries(ctx context.Context, t store.Tenant, siteID string) ([]store.AllowlistEntry, error)

	GetOrgPolicy(ctx context.Context, t store.Tenant) (store.OrgPolicy, error)
	SetAllowExternalSharing(ctx context.Context, t store.Tenant, enabled bool) (store.ReconcileResult, error)

	MemberRole(ctx context.Context, orgID, userID string) (string, error)
	ListMembers(ctx context.Context, orgID string) ([]store.Member, error)

	// Authz exchange (the /authz mint + password endpoints).
	AuthorizeMint(ctx context.Context, v store.MintViewer, host string) (store.MintDecision, error)
	ResolveForPassword(ctx context.Context, host string) (store.PasswordDecision, string, error)

	// Custom domains.
	CreateDomain(ctx context.Context, t store.Tenant, p store.CreateDomainParams) (store.Domain, error)
	GetDomain(ctx context.Context, t store.Tenant, id string) (store.Domain, error)
	ListDomainsForSite(ctx context.Context, t store.Tenant, siteID string) ([]store.Domain, error)
	UpdateDomainStatus(ctx context.Context, t store.Tenant, id, verifyStatus, tlsStatus string) (store.MarkDomainVerifiedResult, error)

	// Global host registry (canonical + verified custom hosts) for a site — used to
	// rewrite EVERY route on an access/policy change (FIX 1).
	ListHostRoutesForSite(ctx context.Context, t store.Tenant, siteID string) ([]store.HostRoute, error)
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
