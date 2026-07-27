// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// ---------------------------------------------------------------------------
// Org memory — durable per-org facts (org_memories), extraction watermarks
// (org_memory_ingests), and embedded content chunks (org_content_chunks).
// All RLS-scoped like every other tenant table; embeddings travel as pgvector
// text ('[f1,f2,...]') and are never read back.
// ---------------------------------------------------------------------------

// OrgMemory is one durable fact Dropway has learned about an org.
type OrgMemory struct {
	ID          string
	OrgID       string
	Kind        string
	Content     string
	ContentHash string
	// EmbeddingModel is the model that produced the stored embedding; rows
	// whose model differs from the active one are invisible to search until
	// re-embedded (see the org-memory scope doc §3.5).
	EmbeddingModel string
	SourceKind     string
	SourceID       *string
	SourceTool     string
	Pinned         bool
	Disabled       bool
	CreatedBy      *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastUsedAt     *time.Time
	// Distance is the cosine distance to the query (search results only).
	Distance float64
}

// ContentChunk is one embedded slice of a published site or skill file.
type ContentChunk struct {
	ID         string
	SourceKind string // 'site_version' | 'skill'
	Path       string
	ChunkSeq   int32
	Content    string
	SiteSlug   string // set for site_version chunks
	SkillSlug  string // set for skill chunks
	Distance   float64
}

// MemoryFilter narrows ListOrgMemories. Zero values match everything except
// disabled rows (set IncludeDisabled for the curation UI).
type MemoryFilter struct {
	Kind            string
	Query           string
	PinnedOnly      bool
	IncludeDisabled bool
	Limit           int32
	Offset          int32
}

// ErrMemoryQuota is returned when an org is at its memory row cap (new inserts
// are refused; refreshes of existing rows still succeed).
var ErrMemoryQuota = errors.New("store: org memory quota reached")

// NewMemoryInput is the write shape for UpsertOrgMemory.
type NewMemoryInput struct {
	Kind           string
	Content        string
	Embedding      []float32 // nil → stored un-embedded, repaired by sweep
	EmbeddingModel string
	SourceKind     string
	SourceID       *string
	SourceTool     string
	CreatedBy      *string
}

// NormalizeMemoryContent is the canonical form content is hashed over:
// whitespace-collapsed, trimmed, lower-cased. Semantically identical
// restatements still insert separately — the extraction pipeline's similarity
// dedupe handles those.
func NormalizeMemoryContent(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// MemoryContentHash is the per-org dedupe key for a memory's content.
func MemoryContentHash(content string) string {
	sum := sha256.Sum256([]byte(NormalizeMemoryContent(content)))
	return hex.EncodeToString(sum[:])
}

// VectorText renders an embedding in pgvector's text form for a ::vector cast.
func VectorText(v []float32) string {
	if len(v) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(v) * 10)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// UpsertOrgMemory inserts a memory (quota-capped under the org advisory lock)
// or refreshes the existing row with the same normalized content. created
// reports whether a genuinely new row was inserted. A disabled row is
// refreshed but stays disabled — extraction can never resurrect it.
func (s *Store) UpsertOrgMemory(ctx context.Context, t Tenant, in NewMemoryInput, maxPerOrg int) (mem OrgMemory, created bool, err error) {
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		if err := q.LockOrgMemoryQuota(ctx, t.OrgID); err != nil {
			return err
		}
		n, err := q.CountOrgMemories(ctx, t.OrgID)
		if err != nil {
			return err
		}
		hash := MemoryContentHash(in.Content)
		if maxPerOrg > 0 && n >= int64(maxPerOrg) {
			// At cap: allow the refresh path (existing hash) but refuse new rows.
			if _, err := q.GetOrgMemoryByHash(ctx, db.GetOrgMemoryByHashParams{OrgID: t.OrgID, ContentHash: hash}); err != nil {
				if isNoRows(err) {
					return ErrMemoryQuota
				}
				return err
			}
		}
		row, err := q.UpsertOrgMemory(ctx, db.UpsertOrgMemoryParams{
			OrgID:          t.OrgID,
			Kind:           in.Kind,
			Content:        in.Content,
			ContentHash:    hash,
			Column5:        VectorText(in.Embedding),
			EmbeddingModel: in.EmbeddingModel,
			SourceKind:     in.SourceKind,
			SourceID:       in.SourceID,
			SourceTool:     textOrNull(in.SourceTool),
			CreatedBy:      in.CreatedBy,
		})
		if err != nil {
			return err
		}
		mem = memoryFromUpsert(row)
		created = row.Inserted
		return nil
	})
	return mem, created, err
}

