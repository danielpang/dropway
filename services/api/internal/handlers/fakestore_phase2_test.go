package handlers

import (
	"context"
	"time"

	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/services/api/internal/store"
)

// This file extends the unit-test fakeStore (handlers_test.go) with the Phase-2
// SiteStore surface so the existing handler tests keep compiling and the new
// access-control / authz / domain handler tests have an in-memory backend.
//
// The fakeStore's Phase-2 state lives in fields added here via an embedded helper
// the tests populate; to avoid editing the original struct we keep the extra state
// in package-level maps keyed by the fakeStore pointer is overkill, so instead we
// add the methods on *fakeStore and store Phase-2 data on new exported-to-package
// fields declared in this file by re-opening the struct is impossible in Go — so
// we keep the state on a sidecar map. Simpler: the fakeStore already holds sites;
// we add the rest as new fields by declaring them on the struct in handlers_test.go
// is not editable here. We therefore use a small registry.

// p2 holds the Phase-2 fake state for a fakeStore instance.
type p2State struct {
	policies   map[string]store.AccessPolicy
	allowlist  map[string][]store.AllowlistEntry // siteID → entries
	members    map[string]string                 // userID → role
	memberErr  error
	orgPolicy  bool                         // allow_external_sharing
	domains    map[string]store.Domain      // domainID → domain
	hostRoutes map[string][]store.HostRoute // siteID → registered hosts (canonical + custom)
	mintFn     func(v store.MintViewer, host string) (store.MintDecision, error)
	passwordFn func(host string) (store.PasswordDecision, string, error)
	reconcile  store.ReconcileResult

	// Phase 4 — captured audit rows + an optional injected error.
	audit    []store.AuditEntry
	auditErr error
}

var p2registry = map[*fakeStore]*p2State{}

func (f *fakeStore) p2() *p2State {
	s, ok := p2registry[f]
	if !ok {
		s = &p2State{
			policies:   map[string]store.AccessPolicy{},
			allowlist:  map[string][]store.AllowlistEntry{},
			members:    map[string]string{},
			domains:    map[string]store.Domain{},
			hostRoutes: map[string][]store.HostRoute{},
		}
		p2registry[f] = s
	}
	return s
}

// hostRoutesForSite returns the site's registered hosts, defaulting to just the
// canonical <slug>.shippedusercontent.com host when none were explicitly seeded.
func (f *fakeStore) hostRoutesForSite(t store.Tenant, s store.Site) []store.HostRoute {
	if hr, ok := f.p2().hostRoutes[s.ID]; ok {
		return hr
	}
	return []store.HostRoute{{Host: s.Slug + ".shippedusercontent.com", OrgID: t.OrgID, SiteID: s.ID}}
}

