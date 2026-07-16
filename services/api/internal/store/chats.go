// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/danielpang/dropway/internal/chatspec"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// ErrSiteHasChatLog is returned when attaching a log to a site that already
// has one (the chat_logs_site_key partial unique index) — one attached log
// per site; detach or move the existing one first.
var ErrSiteHasChatLog = errors.New("store: site already has an attached chat log")

// ChatLog is a shared chat log (Share This Session): an append-only
// conversation history with OPTIONAL, re-pointable site attachment. Attached,
// it renders as the site's "How this was made" panel; unattached it is an
// org-internal library entry with no viewer surface.
type ChatLog struct {
	ID    string
	OrgID string
	// SiteID is the attached site (nil = unattached library entry).
	SiteID     *string
	Title      string
	SourceTool string
	// PanelEnabled gates the served pill/panel without detaching the log.
	PanelEnabled bool
	// AllowMemberEdits is the collaboration toggle (mirrors Site.AllowMemberEdits):
	// true (default) lets any org member append/curate messages; false restricts
	// edits to creator-or-admin. Deletion of the log stays creator-or-admin
	// regardless.
	AllowMemberEdits bool
	CreatedBy        string
	// MessageCount is the live row count (populated by the read paths).
	MessageCount int64
	CreatedAt    time.Time
}

// ChatMessage is one chat-log entry: a conversation turn (kind "chat") or an
// LLM-authored action annotation (kind "action", Meta = raw
// chatspec.ActionMeta JSON).
type ChatMessage struct {
	ID        string
	OrgID     string
	ChatLogID string
	Seq       int32
	// VersionID stamps the site's current deploy version at append time (nil
	// when the log was unattached / the site had no published version).
	VersionID *string
	CreatedBy string
	Role      string
	Kind      string
	Content   string
	// Meta is the raw jsonb of a kind="action" row (nil for chat turns).
	Meta      []byte
	CreatedAt time.Time
}

// AppendChatResult reports one append/import: the rows inserted, and how many
// rows the free-tier rolling window pruned in the same tx (0 on paid tiers).
// Inserted rows past the window are inserted-then-pruned, so with a window w
// and a batch of n > w the survivors are the newest w.
type AppendChatResult struct {
	Messages []ChatMessage
	Pruned   int64
	// Window is the applied rolling window (0 = hard-cap tier, nothing pruned).
	Window int64
}

// CreateChatLog inserts a chat log, optionally attached to siteID. The org
// log count runs through the quota provider under an advisory lock (the same
// race-safe COUNT → policy → INSERT critical section the site/skill caps
// use); the per-org band is a dormant seam (unlimited on every tier today).
func (s *Store) CreateChatLog(ctx context.Context, t Tenant, title, sourceTool string, siteID *string) (ChatLog, error) {
	if sourceTool == "" {
		sourceTool = chatspec.SourceOther
	}
	var out ChatLog
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := q.LockOrgChatLogQuota(ctx, t.OrgID); err != nil {
			return err
		}
		planTier, err := q.GetPlanTier(ctx, t.OrgID)
		if err != nil {
			return err
		}
		current, err := q.CountChatLogsForOrg(ctx, t.OrgID)
		if err != nil {
			return err
		}
		if err := s.quota.Allow(planTier, quota.ResourceChatLogPerOrg, current); err != nil {
			return err // *quota.ExceededError → handler renders HTTP 402
		}
		if siteID != nil {
			if err := assertSiteInOrgTx(ctx, q, t, *siteID); err != nil {
				return err
			}
		}
		row, err := q.CreateChatLog(ctx, db.CreateChatLogParams{
			OrgID:      t.OrgID,
			SiteID:     siteID,
			Title:      title,
			SourceTool: sourceTool,
			CreatedBy:  t.UserID,
		})
		if err != nil {
			if uniqueViolation(err, "chat_logs_site_key") {
				return ErrSiteHasChatLog
			}
			return err
		}
		out = chatLogFromDB(row)
		return nil
	})
	return out, err
}

// GetChatLog returns one log (with its live message count).
func (s *Store) GetChatLog(ctx context.Context, t Tenant, id string) (ChatLog, error) {
	var out ChatLog
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := getChatLogTx(ctx, q, t, id)
		if err != nil {
			return err
		}
		out = chatLogFromDB(row)
		n, err := q.CountChatMessages(ctx, id)
		if err != nil {
			return err
		}
		out.MessageCount = n
		return nil
	})
	return out, err
}

// GetChatLogForSite returns the site's attached log, ErrNotFound when none.
func (s *Store) GetChatLogForSite(ctx context.Context, t Tenant, siteID string) (ChatLog, error) {
	var out ChatLog
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		sid := siteID
		row, err := q.GetChatLogBySite(ctx, &sid)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if row.OrgID != t.OrgID {
			return ErrNotFound
		}
		out = chatLogFromDB(row)
		n, err := q.CountChatMessages(ctx, row.ID)
		if err != nil {
			return err
		}
		out.MessageCount = n
		return nil
	})
	return out, err
}

