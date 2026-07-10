// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// ---------------------------------------------------------------------------
// AI builder — sessions, transcript, cost ledger. All RLS-scoped like every
// other tenant table; the confused-deputy guards mirror the deploy path.
// ---------------------------------------------------------------------------

// AISession is one AI-builder chat stream for a site.
type AISession struct {
	ID               string
	OrgID            string
	SiteID           string
	CreatedBy        string
	Status           string
	Model            string
	SandboxID        string
	SandboxExpiresAt *time.Time
	BaseVersionID    *string
	LatestVersionID  *string
	CreatedAt        time.Time
	LastActivityAt   time.Time
}

// AIMessage is one transcript entry (OpenAI message shape in Content).
type AIMessage struct {
	ID        string
	SessionID string
	Seq       int32
	Role      string
	Content   json.RawMessage
	CreatedAt time.Time
}

// AIUsageRow is one OpenRouter generation in the cost ledger.
type AIUsageRow struct {
	ID                     string
	SessionID              *string
	Model                  string
	OpenrouterGenerationID string
	PromptTokens           int64
	CompletionTokens       int64
	CostUSD                float64
	Reported               bool
	CreatedAt              time.Time
}

// AISettings is the AI gate input for an org: the kill switch + monthly cap.
type AISettings struct {
	Enabled       bool
	MonthlyCapUSD float64
}

func sessionFromDB(r db.AppAiSession) AISession {
	s := AISession{
		ID:              r.ID,
		OrgID:           r.OrgID,
		SiteID:          r.SiteID,
		CreatedBy:       r.CreatedBy,
		Status:          r.Status,
		Model:           r.Model,
		BaseVersionID:   r.BaseVersionID,
		LatestVersionID: r.LatestVersionID,
		CreatedAt:       r.CreatedAt,
		LastActivityAt:  r.LastActivityAt,
	}
	if r.SandboxID.Valid {
		s.SandboxID = r.SandboxID.String
	}
	if r.SandboxExpiresAt.Valid {
		t := r.SandboxExpiresAt.Time
		s.SandboxExpiresAt = &t
	}
	return s
}

// CreateAISession creates a session for a site the active tenant owns. The
// caller has already resolved model + base version and run the AI gate +
// concurrency preflight (StartAISession wraps all of that atomically).
func (s *Store) CreateAISession(ctx context.Context, t Tenant, siteID, model string, baseVersionID *string) (AISession, error) {
	var out AISession
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
		row, err := q.CreateAISession(ctx, db.CreateAISessionParams{
			OrgID:         t.OrgID,
			SiteID:        siteID,
			CreatedBy:     t.UserID,
			Model:         model,
			BaseVersionID: baseVersionID,
		})
		if err != nil {
			return err
		}
		out = sessionFromDB(row)
		return nil
	})
	return out, err
}

// ErrAIConcurrencyLimit is returned when an org already has the maximum number
// of active AI sessions (the per-org concurrency cap → 429).
var ErrAIConcurrencyLimit = errors.New("store: AI session concurrency limit reached")

// StartAISession runs the create with the per-org concurrency cap enforced
// under an advisory lock (the same TOCTOU guard the site cap uses): it counts
// active sessions, refuses past maxConcurrent, then inserts — all in one tx.
func (s *Store) StartAISession(ctx context.Context, t Tenant, siteID, model string, baseVersionID *string, maxConcurrent int) (AISession, error) {
	var out AISession
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
		if err := q.LockOrgAISessionQuota(ctx, t.OrgID); err != nil {
			return err
		}
		n, err := q.CountActiveAISessions(ctx, t.OrgID)
		if err != nil {
			return err
		}
		if maxConcurrent > 0 && n >= int64(maxConcurrent) {
			return ErrAIConcurrencyLimit
		}
		row, err := q.CreateAISession(ctx, db.CreateAISessionParams{
			OrgID:         t.OrgID,
			SiteID:        siteID,
			CreatedBy:     t.UserID,
			Model:         model,
			BaseVersionID: baseVersionID,
		})
		if err != nil {
			return err
		}
		out = sessionFromDB(row)
		return nil
	})
	return out, err
}

