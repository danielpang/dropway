// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package store is the MCP service's thin, org-scoped data layer. It runs every
// query as the non-BYPASSRLS `dropway_app` role inside a transaction that first
// sets the per-request tenant context (SET LOCAL app.current_org_id / _user_id),
// so a token for org A can only ever see org A's rows — the same isolation the
// rest of the platform relies on. Every query ALSO carries an explicit org_id
// predicate bound from the tenant: RLS is the backstop, not the only filter (a
// BYPASSRLS runtime role silently disables RLS — the July 2026 prod leak). It
// can't import services/api/internal/store (Go internal-package rules), so it
// carries its own minimal queries.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/middleware"
)

// Tenant is the authenticated caller's org + user (from the validated OAuth token).
type Tenant struct {
	OrgID  string
	UserID string
}

// Site is one of the org's deployed sites.
type Site struct {
	ID               string
	Slug             string
	AccessMode       string
	CurrentVersionID *string // nil until a version is published (site not live)
	Host             *string // a content/custom host for the site, if any
}

// siteCols is the shared SELECT list: site fields + one representative host.
// The host subselect pins hr.org_id to the site's org so a (hypothetical)
// cross-org route row can never surface here. Kinds are ranked so the STABLE
// canonical <org>--<slug> host wins over a verified custom domain, and both win
// over a time-limited preview host — previews EXPIRE, and plain `ORDER BY host`
// used to pick one whenever it sorted first (e.g. `6f5367d7--org--slug` before
// `org--slug`), handing MCP clients a URL that later dies (a blank/404 embed).
const siteCols = `s.id, s.slug, s.access_mode, s.current_version_id,
	(SELECT hr.host FROM app.host_routes hr WHERE hr.site_id = s.id AND hr.org_id = s.org_id
	 ORDER BY CASE hr.kind WHEN 'canonical' THEN 0 WHEN 'custom' THEN 1 ELSE 2 END, hr.host LIMIT 1)`

// ErrNotFound is returned when a site slug doesn't resolve under the tenant.
var ErrNotFound = errors.New("mcp/store: not found")

// Store wraps the pgx pool (connected as dropway_app).
type Store struct{ pool *pgxpool.Pool }

// New builds a Store over an existing pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Ping verifies the database is reachable (acquires a connection and round-trips).
// Used by /healthz so a misconfigured or unreachable DATABASE_URL fails the health
// check — and thus the deploy — instead of silently serving 403s on every DB-backed
// request (the exact failure mode that hid a wrong DATABASE_URL in production).
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// withTx runs fn inside a tx with the tenant RLS context set. Read-only here, so
// it always rolls back (no writes to commit) — RLS still applies to the reads.
func (s *Store) withTx(ctx context.Context, t Tenant, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Bridge pgx.Tx (Exec → pgconn.CommandTag) to the middleware's pgx-agnostic
	// TenantTx (Exec → any) so we reuse the audited set_config RLS semantics.
	if err := middleware.SetTenantContext(ctx, txAdapter{tx}, t.UserID, t.OrgID); err != nil {
		return err
	}
	return fn(tx)
}

// txAdapter bridges pgx.Tx to middleware.TenantTx.
type txAdapter struct{ tx pgx.Tx }

func (a txAdapter) Exec(ctx context.Context, sql string, args ...any) (any, error) {
	return a.tx.Exec(ctx, sql, args...)
}

// MCPEnabled reports the org's mcp_enabled switch (the admin/owner kill-switch).
func (s *Store) MCPEnabled(ctx context.Context, t Tenant) (bool, error) {
	var enabled bool
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT mcp_enabled FROM app.org_meta WHERE id = $1`, t.OrgID).Scan(&enabled)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return enabled, err
}

// ListSites returns the org's sites (explicit org filter; RLS is the backstop).
func (s *Store) ListSites(ctx context.Context, t Tenant) ([]Site, error) {
	var sites []Site
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+siteCols+` FROM app.sites s WHERE s.org_id = $1 ORDER BY s.slug`, t.OrgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var st Site
			if err := rows.Scan(&st.ID, &st.Slug, &st.AccessMode, &st.CurrentVersionID, &st.Host); err != nil {
				return err
			}
			sites = append(sites, st)
		}
		return rows.Err()
	})
	return sites, err
}

// SiteBySlug resolves one site by slug under the tenant, or ErrNotFound.
func (s *Store) SiteBySlug(ctx context.Context, t Tenant, slug string) (Site, error) {
	var st Site
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT `+siteCols+` FROM app.sites s WHERE s.slug = $1 AND s.org_id = $2`, slug, t.OrgID).
			Scan(&st.ID, &st.Slug, &st.AccessMode, &st.CurrentVersionID, &st.Host)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Site{}, ErrNotFound
	}
	return st, err
}

// --- chat logs ------------------------------------------------------------------

// ChatLog is a shared chat log ("Share This Session") — here always one attached
// to a site (the MCP read path resolves logs via their site).
type ChatLog struct {
	ID           string
	SiteID       *string
	Title        string
	SourceTool   string
	PanelEnabled bool
	MessageCount int64
	CreatedBy    string
	CreatedAt    time.Time
}