// SearchOrgMemories returns the org's top-k memories by cosine distance to the
// query embedding, excluding pinned and disabled rows and rows embedded by a
// different model.
func (s *Store) SearchOrgMemories(ctx context.Context, t Tenant, embedding []float32, model string, k int32) ([]OrgMemory, error) {
	var out []OrgMemory
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.SearchOrgMemories(ctx, db.SearchOrgMemoriesParams{
			OrgID:          t.OrgID,
			Column2:        VectorText(embedding),
			EmbeddingModel: model,
			Limit:          k,
		})
		if err != nil {
			return err
		}
		out = make([]OrgMemory, 0, len(rows))
		for _, r := range rows {
			m := memoryFromParts(r.ID, r.OrgID, r.Kind, r.Content, r.ContentHash, r.EmbeddingModel, r.SourceKind, r.SourceID, r.SourceTool, r.Pinned, r.Disabled, r.CreatedBy, r.CreatedAt, r.UpdatedAt, r.LastUsedAt)
			m.Distance = r.Distance
			out = append(out, m)
		}
		return nil
	})
	return out, err
}

// NearestOrgMemoryDistance returns the cosine distance to the org's closest
// memory — pinned and disabled INCLUDED, unlike SearchOrgMemories — for the
// extraction dedupe probe. found is false when the org has no embedded rows.
func (s *Store) NearestOrgMemoryDistance(ctx context.Context, t Tenant, embedding []float32, model string) (found bool, distance float64, err error) {
	err = s.withTx(ctx, t, func(q *db.Queries) error {
		r, qerr := q.NearestOrgMemory(ctx, db.NearestOrgMemoryParams{
			OrgID:          t.OrgID,
			Column2:        VectorText(embedding),
			EmbeddingModel: model,
		})
		if qerr != nil {
			if isNoRows(qerr) {
				return nil
			}
			return qerr
		}
		found, distance = true, r.Distance
		return nil
	})
	return found, distance, err
}

// ListPinnedOrgMemories returns the always-injected set (pinned, enabled).
func (s *Store) ListPinnedOrgMemories(ctx context.Context, t Tenant, limit int32) ([]OrgMemory, error) {
	var out []OrgMemory
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListPinnedOrgMemories(ctx, db.ListPinnedOrgMemoriesParams{OrgID: t.OrgID, Limit: limit})
		if err != nil {
			return err
		}
		out = make([]OrgMemory, 0, len(rows))
		for _, r := range rows {
			out = append(out, memoryFromParts(r.ID, r.OrgID, r.Kind, r.Content, r.ContentHash, r.EmbeddingModel, r.SourceKind, r.SourceID, r.SourceTool, r.Pinned, r.Disabled, r.CreatedBy, r.CreatedAt, r.UpdatedAt, r.LastUsedAt))
		}
		return nil
	})
	return out, err
}

// ListOrgMemories returns the filtered curation list, pinned first.
func (s *Store) ListOrgMemories(ctx context.Context, t Tenant, f MemoryFilter) ([]OrgMemory, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	var out []OrgMemory
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListOrgMemories(ctx, db.ListOrgMemoriesParams{
			OrgID:   t.OrgID,
			Column2: f.Kind,
			Column3: f.Query,
			Column4: f.PinnedOnly,
			Column5: f.IncludeDisabled,
			Limit:   f.Limit,
			Offset:  f.Offset,
		})
		if err != nil {
			return err
		}
		out = make([]OrgMemory, 0, len(rows))
		for _, r := range rows {
			out = append(out, memoryFromParts(r.ID, r.OrgID, r.Kind, r.Content, r.ContentHash, r.EmbeddingModel, r.SourceKind, r.SourceID, r.SourceTool, r.Pinned, r.Disabled, r.CreatedBy, r.CreatedAt, r.UpdatedAt, r.LastUsedAt))
		}
		return nil
	})
	return out, err
}

// GetOrgMemory returns one memory row (RLS-scoped).
func (s *Store) GetOrgMemory(ctx context.Context, t Tenant, id string) (OrgMemory, error) {
	var out OrgMemory
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		r, err := q.GetOrgMemory(ctx, db.GetOrgMemoryParams{ID: id, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = memoryFromParts(r.ID, r.OrgID, r.Kind, r.Content, r.ContentHash, r.EmbeddingModel, r.SourceKind, r.SourceID, r.SourceTool, r.Pinned, r.Disabled, r.CreatedBy, r.CreatedAt, r.UpdatedAt, r.LastUsedAt)
		return nil
	})
	return out, err
}

