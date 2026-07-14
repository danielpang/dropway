package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/danielpang/dropway/internal/chatspec"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// This file extends the unit-test fakeStore with the shared chat-log surface,
// using the same sidecar-registry pattern as the skills state. window/msgCap
// emulate the cloud depth policy (free rolling window / pro hard cap) so both
// paths are testable without the cloud provider.

type chatsState struct {
	logs     map[string]store.ChatLog          // logID → log
	messages map[string][]store.ChatMessage    // logID → rows (ascending seq)
	nextSeq  map[string]int32                  // logID → allocator (monotonic)
	enabled  bool                              // org kill switch
	nextID   int
	// window: >0 → rolling last-N (free semantics: insert then prune).
	window int64
	// msgCap: >0 → hard cap (pro semantics: fits entirely or 402s).
	msgCap int64
}

var chRegistry = map[*fakeStore]*chatsState{}

func (f *fakeStore) ch() *chatsState {
	s, ok := chRegistry[f]
	if !ok {
		s = &chatsState{
			logs:     map[string]store.ChatLog{},
			messages: map[string][]store.ChatMessage{},
			nextSeq:  map[string]int32{},
			enabled:  true,
		}
		chRegistry[f] = s
	}
	return s
}

func (s *chatsState) id() string {
	s.nextID++
	return fmt.Sprintf("chat_%d", s.nextID)
}

func (f *fakeStore) chatBySite(siteID string) (store.ChatLog, bool) {
	for _, l := range f.ch().logs {
		if l.SiteID != nil && *l.SiteID == siteID {
			return l, true
		}
	}
	return store.ChatLog{}, false
}

func (f *fakeStore) CreateChatLog(_ context.Context, t store.Tenant, title, sourceTool string, siteID *string) (store.ChatLog, error) {
	f.lastTenant = t
	ch := f.ch()
	if sourceTool == "" {
		sourceTool = chatspec.SourceOther
	}
	if siteID != nil {
		if _, ok := f.sites[*siteID]; !ok {
			return store.ChatLog{}, store.ErrNotFound
		}
		if _, taken := f.chatBySite(*siteID); taken {
			return store.ChatLog{}, store.ErrSiteHasChatLog
		}
	}
	l := store.ChatLog{
		ID: ch.id(), OrgID: t.OrgID, SiteID: siteID, Title: title,
		SourceTool: sourceTool, PanelEnabled: true, CreatedBy: t.UserID,
	}
	ch.logs[l.ID] = l
	ch.nextSeq[l.ID] = 1
	return l, nil
}

func (f *fakeStore) GetChatLog(_ context.Context, t store.Tenant, id string) (store.ChatLog, error) {
	f.lastTenant = t
	l, ok := f.ch().logs[id]
	if !ok || l.OrgID != t.OrgID {
		return store.ChatLog{}, store.ErrNotFound
	}
	l.MessageCount = int64(len(f.ch().messages[id]))
	return l, nil
}

func (f *fakeStore) GetChatLogForSite(_ context.Context, t store.Tenant, siteID string) (store.ChatLog, error) {
	f.lastTenant = t
	l, ok := f.chatBySite(siteID)
	if !ok || l.OrgID != t.OrgID {
		return store.ChatLog{}, store.ErrNotFound
	}
	l.MessageCount = int64(len(f.ch().messages[l.ID]))
	return l, nil
}