// ChatMessage is one chat-log entry: a conversation turn (kind "chat") or an
// LLM action annotation (kind "action", Meta carries the raw jsonb).
type ChatMessage struct {
	Seq       int32
	Role      string
	Kind      string
	Content   string
	Meta      []byte  // raw jsonb of a kind="action" row; nil otherwise
	VersionID *string // the site deploy version current at append time, if any
	CreatedAt time.Time
}

// ChatLogBySite resolves the site's attached chat log under the tenant, or
// ErrNotFound (site has no attached log — or no such site in this org).
func (s *Store) ChatLogBySite(ctx context.Context, t Tenant, siteID string) (ChatLog, error) {
	var l ChatLog
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT l.id, l.site_id, l.title, l.source_tool, l.panel_enabled,
			        (SELECT COUNT(*) FROM app.chat_messages m WHERE m.chat_log_id = l.id AND m.org_id = l.org_id),
			        l.created_by, l.created_at
			 FROM app.chat_logs l WHERE l.site_id = $1 AND l.org_id = $2`, siteID, t.OrgID).
			Scan(&l.ID, &l.SiteID, &l.Title, &l.SourceTool, &l.PanelEnabled,
				&l.MessageCount, &l.CreatedBy, &l.CreatedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ChatLog{}, ErrNotFound
	}
	return l, err
}

// ListChatMessages returns a chat log's messages in seq order (RLS-filtered to
// the tenant).
func (s *Store) ListChatMessages(ctx context.Context, t Tenant, chatLogID string) ([]ChatMessage, error) {
	var msgs []ChatMessage
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT m.seq, m.role, m.kind, m.content, m.meta, m.version_id, m.created_at
			 FROM app.chat_messages m WHERE m.chat_log_id = $1 AND m.org_id = $2 ORDER BY m.seq`, chatLogID, t.OrgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m ChatMessage
			if err := rows.Scan(&m.Seq, &m.Role, &m.Kind, &m.Content, &m.Meta, &m.VersionID, &m.CreatedAt); err != nil {
				return err
			}
			msgs = append(msgs, m)
		}
		return rows.Err()
	})
	return msgs, err
}

// --- skills -------------------------------------------------------------------

// Skill is one of the org's shared Claude skills.
type Skill struct {
	ID          string
	Slug        string
	Title       string // '' when unset (clients fall back to the slug)
	Description string
	OwnerUserID string
	// CurrentVersionID is nil until an upload has been finalized (the list paths
	// only surface finalized skills; SkillBySlug also resolves unfinalized ones so
	// upload_skill can reuse an existing row instead of hitting a slug conflict).
	CurrentVersionID *string
	// SizeBytes is the current version's total size (0 until first upload).
	SizeBytes int64
	// Version is the current version's monotonic number (0 until first upload);
	// check_skill_updates compares a held copy's version against it.
	Version   int32
	Folders   []SkillFolderRef
	CreatedAt time.Time
}

// SkillFolderRef is one folder membership as seen from a skill.
type SkillFolderRef struct {
	FolderID string
	Slug     string
	Title    string
	IsPreset bool
}

// SkillFolder is one of the org's admin-curated skill folders.
type SkillFolder struct {
	ID        string
	Slug      string
	Title     string
	ItemCount int64
}

// skillCols is the shared SELECT list: skill fields + the current version's size.
// Requires the `LEFT JOIN app.skill_versions v ON v.id = sk.current_version_id`
// alias in the FROM clause.
const skillCols = `sk.id, sk.slug, COALESCE(sk.title, ''), COALESCE(sk.description, ''),
	sk.owner_user_id, sk.current_version_id, COALESCE(v.size_bytes, 0), COALESCE(v.version_no, 0), sk.created_at`

// skillFrom is the FROM clause skillCols expects.
const skillFrom = ` FROM app.skills sk
	LEFT JOIN app.skill_versions v ON v.id = sk.current_version_id`

func scanSkill(row pgx.Row) (Skill, error) {
	var sk Skill
	err := row.Scan(&sk.ID, &sk.Slug, &sk.Title, &sk.Description,
		&sk.OwnerUserID, &sk.CurrentVersionID, &sk.SizeBytes, &sk.Version, &sk.CreatedAt)
	return sk, err
}

// attachSkillFolders fills Folders for every skill in one query (no N+1).
func attachSkillFolders(ctx context.Context, tx pgx.Tx, orgID string, skills []Skill) error {
	if len(skills) == 0 {
		return nil
	}
	ids := make([]string, len(skills))
	byID := make(map[string]int, len(skills))
	for i, sk := range skills {
		ids[i] = sk.ID
		byID[sk.ID] = i
	}
	rows, err := tx.Query(ctx,
		`SELECT fi.skill_id, f.id, f.slug, f.title, fi.is_preset
		 FROM app.skill_folder_items fi
		 JOIN app.skill_folders f ON f.id = fi.folder_id
		 WHERE fi.skill_id = ANY($1) AND fi.org_id = $2 AND f.org_id = $2
		 ORDER BY f.slug`, ids, orgID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var skillID string
		var ref SkillFolderRef
		if err := rows.Scan(&skillID, &ref.FolderID, &ref.Slug, &ref.Title, &ref.IsPreset); err != nil {
			return err
		}
		if i, ok := byID[skillID]; ok {
			skills[i].Folders = append(skills[i].Folders, ref)
		}
	}
	return rows.Err()
}