// GetAISession returns one session, asserting tenant ownership.
func (s *Store) GetAISession(ctx context.Context, t Tenant, id string) (AISession, error) {
	var out AISession
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.GetAISession(ctx, id)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if row.OrgID != t.OrgID {
			return ErrNotFound
		}
		out = sessionFromDB(row)
		return nil
	})
	return out, err
}

// ListAISessionsForSite returns a site's non-archived sessions, newest activity
// first.
func (s *Store) ListAISessionsForSite(ctx context.Context, t Tenant, siteID string) ([]AISession, error) {
	var out []AISession
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListAISessionsForSite(ctx, siteID)
		if err != nil {
			return err
		}
		out = make([]AISession, len(rows))
		for i, r := range rows {
			out[i] = sessionFromDB(r)
		}
		return nil
	})
	return out, err
}

// SetAISessionStatus updates a session's lifecycle status.
func (s *Store) SetAISessionStatus(ctx context.Context, t Tenant, id, status string) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		return q.SetAISessionStatus(ctx, db.SetAISessionStatusParams{ID: id, Status: status})
	})
}

// SetAISessionSandbox caches (or clears) the live sandbox handle for a session.
func (s *Store) SetAISessionSandbox(ctx context.Context, t Tenant, id, sandboxID string, expiresAt *time.Time) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		p := db.SetAISessionSandboxParams{ID: id}
		if sandboxID != "" {
			p.SandboxID = pgtype.Text{String: sandboxID, Valid: true}
		}
		if expiresAt != nil {
			p.SandboxExpiresAt = pgtype.Timestamptz{Time: *expiresAt, Valid: true}
		}
		return q.SetAISessionSandbox(ctx, p)
	})
}

// SetAISessionLatestVersion records the newest draft a session produced.
func (s *Store) SetAISessionLatestVersion(ctx context.Context, t Tenant, id, versionID string) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		v := versionID
		return q.SetAISessionLatestVersion(ctx, db.SetAISessionLatestVersionParams{ID: id, LatestVersionID: &v})
	})
}

// DeleteAISession removes a session (cascade drops its messages; usage rows
// survive with a null session_id for billing integrity).
func (s *Store) DeleteAISession(ctx context.Context, t Tenant, id string) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.GetAISession(ctx, id)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if row.OrgID != t.OrgID {
			return ErrNotFound
		}
		return q.DeleteAISession(ctx, id)
	})
}

// AppendAIMessage appends one transcript message with the next per-session seq,
// returning the assigned seq (the SSE Last-Event-ID).
func (s *Store) AppendAIMessage(ctx context.Context, t Tenant, sessionID, role string, content json.RawMessage) (AIMessage, error) {
	var out AIMessage
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.AppendAIMessage(ctx, db.AppendAIMessageParams{
			OrgID:     t.OrgID,
			SessionID: sessionID,
			Role:      role,
			Content:   content,
		})
		if err != nil {
			return err
		}
		out = messageFromDB(row)
		return nil
	})
	return out, err
}

// ListAIMessages returns a session's transcript after afterSeq (0 = all).
func (s *Store) ListAIMessages(ctx context.Context, t Tenant, sessionID string, afterSeq int32) ([]AIMessage, error) {
	var out []AIMessage
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListAIMessages(ctx, db.ListAIMessagesParams{SessionID: sessionID, Seq: afterSeq})
		if err != nil {
			return err
		}
		out = make([]AIMessage, len(rows))
		for i, r := range rows {
			out[i] = messageFromDB(r)
		}
		return nil
	})
	return out, err
}

func messageFromDB(r db.AppAiMessage) AIMessage {
	return AIMessage{
		ID:        r.ID,
		SessionID: r.SessionID,
		Seq:       r.Seq,
		Role:      r.Role,
		Content:   json.RawMessage(r.Content),
		CreatedAt: r.CreatedAt,
	}
}

