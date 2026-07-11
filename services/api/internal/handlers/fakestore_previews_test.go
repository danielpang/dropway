package handlers

import (
	"context"
	"time"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// Preview fake state: versionID → registered preview hosts, in a sidecar
// registry like p2State (the fakeStore struct lives in handlers_test.go).
type previewState struct {
	hosts map[string][]string // versionID → hosts
}

var previewRegistry = map[*fakeStore]*previewState{}

func (f *fakeStore) previews() *previewState {
	s, ok := previewRegistry[f]
	if !ok {
		s = &previewState{hosts: map[string][]string{}}
		previewRegistry[f] = s
	}
	return s
}

func (f *fakeStore) CreatePreviewRoute(_ context.Context, t store.Tenant, siteID, versionID string, ttl time.Duration) (store.PreviewResult, error) {
	f.lastTenant = t
	site, ok := f.sites[siteID]
	if !ok || site.OrgID != t.OrgID {
		return store.PreviewResult{}, store.ErrNotFound
	}
	ver, ok := f.versions[versionID]
	if !ok {
		return store.PreviewResult{}, store.ErrNotFound
	}
	if ver.SiteID != siteID {
		return store.PreviewResult{}, store.ErrVersionMismatch
	}
	orgSlug := f.orgSlug
	if orgSlug == "" {
		orgSlug = "org"
	}
	host := projection.PreviewHostForSite(versionID, orgSlug, site.Slug)
	expires := time.Now().UTC().Add(ttl)
	ps := f.previews()
	found := false
	for _, h := range ps.hosts[versionID] {
		if h == host {
			found = true
		}
	}
	if !found {
		ps.hosts[versionID] = append(ps.hosts[versionID], host)
	}
	exp := expires
	ver.PreviewExpiresAt = &exp
	f.versions[versionID] = ver
	return store.PreviewResult{
		Host:      host,
		ExpiresAt: expires,
		Route: projection.RouteValue{
			OrgID: t.OrgID, SiteID: siteID, VersionID: versionID,
			AccessMode: site.AccessMode, SchemaVersion: projection.SchemaVersion,
			ExpiresAt: expires.Format(time.RFC3339),
		},
	}, nil
}

func (f *fakeStore) DeletePreviewRoutes(_ context.Context, t store.Tenant, siteID, versionID string) ([]string, error) {
	f.lastTenant = t
	site, ok := f.sites[siteID]
	if !ok || site.OrgID != t.OrgID {
		return nil, store.ErrNotFound
	}
	ver, ok := f.versions[versionID]
	if !ok {
		return nil, store.ErrNotFound
	}
	if ver.SiteID != siteID {
		return nil, store.ErrVersionMismatch
	}
	ps := f.previews()
	hosts := ps.hosts[versionID]
	delete(ps.hosts, versionID)
	ver.PreviewExpiresAt = nil
	f.versions[versionID] = ver
	return hosts, nil
}