func (f *fakeStore) ListChatLogs(_ context.Context, t store.Tenant) ([]store.ChatLog, error) {
	f.lastTenant = t
	ch := f.ch()
	var out []store.ChatLog
	for _, l := range ch.logs {
		if l.OrgID != t.OrgID {
			continue
		}
		l.MessageCount = int64(len(ch.messages[l.ID]))
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeStore) DeleteChatLog(_ context.Context, t store.Tenant, id string) error {
	f.lastTenant = t
	ch := f.ch()
	l, ok := ch.logs[id]
	if !ok || l.OrgID != t.OrgID {
		return store.ErrNotFound
	}
	delete(ch.logs, id)
	delete(ch.messages, id)
	return nil
}

func (f *fakeStore) SetChatLogSite(_ context.Context, t store.Tenant, id string, siteID *string) (store.ChatLog, error) {
	f.lastTenant = t
	ch := f.ch()
	l, ok := ch.logs[id]
	if !ok || l.OrgID != t.OrgID {
		return store.ChatLog{}, store.ErrNotFound
	}
	if siteID != nil {
		if _, ok := f.sites[*siteID]; !ok {
			return store.ChatLog{}, store.ErrNotFound
		}
		if other, taken := f.chatBySite(*siteID); taken && other.ID != id {
			return store.ChatLog{}, store.ErrSiteHasChatLog
		}
	}
	l.SiteID = siteID
	ch.logs[id] = l
	return l, nil
}

func (f *fakeStore) SetChatLogPanel(_ context.Context, t store.Tenant, id string, enabled bool) (store.ChatLog, error) {
	f.lastTenant = t
	ch := f.ch()
	l, ok := ch.logs[id]
	if !ok || l.OrgID != t.OrgID {
		return store.ChatLog{}, store.ErrNotFound
	}
	l.PanelEnabled = enabled
	ch.logs[id] = l
	return l, nil
}

func (f *fakeStore) AppendChatMessages(_ context.Context, t store.Tenant, logID string, msgs []chatspec.Message) (store.AppendChatResult, error) {
	f.lastTenant = t
	ch := f.ch()
	l, ok := ch.logs[logID]
	if !ok || l.OrgID != t.OrgID {
		return store.AppendChatResult{}, store.ErrNotFound
	}
	if ch.window == 0 && ch.msgCap > 0 {
		current := int64(len(ch.messages[logID]))
		if current+int64(len(msgs)) > ch.msgCap {
			return store.AppendChatResult{}, &quota.ExceededError{
				Limit: quota.ResourceChatMessagePerLog, Current: current,
				Max: ch.msgCap, PlanTier: "pro", NextTier: "business",
			}
		}
	}
	// Version stamp: the attached site's current version (mirrors the store).
	var versionID *string
	if l.SiteID != nil {
		if site, ok := f.sites[*l.SiteID]; ok {
			versionID = site.CurrentVersionID
		}
	}
	var res store.AppendChatResult
	for _, m := range msgs {
		seq := ch.nextSeq[logID]
		ch.nextSeq[logID] = seq + 1
		var meta []byte
		if m.Meta != nil {
			meta, _ = json.Marshal(m.Meta)
		}
		row := store.ChatMessage{
			ID: fmt.Sprintf("%s_m%d", logID, seq), OrgID: t.OrgID, ChatLogID: logID,
			Seq: seq, VersionID: versionID, CreatedBy: t.UserID,
			Role: m.Role, Kind: m.Kind, Content: m.Content, Meta: meta,
		}
		ch.messages[logID] = append(ch.messages[logID], row)
		res.Messages = append(res.Messages, row)
	}
	if ch.window > 0 {
		res.Window = ch.window
		rows := ch.messages[logID]
		if int64(len(rows)) > ch.window {
			res.Pruned = int64(len(rows)) - ch.window
			ch.messages[logID] = append([]store.ChatMessage(nil), rows[len(rows)-int(ch.window):]...)
		}
	}
	return res, nil
}

func (f *fakeStore) ListChatMessages(_ context.Context, t store.Tenant, logID string, afterSeq, limit int32) ([]store.ChatMessage, error) {
	f.lastTenant = t
	ch := f.ch()
	l, ok := ch.logs[logID]
	if !ok || l.OrgID != t.OrgID {
		return nil, store.ErrNotFound
	}
	if limit <= 0 {
		limit = 500
	}
	var out []store.ChatMessage
	for _, m := range ch.messages[logID] {
		if m.Seq > afterSeq {
			out = append(out, m)
		}
		if int32(len(out)) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteChatMessage(_ context.Context, t store.Tenant, logID string, seq int32) error {
	f.lastTenant = t
	ch := f.ch()
	l, ok := ch.logs[logID]
	if !ok || l.OrgID != t.OrgID {
		return store.ErrNotFound
	}
	rows := ch.messages[logID]
	for i, m := range rows {
		if m.Seq == seq {
			ch.messages[logID] = append(rows[:i:i], rows[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeStore) ChatLogsEnabled(_ context.Context, t store.Tenant) (bool, error) {
	f.lastTenant = t
	return f.ch().enabled, nil
}

func (f *fakeStore) SetChatLogsEnabled(_ context.Context, t store.Tenant, enabled bool) error {
	f.lastTenant = t
	f.ch().enabled = enabled
	return nil
}

func (f *fakeStore) CompileChatTranscript(_ context.Context, t store.Tenant, logID string) ([]byte, error) {
	f.lastTenant = t
	ch := f.ch()
	l, ok := ch.logs[logID]
	if !ok || l.OrgID != t.OrgID {
		return nil, store.ErrNotFound
	}
	doc := store.ChatTranscript{
		ChatID: l.ID, Title: l.Title, SourceTool: l.SourceTool,
		TotalAppended: ch.nextSeq[logID] - 1,
	}
	for _, m := range ch.messages[logID] {
		tm := store.ChatTranscriptMessage{
			Seq: m.Seq, Role: m.Role, Kind: m.Kind, Content: m.Content, CreatedAt: m.CreatedAt,
		}
		if len(m.Meta) > 0 {
			tm.Meta = json.RawMessage(m.Meta)
		}
		if m.VersionID != nil {
			tm.VersionID = *m.VersionID
		}
		doc.Messages = append(doc.Messages, tm)
	}
	return json.Marshal(doc)
}

func (f *fakeStore) SiteChatRoutes(_ context.Context, t store.Tenant, siteID string) ([]store.RouteUpdate, error) {
	f.lastTenant = t
	if _, ok := f.sites[siteID]; !ok {
		return nil, store.ErrNotFound
	}
	// The fake has no host registry; the unit tests assert the transcript
	// object + store state, and the route refresh is covered by store-level
	// logic (SiteChatRoutes) against the real DB in integration.
	return nil, nil
}
