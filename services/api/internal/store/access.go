// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// ---------------------------------------------------------------------------
// Phase-2 sentinels (mapped to HTTP statuses by the handlers).
// ---------------------------------------------------------------------------

var (
	// ErrInvalidMode is returned for an unknown access mode.
	ErrInvalidMode = errors.New("store: invalid access mode")
	// ErrPolicyExpired is returned when a site's access policy expires_at is in the
	// past, so an edge token must NOT be minted ("link expired").
	ErrPolicyExpired = errors.New("store: access policy expired")
	// ErrNotAllowlisted is returned when a viewer's verified email is not on a
	// site's allowlist.
	ErrNotAllowlisted = errors.New("store: email not on allowlist")
	// ErrNotOrgMember is returned when an org_only site is requested by a viewer
	// whose org does not own the site.
	ErrNotOrgMember = errors.New("store: viewer is not a member of the site's org")
	// ErrWrongPassword is returned when a password-mode check fails.
	ErrWrongPassword = errors.New("store: incorrect password")
	// ErrNoPolicy is returned when a non-public site has no access policy row.
	ErrNoPolicy = errors.New("store: site has no access policy")
)

// AccessPolicy is the API-facing view of a site's gating config.
type AccessPolicy struct {
	SiteID       string
	OrgID        string
	Mode         string
	PasswordHash string // empty unless mode=password
	ExpiresAt    *time.Time
	Unlisted     bool
	UpdatedAt    time.Time
}

// AllowlistEntry is one email grant on a site's allowlist.
type AllowlistEntry struct {
	ID         string
	OrgID      string
	SiteID     string
	Email      string
	IsExternal bool
	ClaimedAt  *time.Time
	ClaimedBy  *string
	CreatedAt  time.Time
}

// SetAccessParams configures a site's access mode + policy in one operation.
type SetAccessParams struct {
	SiteID       string
	Mode         string
	PasswordHash string     // already-hashed; only used when Mode=password
	ExpiresAt    *time.Time // optional link expiry
	Unlisted     bool       // public-tier unlisted flag
}

