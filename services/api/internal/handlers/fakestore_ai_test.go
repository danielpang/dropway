package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/danielpang/dropway/services/api/internal/store"
)

// AI fake state in a sidecar registry (the fakeStore struct is in
// handlers_test.go). Enough to exercise the AI handlers without a DB.
type aiState struct {
	sessions   map[string]store.AISession
	messages   map[string][]store.AIMessage // sessionID → transcript
	settings   store.AISettings
	spend      float64
	activeN    int
	concurrErr bool
	nextID     int
}

var aiRegistry = map[*fakeStore]*aiState{}

func (f *fakeStore) ai() *aiState {
	s, ok := aiRegistry[f]
	if !ok {
		s = &aiState{
			sessions: map[string]store.AISession{},
			messages: map[string][]store.AIMessage{},
			settings: store.AISettings{Enabled: true, MonthlyCapUSD: 20},
		}
		aiRegistry[f] = s
	}
	return s
}

func (f *fakeStore) StartAISession(_ context.Context, t store.Tenant, siteID, model string, base *string, maxConcurrent int) (store.AISession, error) {
	st := f.ai()
	if _, ok := f.sites[siteID]; !ok {
		return store.AISession{}, store.ErrNotFound
	}
	if st.concurrErr || (maxConcurrent > 0 && st.activeN >= maxConcurrent) {
		return store.AISession{}, store.ErrAIConcurrencyLimit
	}
	st.nextID++
	id := "aisess_" + time.Now().UTC().Format("150405") + itoa(st.nextID)
	sess := store.AISession{
		ID: id, OrgID: t.OrgID, SiteID: siteID, CreatedBy: t.UserID,
		Status: "active", Model: model, BaseVersionID: base,
		CreatedAt: time.Now(), LastActivityAt: time.Now(),
	}
	st.sessions[id] = sess
	st.activeN++
	return sess, nil
}

func (f *fakeStore) GetAISession(_ context.Context, t store.Tenant, id string) (store.AISession, error) {
	s, ok := f.ai().sessions[id]
	if !ok || s.OrgID != t.OrgID {
		return store.AISession{}, store.ErrNotFound
	}
	return s, nil
}

func (f *fakeStore) TryBeginAITurn(_ context.Context, t store.Tenant, id string) (bool, error) {
	s, ok := f.ai().sessions[id]
	if !ok || s.OrgID != t.OrgID {
		return false, store.ErrNotFound
	}
	if s.Status == "running" {
		return false, nil // already claimed
	}
	s.Status = "running"
	f.ai().sessions[id] = s
	return true, nil
}

func (f *fakeStore) SetAISessionStatus(_ context.Context, t store.Tenant, id, status string) error {
	s, ok := f.ai().sessions[id]
	if !ok || s.OrgID != t.OrgID {
		return store.ErrNotFound
	}
	s.Status = status
	f.ai().sessions[id] = s
	return nil
}

func (f *fakeStore) ListAISessionsForSite(_ context.Context, _ store.Tenant, siteID string) ([]store.AISession, error) {
	var out []store.AISession
	for _, s := range f.ai().sessions {
		if s.SiteID == siteID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteAISession(_ context.Context, t store.Tenant, id string) error {
	s, ok := f.ai().sessions[id]
	if !ok || s.OrgID != t.OrgID {
		return store.ErrNotFound
	}
	delete(f.ai().sessions, id)
	return nil
}

func (f *fakeStore) ListAIMessages(_ context.Context, _ store.Tenant, sessionID string, afterSeq int32) ([]store.AIMessage, error) {
	var out []store.AIMessage
	for _, m := range f.ai().messages[sessionID] {
		if m.Seq > afterSeq {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeStore) AISpendSince(_ context.Context, _ store.Tenant, _ time.Time) (float64, error) {
	return f.ai().spend, nil
}

func (f *fakeStore) ListAIUsage(_ context.Context, _ store.Tenant, _ time.Time, _ int32) ([]store.AIUsageRow, error) {
	return nil, nil
}

func (f *fakeStore) GetAISettings(_ context.Context, _ store.Tenant) (store.AISettings, error) {
	return f.ai().settings, nil
}

func (f *fakeStore) SetAIEnabled(_ context.Context, _ store.Tenant, enabled bool) error {
	st := f.ai()
	st.settings.Enabled = enabled
	return nil
}

func (f *fakeStore) SetAIMonthlyCap(_ context.Context, _ store.Tenant, capUSD float64) error {
	st := f.ai()
	st.settings.MonthlyCapUSD = capUSD
	return nil
}

// seedAIMessage adds a transcript row for tests.
func (f *fakeStore) seedAIMessage(sessionID, role string, content any) {
	st := f.ai()
	body, _ := json.Marshal(content)
	msgs := st.messages[sessionID]
	seq := int32(len(msgs) + 1)
	st.messages[sessionID] = append(msgs, store.AIMessage{
		ID: "msg" + itoa(int(seq)), SessionID: sessionID, Seq: seq, Role: role,
		Content: body, CreatedAt: time.Now(),
	})
}