// ListSkills returns the org's FINALIZED skills matching the filters, each with
// its folder memberships. query (” = all) ILIKE-matches slug/title/description;
// folderSlug (” = any) restricts to that folder's members; presetsOnly
// additionally requires the membership's is_preset flag.
func (s *Store) ListSkills(ctx context.Context, t Tenant, query, folderSlug string, presetsOnly bool) ([]Skill, error) {
	var skills []Skill
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+skillCols+skillFrom+`
			 WHERE sk.current_version_id IS NOT NULL
			   AND sk.org_id = $4
			   AND ($1::text = ''
			        OR sk.slug ILIKE '%' || $1 || '%'
			        OR COALESCE(sk.title, '') ILIKE '%' || $1 || '%'
			        OR COALESCE(sk.description, '') ILIKE '%' || $1 || '%')
			   AND (($2::text = '' AND NOT $3::boolean)
			        OR EXISTS (
			             SELECT 1 FROM app.skill_folder_items fi
			             JOIN app.skill_folders f ON f.id = fi.folder_id
			             WHERE fi.skill_id = sk.id
			               AND fi.org_id = $4 AND f.org_id = $4
			               AND ($2::text = '' OR f.slug = $2)
			               AND (NOT $3::boolean OR fi.is_preset)))
			 ORDER BY sk.slug`, query, folderSlug, presetsOnly, t.OrgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			sk, err := scanSkill(rows)
			if err != nil {
				return err
			}
			skills = append(skills, sk)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return attachSkillFolders(ctx, tx, t.OrgID, skills)
	})
	return skills, err
}

// SkillBySlug resolves one skill by slug under the tenant (finalized or not),
// or ErrNotFound.
func (s *Store) SkillBySlug(ctx context.Context, t Tenant, slug string) (Skill, error) {
	var sk Skill
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		var err error
		sk, err = scanSkill(tx.QueryRow(ctx,
			`SELECT `+skillCols+skillFrom+` WHERE sk.slug = $1 AND sk.org_id = $2`, slug, t.OrgID))
		if err != nil {
			return err
		}
		one := []Skill{sk}
		if err := attachSkillFolders(ctx, tx, t.OrgID, one); err != nil {
			return err
		}
		sk = one[0]
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Skill{}, ErrNotFound
	}
	return sk, err
}

// ListSkillFolders returns the org's skill folders with their item counts.
func (s *Store) ListSkillFolders(ctx context.Context, t Tenant) ([]SkillFolder, error) {
	var folders []SkillFolder
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT f.id, f.slug, f.title, COUNT(fi.skill_id)
			 FROM app.skill_folders f
			 LEFT JOIN app.skill_folder_items fi ON fi.folder_id = f.id
			 WHERE f.org_id = $1
			 GROUP BY f.id, f.slug, f.title
			 ORDER BY f.slug`, t.OrgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var f SkillFolder
			if err := rows.Scan(&f.ID, &f.Slug, &f.Title, &f.ItemCount); err != nil {
				return err
			}
			folders = append(folders, f)
		}
		return rows.Err()
	})
	return folders, err
}

// SkillFolderBySlug resolves one skill folder by slug under the tenant, or
// ErrNotFound.
func (s *Store) SkillFolderBySlug(ctx context.Context, t Tenant, slug string) (SkillFolder, error) {
	var f SkillFolder
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT f.id, f.slug, f.title, COUNT(fi.skill_id)
			 FROM app.skill_folders f
			 LEFT JOIN app.skill_folder_items fi ON fi.folder_id = f.id
			 WHERE f.slug = $1 AND f.org_id = $2
			 GROUP BY f.id, f.slug, f.title`, slug, t.OrgID).
			Scan(&f.ID, &f.Slug, &f.Title, &f.ItemCount)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SkillFolder{}, ErrNotFound
	}
	return f, err
}

// ListFolderSkills returns every FINALIZED skill in a folder (the bulk-download
// set), decorated like ListSkills.
func (s *Store) ListFolderSkills(ctx context.Context, t Tenant, folderID string) ([]Skill, error) {
	var skills []Skill
	err := s.withTx(ctx, t, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT `+skillCols+`
			 FROM app.skill_folder_items fi
			 JOIN app.skills sk ON sk.id = fi.skill_id
			 LEFT JOIN app.skill_versions v ON v.id = sk.current_version_id
			 WHERE fi.folder_id = $1 AND fi.org_id = $2 AND sk.org_id = $2
			   AND sk.current_version_id IS NOT NULL
			 ORDER BY sk.slug`, folderID, t.OrgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			sk, err := scanSkill(rows)
			if err != nil {
				return err
			}
			skills = append(skills, sk)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return attachSkillFolders(ctx, tx, t.OrgID, skills)
	})
	return skills, err
}