// UpdateOrgMemoryContent replaces a memory's content (admin edit). The caller
// re-embeds the new content first.
func (s *Store) UpdateOrgMemoryContent(ctx context.Context, t Tenant, id, content string, embedding []float32, model, kind string) (OrgMemory, error) {
	var out OrgMemory
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		r, err := q.UpdateOrgMemoryContent(ctx, db.UpdateOrgMemoryContentParams{
			ID:             id,
			OrgID:          t.OrgID,
			Content:        content,
			ContentHash:    MemoryContentHash(content),
			Column5:        VectorText(embedding),
			EmbeddingModel: model,
			Kind:           kind,
		})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = memoryFromParts(r.ID, r.OrgID, r.Kind, r.Content, r.ContentHash, r.EmbeddingModel, r.SourceKind, r.SourceID, r.SourceTool, r.Pinned, r.Disabled, r.CreatedBy, r.CreatedAt, r.UpdatedAt, r.LastUsedAt)
		return nil
	})
	return out, err
}

// SetOrgMemoryPinned pins/unpins a memory.
func (s *Store) SetOrgMemoryPinned(ctx context.Context, t Tenant, id string, pinned bool) (OrgMemory, error) {
	var out OrgMemory
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		r, err := q.SetOrgMemoryPinned(ctx, db.SetOrgMemoryPinnedParams{ID: id, OrgID: t.OrgID, Pinned: pinned})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = memoryFromParts(r.ID, r.OrgID, r.Kind, r.Content, r.ContentHash, r.EmbeddingModel, r.SourceKind, r.SourceID, r.SourceTool, r.Pinned, r.Disabled, r.CreatedBy, r.CreatedAt, r.UpdatedAt, r.LastUsedAt)
		return nil
	})
	return out, err
}

// SetOrgMemoryDisabled suppresses/reactivates a memory.
func (s *Store) SetOrgMemoryDisabled(ctx context.Context, t Tenant, id string, disabled bool) (OrgMemory, error) {
	var out OrgMemory
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		r, err := q.SetOrgMemoryDisabled(ctx, db.SetOrgMemoryDisabledParams{ID: id, OrgID: t.OrgID, Disabled: disabled})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		out = memoryFromParts(r.ID, r.OrgID, r.Kind, r.Content, r.ContentHash, r.EmbeddingModel, r.SourceKind, r.SourceID, r.SourceTool, r.Pinned, r.Disabled, r.CreatedBy, r.CreatedAt, r.UpdatedAt, r.LastUsedAt)
		return nil
	})
	return out, err
}

// DeleteOrgMemory hard-deletes a memory.
func (s *Store) DeleteOrgMemory(ctx context.Context, t Tenant, id string) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		n, err := q.DeleteOrgMemory(ctx, db.DeleteOrgMemoryParams{ID: id, OrgID: t.OrgID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// CountOrgMemories returns the org's memory row count (quota display).
func (s *Store) CountOrgMemories(ctx context.Context, t Tenant) (int64, error) {
	var n int64
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		var err error
		n, err = q.CountOrgMemories(ctx, t.OrgID)
		return err
	})
	return n, err
}

// TouchOrgMemoriesUsed stamps last_used_at on the injected rows (best-effort;
// callers ignore the error).
func (s *Store) TouchOrgMemoriesUsed(ctx context.Context, t Tenant, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.withTx(ctx, t, func(q *db.Queries) error {
		return q.TouchOrgMemoriesUsed(ctx, db.TouchOrgMemoriesUsedParams{OrgID: t.OrgID, Column2: ids})
	})
}

// MemoryEnabled reads the org's memory kill switch.
func (s *Store) MemoryEnabled(ctx context.Context, t Tenant) (bool, error) {
	var enabled bool
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		var err error
		enabled, err = q.GetMemoryEnabled(ctx, t.OrgID)
		if isNoRows(err) {
			return ErrNotFound
		}
		return err
	})
	return enabled, err
}

// SetMemoryEnabled flips the org's memory kill switch (admin-gated in Go).
func (s *Store) SetMemoryEnabled(ctx context.Context, t Tenant, enabled bool) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		return q.SetMemoryEnabled(ctx, db.SetMemoryEnabledParams{ID: t.OrgID, MemoryEnabled: enabled})
	})
}

// MemoryIngestSeq returns the extraction watermark for a source (0 when the
// source has never been processed).
func (s *Store) MemoryIngestSeq(ctx context.Context, t Tenant, sourceKind, sourceID string) (int64, error) {
	var seq int64
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		r, err := q.GetMemoryIngest(ctx, db.GetMemoryIngestParams{OrgID: t.OrgID, SourceKind: sourceKind, SourceID: sourceID})
		if err != nil {
			if isNoRows(err) {
				seq = 0
				return nil
			}
			return err
		}
		seq = r.ThroughSeq
		return nil
	})
	return seq, err
}