func (f *fakeStore) ListHostRoutesForSite(_ context.Context, t store.Tenant, siteID string) ([]store.HostRoute, error) {
	s, ok := f.sites[siteID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return f.hostRoutesForSite(t, s), nil
}

func (f *fakeStore) SetSiteAccess(_ context.Context, t store.Tenant, p store.SetAccessParams) (store.PublishResult, error) {
	f.lastTenant = t
	s, ok := f.sites[p.SiteID]
	if !ok {
		return store.PublishResult{}, store.ErrNotFound
	}
	if p.Mode == projection.AccessPublic && !f.p2().orgPolicy {
		return store.PublishResult{}, store.ErrExternalSharingDisabled
	}
	s.AccessMode = p.Mode
	f.sites[p.SiteID] = s
	pol := store.AccessPolicy{SiteID: p.SiteID, OrgID: t.OrgID, Mode: p.Mode, PasswordHash: p.PasswordHash, ExpiresAt: p.ExpiresAt, Unlisted: p.Unlisted}
	f.p2().policies[p.SiteID] = pol
	res := store.PublishResult{Site: s}
	if s.CurrentVersionID != nil {
		exp := ""
		if p.Mode == projection.AccessPublic && p.ExpiresAt != nil {
			exp = p.ExpiresAt.UTC().Format(time.RFC3339)
		}
		// Rewrite EVERY host of the site (canonical + custom) — FIX 1.
		for _, hr := range f.hostRoutesForSite(t, s) {
			rv := projection.RouteValue{
				OrgID: t.OrgID, SiteID: p.SiteID, VersionID: *s.CurrentVersionID,
				AccessMode: p.Mode, SchemaVersion: projection.SchemaVersion, ExpiresAt: exp,
			}
			res.Routes = append(res.Routes, store.RouteUpdate{Host: hr.Host, Route: rv})
		}
		res.Host = s.Slug + ".shippedusercontent.com"
		res.Route = projection.RouteValue{
			OrgID: t.OrgID, SiteID: p.SiteID, VersionID: *s.CurrentVersionID,
			AccessMode: p.Mode, SchemaVersion: projection.SchemaVersion, ExpiresAt: exp,
		}
	}
	return res, nil
}

func (f *fakeStore) GetSiteAccessPolicy(_ context.Context, t store.Tenant, siteID string) (store.AccessPolicy, error) {
	p, ok := f.p2().policies[siteID]
	if !ok {
		return store.AccessPolicy{}, store.ErrNoPolicy
	}
	return p, nil
}

func (f *fakeStore) AddAllowlistEntry(_ context.Context, t store.Tenant, p store.AddAllowlistEntryParams) (store.AllowlistEntry, error) {
	f.lastTenant = t
	if _, ok := f.sites[p.SiteID]; !ok {
		return store.AllowlistEntry{}, store.ErrNotFound
	}
	if p.IsExternal && !f.p2().orgPolicy {
		return store.AllowlistEntry{}, store.ErrExternalSharingDisabled
	}
	e := store.AllowlistEntry{OrgID: t.OrgID, SiteID: p.SiteID, Email: p.Email, IsExternal: p.IsExternal}
	f.p2().allowlist[p.SiteID] = append(f.p2().allowlist[p.SiteID], e)
	return e, nil
}

func (f *fakeStore) RemoveAllowlistEntry(_ context.Context, t store.Tenant, siteID, email string) error {
	if _, ok := f.sites[siteID]; !ok {
		return store.ErrNotFound
	}
	cur := f.p2().allowlist[siteID]
	out := cur[:0]
	for _, e := range cur {
		if e.Email != email {
			out = append(out, e)
		}
	}
	f.p2().allowlist[siteID] = out
	return nil
}

func (f *fakeStore) ListAllowlistEntries(_ context.Context, t store.Tenant, siteID string) ([]store.AllowlistEntry, error) {
	if _, ok := f.sites[siteID]; !ok {
		return nil, store.ErrNotFound
	}
	return f.p2().allowlist[siteID], nil
}

func (f *fakeStore) GetOrgPolicy(_ context.Context, t store.Tenant) (store.OrgPolicy, error) {
	return store.OrgPolicy{OrgID: t.OrgID, AllowExternalSharing: f.p2().orgPolicy}, nil
}

func (f *fakeStore) SetAllowExternalSharing(_ context.Context, t store.Tenant, enabled bool) (store.ReconcileResult, error) {
	f.lastTenant = t
	f.p2().orgPolicy = enabled
	res := f.p2().reconcile
	res.AllowExternalSharing = enabled
	return res, nil
}

func (f *fakeStore) MemberRole(_ context.Context, orgID, userID string) (string, error) {
	if f.p2().memberErr != nil {
		return "", f.p2().memberErr
	}
	role, ok := f.p2().members[userID]
	if !ok {
		return "", store.ErrNoMembership
	}
	return role, nil
}

func (f *fakeStore) ListMembers(_ context.Context, orgID string) ([]store.Member, error) {
	if f.p2().memberErr != nil {
		return nil, f.p2().memberErr
	}
	var out []store.Member
	for uid, role := range f.p2().members {
		out = append(out, store.Member{UserID: uid, OrgID: orgID, Role: role})
	}
	return out, nil
}

func (f *fakeStore) AuthorizeMint(_ context.Context, v store.MintViewer, host string) (store.MintDecision, error) {
	if f.p2().mintFn != nil {
		return f.p2().mintFn(v, host)
	}
	return store.MintDecision{}, store.ErrHostNotFound
}

func (f *fakeStore) ResolveForPassword(_ context.Context, host string) (store.PasswordDecision, string, error) {
	if f.p2().passwordFn != nil {
		return f.p2().passwordFn(host)
	}
	return store.PasswordDecision{}, "", store.ErrHostNotFound
}

func (f *fakeStore) CreateDomain(_ context.Context, t store.Tenant, p store.CreateDomainParams) (store.Domain, error) {
	f.lastTenant = t
	if _, ok := f.sites[p.SiteID]; !ok {
		return store.Domain{}, store.ErrNotFound
	}
	for _, d := range f.p2().domains {
		if d.Hostname == p.Hostname {
			return store.Domain{}, store.ErrHostTaken
		}
	}
	d := store.Domain{
		ID: "dom_" + p.Hostname, OrgID: t.OrgID, SiteID: p.SiteID, Hostname: p.Hostname,
		VerifyStatus: store.DomainPending, TLSStatus: store.TLSPending,
		CFHostnameID: p.CFHostnameID, DCVRecord: p.DCVRecord,
	}
	f.p2().domains[d.ID] = d
	return d, nil
}

func (f *fakeStore) GetDomain(_ context.Context, t store.Tenant, id string) (store.Domain, error) {
	d, ok := f.p2().domains[id]
	if !ok {
		return store.Domain{}, store.ErrNotFound
	}
	return d, nil
}

func (f *fakeStore) ListDomainsForSite(_ context.Context, t store.Tenant, siteID string) ([]store.Domain, error) {
	if _, ok := f.sites[siteID]; !ok {
		return nil, store.ErrNotFound
	}
	var out []store.Domain
	for _, d := range f.p2().domains {
		if d.SiteID == siteID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateDomainStatus(_ context.Context, t store.Tenant, id, verifyStatus, tlsStatus string) (store.MarkDomainVerifiedResult, error) {
	d, ok := f.p2().domains[id]
	if !ok {
		return store.MarkDomainVerifiedResult{}, store.ErrNotFound
	}
	d.VerifyStatus = verifyStatus
	d.TLSStatus = tlsStatus
	f.p2().domains[id] = d
	res := store.MarkDomainVerifiedResult{Domain: d}
	if verifyStatus == store.DomainVerified && tlsStatus == store.TLSIssued {
		res.Registered = true
		res.Host = d.Hostname
		// Register the verified custom host in the global registry (alongside the
		// canonical host) so a later access change rewrites it too (FIX 1).
		if s, ok := f.sites[d.SiteID]; ok {
			hosts := f.hostRoutesForSite(t, s)
			already := false
			for _, hr := range hosts {
				if hr.Host == d.Hostname {
					already = true
				}
			}
			if !already {
				hosts = append(hosts, store.HostRoute{Host: d.Hostname, OrgID: t.OrgID, SiteID: d.SiteID})
			}
			f.p2().hostRoutes[d.SiteID] = hosts
			if s.CurrentVersionID != nil {
				res.Route = projection.RouteValue{
					OrgID: t.OrgID, SiteID: d.SiteID, VersionID: *s.CurrentVersionID,
					AccessMode: s.AccessMode, SchemaVersion: projection.SchemaVersion,
				}
			}
		}
	}
	return res, nil
}
