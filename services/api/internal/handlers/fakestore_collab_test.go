package handlers

import (
	"context"

	"github.com/danielpang/dropway/services/api/internal/store"
)

// Collaboration-toggle fakes: flip the flag on the in-memory rows so the
// handler gates (requireSiteEditor and friends) exercise both branches.

func (f *fakeStore) SetSiteAllowMemberEdits(_ context.Context, t store.Tenant, siteID string, allow bool) (store.Site, error) {
	f.lastTenant = t
	s, ok := f.sites[siteID]
	if !ok || s.OrgID != t.OrgID {
		return store.Site{}, store.ErrNotFound
	}
	s.AllowMemberEdits = allow
	f.sites[siteID] = s
	return s, nil
}

func (f *fakeStore) SetSkillAllowMemberEdits(_ context.Context, t store.Tenant, skillID string, allow bool) (store.Skill, error) {
	f.lastTenant = t
	sk := f.sk()
	s, ok := sk.skills[skillID]
	if !ok || s.OrgID != t.OrgID {
		return store.Skill{}, store.ErrNotFound
	}
	s.AllowMemberEdits = allow
	sk.skills[skillID] = s
	return f.decorated(s), nil
}

func (f *fakeStore) SetChatLogAllowMemberEdits(_ context.Context, t store.Tenant, logID string, allow bool) (store.ChatLog, error) {
	f.lastTenant = t
	ch := f.ch()
	l, ok := ch.logs[logID]
	if !ok || l.OrgID != t.OrgID {
		return store.ChatLog{}, store.ErrNotFound
	}
	l.AllowMemberEdits = allow
	ch.logs[logID] = l
	return l, nil
}