// SetSiteAccess sets a site's access_mode (the RouteValue source) AND upserts the
// matching site_access_policy row in one transaction (confused-deputy guard: the
// site must belong to the active tenant). It returns the resulting route value so
// the caller can rewrite the edge projection (mode + expires_at).
//
// Defense in depth: both UPDATE app.sites.access_mode and the policy upsert fire
// the external-sharing trigger (0004), which rejects mode='public' under a false
// org policy — surfaced as ErrExternalSharingDisabled. password_hash is stored
// only for password mode (never plaintext; the caller hashes).
func (s *Store) SetSiteAccess(ctx context.Context, t Tenant, p SetAccessParams) (PublishResult, error) {
	switch p.Mode {
	case projection.AccessPublic, projection.AccessPassword, projection.AccessAllowlist, projection.AccessOrgOnly:
	default:
		return PublishResult{}, ErrInvalidMode
	}

	var res PublishResult
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		site, err := q.GetSite(ctx, p.SiteID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if site.OrgID != t.OrgID {
			return ErrNotFound
		}

		// Flip the site's access_mode (the RouteValue source). The trigger rejects
		// 'public' under a false org policy → 23514 → ErrExternalSharingDisabled.
		if err := q.SetSiteAccessMode(ctx, db.SetSiteAccessModeParams{
			ID: p.SiteID, AccessMode: p.Mode,
		}); err != nil {
			if checkViolation(err) {
				return ErrExternalSharingDisabled
			}
			return err
		}

		// Upsert the policy mirror (mode + password_hash + expires_at + unlisted).
		pwHash := pgtype.Text{}
		if p.Mode == projection.AccessPassword && p.PasswordHash != "" {
			pwHash = pgtype.Text{String: p.PasswordHash, Valid: true}
		}
		exp := pgtype.Timestamptz{}
		if p.ExpiresAt != nil {
			exp = pgtype.Timestamptz{Time: *p.ExpiresAt, Valid: true}
		}
		row, err := q.UpsertSiteAccessPolicy(ctx, db.UpsertSiteAccessPolicyParams{
			SiteID:       p.SiteID,
			OrgID:        t.OrgID,
			Mode:         p.Mode,
			PasswordHash: pwHash,
			ExpiresAt:    exp,
			Unlisted:     p.Unlisted,
		})
		if err != nil {
			if checkViolation(err) {
				return ErrExternalSharingDisabled
			}
			return err
		}

		// Build the route values the caller projects (only when there is a live
		// version to serve; otherwise Routes is empty and the caller skips the write).
		res.Site = siteFromDB(site)
		res.Site.AccessMode = p.Mode
		{
			expiresAt := routeExpiry(p.Mode, accessPolicyFromDB(row))
			// Rewrite EVERY host of the site (canonical + verified custom domains),
			// not just the canonical one — each custom host has its own route:<host>
			// KV entry, and leaving it at the old access_mode keeps the Worker serving
			// the custom host under the OLD tier after the policy tightened (FIX 1).
			// Preview hosts are rewritten too (a gated site's draft must gate the
			// same way), but keep their pinned version + preview deadline. They also
			// exist for sites with NO live version (an unpublished AI-created site).
			hostRoutes, err := q.ListHostRoutesForSite(ctx, p.SiteID)
			if err != nil {
				return err
			}
			// Carry the attached chat log (v4 chat_id) through the rewrite —
			// an access flip must not strip the "How this was made" panel.
			// Preview routes stay chat-less (parity with publish/rebuild).
			chatID, err := chatIDForSiteTx(ctx, q, p.SiteID)
			if err != nil {
				return err
			}
			for _, hr := range hostRoutes {
				var rv projection.RouteValue
				switch {
				case hr.Kind == RouteKindPreview && hr.VersionID != nil:
					var deadline time.Time
					if hr.ExpiresAt.Valid {
						deadline = hr.ExpiresAt.Time
					}
					rv = projection.RouteValue{
						OrgID:         t.OrgID,
						SiteID:        p.SiteID,
						VersionID:     *hr.VersionID,
						AccessMode:    p.Mode,
						SchemaVersion: projection.SchemaVersion,
						ExpiresAt:     earliestExpiry(expiresAt, deadline),
					}
				case site.CurrentVersionID != nil:
					rv = projection.RouteValue{
						OrgID:         t.OrgID,
						SiteID:        p.SiteID,
						VersionID:     *site.CurrentVersionID,
						AccessMode:    p.Mode,
						SchemaVersion: projection.SchemaVersion,
						ExpiresAt:     expiresAt,
						ChatID:        chatID,
					}
				default:
					continue // no live version and not a preview → no route to rewrite
				}
				res.Routes = append(res.Routes, RouteUpdate{Host: hr.Host, Route: rv})
			}
			// Keep the canonical Host/Route populated for back-compat (the historical
			// single-route shape); the handler now iterates Routes.
			if site.CurrentVersionID != nil {
				orgSlug, err := orgSlugTx(ctx, tx, t.OrgID)
				if err != nil {
					return err
				}
				res.Host = projection.HostForSite(orgSlug, site.Slug)
				res.Route = projection.RouteValue{
					OrgID:         t.OrgID,
					SiteID:        p.SiteID,
					VersionID:     *site.CurrentVersionID,
					AccessMode:    p.Mode,
					SchemaVersion: projection.SchemaVersion,
					ExpiresAt:     expiresAt,
					ChatID:        chatID,
				}
			}
		}
		return nil
	})
	return res, err
}