// ListChatLogs returns the org's chat library, newest first, with per-log
// message counts.
func (s *Store) ListChatLogs(ctx context.Context, t Tenant) ([]ChatLog, error) {
	var out []ChatLog
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListChatLogs(ctx, t.OrgID)
		if err != nil {
			return err
		}
		out = make([]ChatLog, len(rows))
		for i, r := range rows {
			out[i] = ChatLog{
				ID: r.ID, OrgID: r.OrgID, SiteID: r.SiteID, Title: r.Title,
				SourceTool: r.SourceTool, PanelEnabled: r.PanelEnabled,
				AllowMemberEdits: r.AllowMemberEdits,
				CreatedBy:        r.CreatedBy, MessageCount: r.MessageCount, CreatedAt: r.CreatedAt,
			}
		}
		return nil
	})
	return out, err
}

// DeleteChatLog removes a log and (via FK cascade) its messages.
func (s *Store) DeleteChatLog(ctx context.Context, t Tenant, id string) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := getChatLogTx(ctx, q, t, id); err != nil {
			return err
		}
		n, err := q.DeleteChatLog(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SetChatLogSite attaches (siteID set), detaches (nil), or moves a log. It is
// a metadata UPDATE — messages never migrate. Attaching to a site that
// already has a log returns ErrSiteHasChatLog.
func (s *Store) SetChatLogSite(ctx context.Context, t Tenant, id string, siteID *string) (ChatLog, error) {
	var out ChatLog
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := getChatLogTx(ctx, q, t, id); err != nil {
			return err
		}
		if siteID != nil {
			if err := assertSiteInOrgTx(ctx, q, t, *siteID); err != nil {
				return err
			}
		}
		row, err := q.SetChatLogSite(ctx, db.SetChatLogSiteParams{ID: id, SiteID: siteID})
		if err != nil {
			if uniqueViolation(err, "chat_logs_site_key") {
				return ErrSiteHasChatLog
			}
			return err
		}
		out = chatLogFromDB(row)
		return nil
	})
	return out, err
}

// SetChatLogPanel flips the served-panel flag (hide the pill without
// detaching the log).
func (s *Store) SetChatLogPanel(ctx context.Context, t Tenant, id string, enabled bool) (ChatLog, error) {
	var out ChatLog
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := getChatLogTx(ctx, q, t, id); err != nil {
			return err
		}
		row, err := q.SetChatLogPanelEnabled(ctx, db.SetChatLogPanelEnabledParams{ID: id, PanelEnabled: enabled})
		if err != nil {
			return err
		}
		out = chatLogFromDB(row)
		return nil
	})
	return out, err
}

// AppendChatMessages appends msgs (already chatspec-validated by the handler)
// to a log inside one tx, under the log's advisory lock:
//
//   - window tier (free): INSERT all, then prune to the newest `window` rows —
//     appends never 402; the trimmed remainder is reported for disclosure.
//   - hard-cap tier (pro): COUNT → AllowN(current, n) → INSERT — the batch
//     either fits entirely or 402s before any row lands.
//
// Each row is stamped with the attached site's CURRENT version id (nil when
// unattached or never published), and seq numbers come from the log's
// monotonic allocator so pruning never reuses them.
func (s *Store) AppendChatMessages(ctx context.Context, t Tenant, logID string, msgs []chatspec.Message) (AppendChatResult, error) {
	if len(msgs) == 0 {
		return AppendChatResult{}, errors.New("store: no messages to append")
	}
	var res AppendChatResult
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if err := q.LockChatLogAppend(ctx, logID); err != nil {
			return err
		}
		log, err := getChatLogTx(ctx, q, t, logID)
		if err != nil {
			return err
		}
		planTier, err := q.GetPlanTier(ctx, t.OrgID)
		if err != nil {
			return err
		}

		// Version stamp: the attached site's current deploy at append time.
		var versionID *string
		if log.SiteID != nil {
			vid, err := q.GetSiteCurrentVersionID(ctx, *log.SiteID)
			if err != nil && !isNoRows(err) {
				return err
			}
			versionID = vid
		}

		window, windowed := quota.RetentionWindow(s.quota, planTier, quota.ResourceChatMessagePerLog)
		if !windowed {
			current, err := q.CountChatMessages(ctx, logID)
			if err != nil {
				return err
			}
			if err := s.quota.AllowN(planTier, quota.ResourceChatMessagePerLog, current, int64(len(msgs))); err != nil {
				return err // *quota.ExceededError → 402; no row lands
			}
		}

		base, err := q.AllocateChatSeq(ctx, db.AllocateChatSeqParams{ID: logID, Column2: int32(len(msgs))})
		if err != nil {
			return err
		}
		res.Messages = make([]ChatMessage, 0, len(msgs))
		for i, m := range msgs {
			var meta []byte
			if m.Meta != nil {
				meta, err = json.Marshal(m.Meta)
				if err != nil {
					return err
				}
			}
			row, err := q.InsertChatMessage(ctx, db.InsertChatMessageParams{
				OrgID:     t.OrgID,
				ChatLogID: logID,
				Seq:       base + int32(i),
				VersionID: versionID,
				CreatedBy: t.UserID,
				Role:      m.Role,
				Kind:      m.Kind,
				Content:   m.Content,
				Meta:      meta,
			})
			if err != nil {
				return err
			}
			res.Messages = append(res.Messages, chatMessageFromDB(row))
		}
		if windowed {
			res.Window = window
			pruned, err := q.PruneChatMessages(ctx, db.PruneChatMessagesParams{
				ChatLogID: logID,
				Limit:     int32(window),
			})
			if err != nil {
				return err
			}
			res.Pruned = pruned
		}
		return nil
	})
	return res, err
}