// AdvanceMemoryIngest moves a source's extraction watermark forward
// (monotonic; concurrent writers can't move it back).
func (s *Store) AdvanceMemoryIngest(ctx context.Context, t Tenant, sourceKind, sourceID string, throughSeq int64) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		return q.UpsertMemoryIngest(ctx, db.UpsertMemoryIngestParams{
			OrgID:      t.OrgID,
			SourceKind: sourceKind,
			SourceID:   sourceID,
			ThroughSeq: throughSeq,
		})
	})
}

// NewChunkInput is one embedded content slice for ReplaceContentChunks.
type NewChunkInput struct {
	Path      string
	ChunkSeq  int32
	Content   string
	Embedding []float32
}

// ReplaceContentChunks swaps a source's chunks atomically: delete-then-insert
// in one tx so re-indexing a version/skill never leaves a mixed state.
// Exactly one of versionID (with siteID) or skillID must be set.
func (s *Store) ReplaceContentChunks(ctx context.Context, t Tenant, sourceKind string, versionID, siteID, skillID *string, model string, chunks []NewChunkInput) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		switch sourceKind {
		case "site_version":
			if versionID == nil {
				return errors.New("store: site_version chunks need a version id")
			}
			if _, err := q.DeleteContentChunksForVersion(ctx, db.DeleteContentChunksForVersionParams{OrgID: t.OrgID, VersionID: versionID}); err != nil {
				return err
			}
		case "skill":
			if skillID == nil {
				return errors.New("store: skill chunks need a skill id")
			}
			if _, err := q.DeleteContentChunksForSkill(ctx, db.DeleteContentChunksForSkillParams{OrgID: t.OrgID, SkillID: skillID}); err != nil {
				return err
			}
		default:
			return errors.New("store: unknown chunk source kind " + sourceKind)
		}
		for _, c := range chunks {
			if err := q.InsertContentChunk(ctx, db.InsertContentChunkParams{
				OrgID:          t.OrgID,
				SourceKind:     sourceKind,
				VersionID:      versionID,
				SiteID:         siteID,
				SkillID:        skillID,
				Path:           c.Path,
				ChunkSeq:       c.ChunkSeq,
				Content:        c.Content,
				Column9:        VectorText(c.Embedding),
				EmbeddingModel: model,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// SearchContentChunks returns the org's top-k content chunks by cosine
// distance (current site versions and skills only).
func (s *Store) SearchContentChunks(ctx context.Context, t Tenant, embedding []float32, model string, k int32) ([]ContentChunk, error) {
	var out []ContentChunk
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.SearchContentChunks(ctx, db.SearchContentChunksParams{
			OrgID:          t.OrgID,
			Column2:        VectorText(embedding),
			EmbeddingModel: model,
			Limit:          k,
		})
		if err != nil {
			return err
		}
		out = make([]ContentChunk, 0, len(rows))
		for _, r := range rows {
			out = append(out, ContentChunk{
				ID:         r.ID,
				SourceKind: r.SourceKind,
				Path:       r.Path,
				ChunkSeq:   r.ChunkSeq,
				Content:    r.Content,
				SiteSlug:   r.SiteSlug.String,
				SkillSlug:  r.SkillSlug.String,
				Distance:   r.Distance,
			})
		}
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------------------

func memoryFromUpsert(r db.UpsertOrgMemoryRow) OrgMemory {
	return memoryFromParts(r.ID, r.OrgID, r.Kind, r.Content, r.ContentHash, r.EmbeddingModel, r.SourceKind, r.SourceID, r.SourceTool, r.Pinned, r.Disabled, r.CreatedBy, r.CreatedAt, r.UpdatedAt, r.LastUsedAt)
}

func memoryFromParts(id, orgID, kind, content, hash, model, sourceKind string, sourceID *string, sourceTool pgtype.Text, pinned, disabled bool, createdBy *string, createdAt, updatedAt time.Time, lastUsed pgtype.Timestamptz) OrgMemory {
	m := OrgMemory{
		ID:             id,
		OrgID:          orgID,
		Kind:           kind,
		Content:        content,
		ContentHash:    hash,
		EmbeddingModel: model,
		SourceKind:     sourceKind,
		SourceID:       sourceID,
		Pinned:         pinned,
		Disabled:       disabled,
		CreatedBy:      createdBy,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}
	if sourceTool.Valid {
		m.SourceTool = sourceTool.String
	}
	if lastUsed.Valid {
		ts := lastUsed.Time
		m.LastUsedAt = &ts
	}
	return m
}