// RecordAIUsage appends one OpenRouter generation to the cost ledger. It is
// idempotent on the generation id (a retried turn never double-counts):
// recorded reports whether the row was newly inserted.
func (s *Store) RecordAIUsage(ctx context.Context, t Tenant, u AIUsageRow) (recorded bool, err error) {
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		_, insErr := q.InsertAIUsage(ctx, db.InsertAIUsageParams{
			OrgID:                  t.OrgID,
			SessionID:              u.SessionID,
			Model:                  u.Model,
			OpenrouterGenerationID: u.OpenrouterGenerationID,
			PromptTokens:           u.PromptTokens,
			CompletionTokens:       u.CompletionTokens,
			Column7:                u.CostUSD,
		})
		if insErr != nil {
			if isNoRows(insErr) {
				return nil // already recorded (ON CONFLICT DO NOTHING → no row)
			}
			return insErr
		}
		recorded = true
		return nil
	})
	return recorded, err
}

// AISpendSince returns the org's AI spend (USD) since the period start — the
// spend-cap check input and the dashboard usage figure.
func (s *Store) AISpendSince(ctx context.Context, t Tenant, since time.Time) (float64, error) {
	var total float64
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		v, err := q.SumAIUsageSince(ctx, db.SumAIUsageSinceParams{OrgID: t.OrgID, CreatedAt: since})
		total = v
		return err
	})
	return total, err
}

// ListAIUsage returns recent ledger rows since a period start (billing page).
func (s *Store) ListAIUsage(ctx context.Context, t Tenant, since time.Time, limit int32) ([]AIUsageRow, error) {
	var out []AIUsageRow
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListAIUsageForOrg(ctx, db.ListAIUsageForOrgParams{OrgID: t.OrgID, CreatedAt: since, Limit: limit})
		if err != nil {
			return err
		}
		out = make([]AIUsageRow, len(rows))
		for i, r := range rows {
			out[i] = usageFromDB(r)
		}
		return nil
	})
	return out, err
}

func usageFromDB(r db.AppAiUsage) AIUsageRow {
	return AIUsageRow{
		ID:                     r.ID,
		SessionID:              r.SessionID,
		Model:                  r.Model,
		OpenrouterGenerationID: r.OpenrouterGenerationID,
		PromptTokens:           r.PromptTokens,
		CompletionTokens:       r.CompletionTokens,
		CostUSD:                r.CostUsd,
		Reported:               r.ReportedToBillingAt.Valid,
		CreatedAt:              r.CreatedAt,
	}
}

// GetAISettings returns the org's AI gate inputs (kill switch + cap). It reads
// org_meta, provisioning the anchor first so a brand-new org has defaults.
func (s *Store) GetAISettings(ctx context.Context, t Tenant) (AISettings, error) {
	var out AISettings
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.GetOrgMeta(ctx, t.OrgID)
		if err != nil {
			if isNoRows(err) {
				// Fail-soft to defaults (AI on, $20 cap), matching GetPlanTier.
				out = AISettings{Enabled: true, MonthlyCapUSD: 20}
				return nil
			}
			return err
		}
		out = AISettings{Enabled: row.AiEnabled, MonthlyCapUSD: row.AiMonthlyCapUsd}
		return nil
	})
	return out, err
}

// SetAIEnabled toggles the org AI kill switch (admin/owner, re-checked in Go).
func (s *Store) SetAIEnabled(ctx context.Context, t Tenant, enabled bool) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		return q.SetAIEnabled(ctx, db.SetAIEnabledParams{ID: t.OrgID, AiEnabled: enabled})
	})
}

// SetAIMonthlyCap sets the org's monthly AI spend cap (USD).
func (s *Store) SetAIMonthlyCap(ctx context.Context, t Tenant, capUSD float64) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		return q.SetAIMonthlyCap(ctx, db.SetAIMonthlyCapParams{ID: t.OrgID, Column2: capUSD})
	})
}