// ListChatMessages pages a log's messages forward: seq > afterSeq, ascending,
// up to limit (≤ 0 → 500).
func (s *Store) ListChatMessages(ctx context.Context, t Tenant, logID string, afterSeq int32, limit int32) ([]ChatMessage, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	var out []ChatMessage
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		if _, err := getChatLogTx(ctx, q, t, logID); err != nil {
			return err
		}
		rows, err := q.ListChatMessages(ctx, db.ListChatMessagesParams{
			ChatLogID: logID,
			Seq:       afterSeq,
			Limit:     limit,
		})
		if err != nil {
			return err
		}
		out = make([]ChatMessage, len(rows))
		for i, r := range rows {
			out[i] = chatMessageFromDB(r)
		}
		return nil
	})
	return out, err
}

// DeleteChatMessage removes ONE message by seq (mistakes, pasted secrets).
// Deletion frees hard-cap slots; seq numbers are never reused.
func (s *Store) DeleteChatMessage(ctx context.Context, t Tenant, logID string, seq int32) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		if err := q.LockChatLogAppend(ctx, logID); err != nil {
			return err
		}
		if _, err := getChatLogTx(ctx, q, t, logID); err != nil {
			return err
		}
		n, err := q.DeleteChatMessage(ctx, db.DeleteChatMessageParams{ChatLogID: logID, Seq: seq})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ChatLogsEnabled reads the org kill switch (fail-soft true).
func (s *Store) ChatLogsEnabled(ctx context.Context, t Tenant) (bool, error) {
	var enabled bool
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		var err error
		enabled, err = q.GetChatLogsEnabled(ctx, t.OrgID)
		return err
	})
	return enabled, err
}

// SetChatLogsEnabled flips the org kill switch (admin-gated in the handler).
func (s *Store) SetChatLogsEnabled(ctx context.Context, t Tenant, enabled bool) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		return q.SetChatLogsEnabled(ctx, db.SetChatLogsEnabledParams{ID: t.OrgID, ChatLogsEnabled: enabled})
	})
}

// ChatTranscript is the compiled transcript document the handler writes to
// object storage (chat-transcripts/<org>/<chat_id>.json) after every
// mutation, and the serving Worker reads at /__dropway/chat. It is a
// PROJECTION of Postgres (rebuildable), never authoritative.
type ChatTranscript struct {
	ChatID     string                  `json:"chat_id"`
	Title      string                  `json:"title,omitempty"`
	SourceTool string                  `json:"source_tool,omitempty"`
	Messages   []ChatTranscriptMessage `json:"messages"`
	// TotalAppended is the high-water seq: with a rolling window the viewer
	// can say "showing the last N of a longer conversation".
	TotalAppended int32 `json:"total_appended"`
}