// GetSiteAccessPolicy returns a site's access policy (ErrNoPolicy if none).
func (s *Store) GetSiteAccessPolicy(ctx context.Context, t Tenant, siteID string) (AccessPolicy, error) {
	var out AccessPolicy
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		row, err := q.GetSiteAccessPolicy(ctx, siteID)
		if err != nil {
			if isNoRows(err) {
				return ErrNoPolicy
			}
			return err
		}
		out = accessPolicyFromDB(row)
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------------------
// Allowlist CRUD
// ---------------------------------------------------------------------------

// AddAllowlistEntryParams adds one email grant to a site.
type AddAllowlistEntryParams struct {
	SiteID     string
	Email      string
	IsExternal bool
}

// AddAllowlistEntry adds (or re-adds, resetting the claim) an email grant. The
// external-sharing trigger rejects is_external=true under a false org policy
// (ErrExternalSharingDisabled). The site must belong to the active tenant.
func (s *Store) AddAllowlistEntry(ctx context.Context, t Tenant, p AddAllowlistEntryParams) (AllowlistEntry, error) {
	email := normalizeEmail(p.Email)
	if email == "" {
		return AllowlistEntry{}, ErrBadEmail
	}
	var out AllowlistEntry
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		site, err := q.GetSite(ctx, p.SiteID)
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if site.OrgID != t.OrgID {
			return ErrNotFound
		}
		row, err := q.UpsertAllowlistEntry(ctx, db.UpsertAllowlistEntryParams{
			OrgID:      t.OrgID,
			SiteID:     p.SiteID,
			Email:      email,
			IsExternal: p.IsExternal,
		})
		if err != nil {
			if checkViolation(err) {
				return ErrExternalSharingDisabled
			}
			return err
		}
		out = allowlistEntryFromDB(row)
		return nil
	})
	return out, err
}

// RemoveAllowlistEntry deletes an email grant from a site.
func (s *Store) RemoveAllowlistEntry(ctx context.Context, t Tenant, siteID, email string) error {
	email = normalizeEmail(email)
	return s.withTx(ctx, t, func(q *db.Queries) error {
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
		return q.DeleteAllowlistEntry(ctx, db.DeleteAllowlistEntryParams{SiteID: siteID, Email: email})
	})
}

// ListAllowlistEntries returns a site's allowlist.
func (s *Store) ListAllowlistEntries(ctx context.Context, t Tenant, siteID string) ([]AllowlistEntry, error) {
	var out []AllowlistEntry
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
		rows, err := q.ListAllowlistEntries(ctx, siteID)
		if err != nil {
			return err
		}
		out = make([]AllowlistEntry, len(rows))
		for i, r := range rows {
			out[i] = allowlistEntryFromDB(r)
		}
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------------------
// conversions + helpers
// ---------------------------------------------------------------------------

// ErrBadEmail is returned when an allowlist email is empty/malformed.
var ErrBadEmail = errors.New("store: invalid email")

func accessPolicyFromDB(r db.AppSiteAccessPolicy) AccessPolicy {
	p := AccessPolicy{
		SiteID:    r.SiteID,
		OrgID:     r.OrgID,
		Mode:      r.Mode,
		Unlisted:  r.Unlisted,
		UpdatedAt: r.UpdatedAt,
	}
	if r.PasswordHash.Valid {
		p.PasswordHash = r.PasswordHash.String
	}
	if r.ExpiresAt.Valid {
		t := r.ExpiresAt.Time
		p.ExpiresAt = &t
	}
	return p
}

func allowlistEntryFromDB(r db.AppAllowlistEntry) AllowlistEntry {
	e := AllowlistEntry{
		ID:         r.ID,
		OrgID:      r.OrgID,
		SiteID:     r.SiteID,
		Email:      r.Email,
		IsExternal: r.IsExternal,
		ClaimedBy:  r.ClaimedByUserID,
		CreatedAt:  r.CreatedAt,
	}
	if r.ClaimedAt.Valid {
		t := r.ClaimedAt.Time
		e.ClaimedAt = &t
	}
	return e
}

// routeExpiry returns the RFC3339 expires_at to put in the RouteValue, but ONLY
// for the public tier — identity-gated modes (password/allowlist/org_only) enforce
// expiry at mint time in the Go API, not at the edge (the edge token is short-lived
// already). For public/unlisted, the edge enforces expiry from the RouteValue.
func routeExpiry(mode string, p AccessPolicy) string {
	if mode != projection.AccessPublic {
		return ""
	}
	if p.ExpiresAt == nil {
		return ""
	}
	return p.ExpiresAt.UTC().Format(time.RFC3339)
}

// normalizeEmail lowercases + trims an email for case-insensitive matching (the
// allowlist (site,email) unique key and the verified-email claim compare on this).
func normalizeEmail(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}
