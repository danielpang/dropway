// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"

	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/storage"
	"github.com/danielpang/shipped/services/api/internal/store"
)

// SiteStore is the data-layer surface the handlers depend on. The concrete
// implementation is *store.Store (tx-per-call over pgx with the SET LOCAL RLS
// context); defining it as an interface keeps the handlers unit-testable with a
// fake (no live database) while the integration test exercises the real Store.
type SiteStore interface {
	EnsureOrgProvisioned(ctx context.Context, t store.Tenant) error
	CreateSite(ctx context.Context, t store.Tenant, slug, accessMode string) (store.Site, error)
	ListSites(ctx context.Context, t store.Tenant) ([]store.Site, error)
	GetSite(ctx context.Context, t store.Tenant, id string) (store.Site, error)
	CreateSiteVersion(ctx context.Context, t store.Tenant, p store.CreateSiteVersionParams) (store.SiteVersion, error)
	GetSiteVersion(ctx context.Context, t store.Tenant, id string) (store.SiteVersion, error)
	Publish(ctx context.Context, t store.Tenant, siteID, versionID string) (store.PublishResult, error)
}

// Ensure the concrete store satisfies the handler surface.
var _ SiteStore = (*store.Store)(nil)

// ObjectStore is the blob/manifest surface (storage.Store).
type ObjectStore = storage.Store

// ProjectionWriter is the edge-projection surface (projection.Writer).
type ProjectionWriter = projection.Writer