// ChatTranscriptMessage is one compiled transcript entry.
type ChatTranscriptMessage struct {
	Seq       int32           `json:"seq"`
	Role      string          `json:"role"`
	Kind      string          `json:"kind"`
	Content   string          `json:"content"`
	Meta      json.RawMessage `json:"meta,omitempty"`
	VersionID string          `json:"version_id,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// CompileChatTranscript builds the served transcript JSON for a log.
func (s *Store) CompileChatTranscript(ctx context.Context, t Tenant, logID string) ([]byte, error) {
	var doc ChatTranscript
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		log, err := getChatLogTx(ctx, q, t, logID)
		if err != nil {
			return err
		}
		doc.ChatID = log.ID
		doc.Title = log.Title
		doc.SourceTool = log.SourceTool
		doc.TotalAppended = log.NextSeq - 1
		rows, err := q.ListChatMessages(ctx, db.ListChatMessagesParams{
			ChatLogID: logID,
			Seq:       0,
			Limit:     int32(chatspec.MaxImportMessages),
		})
		if err != nil {
			return err
		}
		doc.Messages = make([]ChatTranscriptMessage, len(rows))
		for i, r := range rows {
			m := ChatTranscriptMessage{
				Seq: r.Seq, Role: r.Role, Kind: r.Kind, Content: r.Content, CreatedAt: r.CreatedAt,
			}
			if len(r.Meta) > 0 {
				m.Meta = json.RawMessage(r.Meta)
			}
			if r.VersionID != nil {
				m.VersionID = *r.VersionID
			}
			doc.Messages[i] = m
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(doc)
}

// SiteChatRoutes re-derives every non-preview route of ONE site with the
// site's CURRENT chat state (attach/detach/panel flips change RouteValue.
// chat_id without a republish — the projection mirror of ReprojectOrgRoutes,
// scoped to one site). Returns no routes when the site has never been
// published (there is nothing at the edge to refresh).
func (s *Store) SiteChatRoutes(ctx context.Context, t Tenant, siteID string) ([]RouteUpdate, error) {
	var updates []RouteUpdate
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		site, err := q.GetSite(ctx, siteID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if site.OrgID != t.OrgID {
			return ErrNotFound
		}
		if site.CurrentVersionID == nil {
			return nil // never published — no edge routes to refresh
		}
		planTier, err := q.GetPlanTier(ctx, t.OrgID)
		if err != nil {
			return err
		}
		var expiresAt string
		if pol, perr := q.GetSiteAccessPolicy(ctx, siteID); perr == nil {
			expiresAt = routeExpiry(site.AccessMode, accessPolicyFromDB(pol))
		} else if !isNoRows(perr) {
			return perr
		}
		chatID, err := chatIDForSiteTx(ctx, q, siteID)
		if err != nil {
			return err
		}
		hostRoutes, err := q.ListHostRoutesForSite(ctx, siteID)
		if err != nil {
			return err
		}
		for _, hr := range hostRoutes {
			if hr.Kind == RouteKindPreview {
				continue
			}
			updates = append(updates, RouteUpdate{
				Host:  hr.Host,
				Route: routeValue(t.OrgID, siteID, *site.CurrentVersionID, site.AccessMode, expiresAt, planTier, chatID),
			})
		}
		return nil
	})
	return updates, err
}

// chatIDForSiteTx returns the site's attached, panel-enabled chat log id
// ("" when none) — the RouteValue.chat_id input for publish/rebuild/refresh.
func chatIDForSiteTx(ctx context.Context, q *db.Queries, siteID string) (string, error) {
	sid := siteID
	row, err := q.GetChatLogBySite(ctx, &sid)
	if err != nil {
		if isNoRows(err) {
			return "", nil
		}
		return "", err
	}
	if !row.PanelEnabled {
		return "", nil
	}
	return row.ID, nil
}

// getChatLogTx reads a log and asserts it belongs to the active tenant (the
// confused-deputy guard sensitive chat writes share).
func getChatLogTx(ctx context.Context, q *db.Queries, t Tenant, id string) (db.AppChatLog, error) {
	row, err := q.GetChatLog(ctx, id)
	if err != nil {
		if isNoRows(err) {
			return db.AppChatLog{}, ErrNotFound
		}
		return db.AppChatLog{}, err
	}
	if row.OrgID != t.OrgID {
		return db.AppChatLog{}, ErrNotFound
	}
	return row, nil
}

// assertSiteInOrgTx verifies the site exists and belongs to the tenant.
func assertSiteInOrgTx(ctx context.Context, q *db.Queries, t Tenant, siteID string) error {
	site, err := q.GetSite(ctx, siteID)
	if err != nil {
		if isNoRows(err) {
			return ErrNotFound
		}
		return err
	}
	if site.OrgID != t.OrgID {
		return ErrNotFound
	}
	return nil
}

func chatLogFromDB(r db.AppChatLog) ChatLog {
	return ChatLog{
		ID: r.ID, OrgID: r.OrgID, SiteID: r.SiteID, Title: r.Title,
		SourceTool: r.SourceTool, PanelEnabled: r.PanelEnabled,
		AllowMemberEdits: r.AllowMemberEdits,
		CreatedBy:        r.CreatedBy, CreatedAt: r.CreatedAt,
	}
}

func chatMessageFromDB(r db.AppChatMessage) ChatMessage {
	return ChatMessage{
		ID: r.ID, OrgID: r.OrgID, ChatLogID: r.ChatLogID, Seq: r.Seq,
		VersionID: r.VersionID, CreatedBy: r.CreatedBy, Role: r.Role,
		Kind: r.Kind, Content: r.Content, Meta: r.Meta, CreatedAt: r.CreatedAt,
	}
}
