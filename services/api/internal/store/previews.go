// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// host_routes.kind values. Canonical/custom rows follow the site's live
// version; preview rows pin one draft version and expire.
const (
	RouteKindCanonical = "canonical"
	RouteKindCustom    = "custom"
	RouteKindPreview   = "preview"
)

// PreviewResult is returned by CreatePreviewRoute: the preview host, the route
// value the caller projects to the edge, and the deadline both the host_routes
// row and site_versions.preview_expires_at now carry.
type PreviewResult struct {
	Host      string
	Route     projection.RouteValue
	ExpiresAt time.Time
}

// CreatePreviewRoute registers (or renews) the time-limited preview host for
// one site version: `<shortVersionID>--<org>--<slug>.<ContentDomain>` pinned to
// exactly that version, expiring ttl from now. Calling it again extends the
// deadline and re-registers an expired/deleted preview (the draft's blobs +
// manifest stay in R2 under the draft-retention GC policy, so re-creation is
// one row + one KV write).
//
// The route write to KV is the CALLER's job (post-commit, like Publish); this
// only makes Postgres authoritative. Confused-deputy guards mirror Publish:
// the site must belong to the active tenant and the version to the site.
func (s *Store) CreatePreviewRoute(ctx context.Context, t Tenant, siteID, versionID string, ttl time.Duration) (PreviewResult, error) {
	var res PreviewResult
	err := s.withTxRaw(ctx, t, func(tx pgx.Tx, q *db.Queries) error {
		site, err := q.GetSite(ctx, db.GetSiteParams{ID: siteID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if site.OrgID != t.OrgID {
			return ErrNotFound
		}
		ver, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: versionID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if ver.SiteID != siteID || ver.OrgID != t.OrgID {
			return ErrVersionMismatch
		}
		if ver.Status != "ready" {
			return ErrVersionMismatch
		}

		orgSlug, err := orgSlugTx(ctx, tx, t.OrgID)
		if err != nil {
			return err
		}
		host := projection.PreviewHostForSite(versionID, orgSlug, site.Slug)
		expiresAt := time.Now().UTC().Add(ttl)

		vid := versionID
		if err := q.UpsertPreviewRoute(ctx, db.UpsertPreviewRouteParams{
			Host:      host,
			OrgID:     t.OrgID,
			SiteID:    siteID,
			VersionID: &vid,
			ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
		}); err != nil {
			if uniqueViolation(err, "") {
				return ErrHostTaken
			}
			return err
		}
		// Belt-and-braces: the upsert's WHERE guards silently no-op when the host
		// row is owned by another (org, site) or is not a preview row. Re-read and
		// refuse rather than projecting a route for a host we don't own.
		hr, err := q.GetHostRoute(ctx, db.GetHostRouteParams{Host: host, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrHostTaken // owned by another org (invisible under RLS)
			}
			return err
		}
		if hr.SiteID != siteID || hr.OrgID != t.OrgID || hr.Kind != RouteKindPreview {
			return ErrHostTaken
		}

		if err := q.SetVersionPreviewExpiry(ctx, db.SetVersionPreviewExpiryParams{
			ID: versionID, PreviewExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true}, OrgID: t.OrgID,
		}); err != nil {
			return err
		}

		// The preview serves under the SITE's access mode (a gated site's draft is
		// gated too). Edge expiry is the earlier of the preview deadline and any
		// public link-expiry the site's policy carries.
		var policyExpiry string
		if pol, perr := q.GetSiteAccessPolicy(ctx, db.GetSiteAccessPolicyParams{SiteID: siteID, OrgID: t.OrgID}); perr == nil {
			policyExpiry = routeExpiry(site.AccessMode, accessPolicyFromDB(pol))
		} else if !isNoRows(perr) {
			return perr
		}
		planTier, err := q.GetPlanTier(ctx, t.OrgID)
		if err != nil {
			return err
		}

		res.Host = host
		res.ExpiresAt = expiresAt
		// Preview hosts stay chat-less: the panel narrates the live site, not
		// a draft under review.
		res.Route = routeValue(t.OrgID, siteID, versionID, site.AccessMode,
			earliestExpiry(policyExpiry, expiresAt), planTier, "")
		return nil
	})
	return res, err
}

// DeletePreviewRoutes drops every preview route of one version (explicit
// deletion; Publish does the same inline for the version it publishes). It
// returns the removed hosts so the caller also deletes their KV keys.
func (s *Store) DeletePreviewRoutes(ctx context.Context, t Tenant, siteID, versionID string) ([]string, error) {
	var hosts []string
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		site, err := q.GetSite(ctx, db.GetSiteParams{ID: siteID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if site.OrgID != t.OrgID {
			return ErrNotFound
		}
		ver, err := q.GetSiteVersion(ctx, db.GetSiteVersionParams{ID: versionID, OrgID: t.OrgID})
		if err != nil {
			if isNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		if ver.SiteID != siteID || ver.OrgID != t.OrgID {
			return ErrVersionMismatch
		}

		vid := versionID
		hosts, err = q.DeletePreviewRoutesForVersion(ctx, db.DeletePreviewRoutesForVersionParams{VersionID: &vid, OrgID: t.OrgID})
		if err != nil {
			return err
		}
		return q.SetVersionPreviewExpiry(ctx, db.SetVersionPreviewExpiryParams{
			ID: versionID, PreviewExpiresAt: pgtype.Timestamptz{}, OrgID: t.OrgID,
		})
	})
	return hosts, err
}

// DeleteOtherSitePreviewRoutes drops every preview route of the site EXCEPT the
// one pinning keepVersionID, so a site has at most one live preview at a time (a
// new AI draft supersedes the earlier drafts' previews). Returns the removed
// hosts so the caller deletes their KV keys. RLS scopes the delete to the tenant.
func (s *Store) DeleteOtherSitePreviewRoutes(ctx context.Context, t Tenant, siteID, keepVersionID string) ([]string, error) {
	var hosts []string
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		vid := keepVersionID
		var derr error
		hosts, derr = q.DeleteSitePreviewRoutesExcept(ctx, db.DeleteSitePreviewRoutesExceptParams{
			SiteID: siteID, KeepVersionID: &vid, OrgID: t.OrgID,
		})
		return derr
	})
	return hosts, err
}

// SweepExpiredPreviews deletes this org's preview routes whose deadline passed
// more than grace ago (bookkeeping; the edge already 410s them), returning the
// removed hosts so the caller deletes their KV keys. Run per org under the org's
// RLS tenant context by the ops sweep.
func (s *Store) SweepExpiredPreviews(ctx context.Context, t Tenant, olderThan time.Time) ([]string, error) {
	var hosts []string
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		var err error
		hosts, err = q.DeleteExpiredPreviewRoutes(ctx, db.DeleteExpiredPreviewRoutesParams{
			ExpiresAt: pgtype.Timestamptz{Time: olderThan, Valid: true},
			OrgID:     t.OrgID,
		})
		return err
	})
	return hosts, err
}

// UnreportedUsage is one AI ledger row the cloud meter has not acked (for the
// meter retry sweep). Kept vendor-neutral in core; the cloud meter consumes it.
type UnreportedUsage struct {
	RowID        string
	OrgID        string
	GenerationID string
	CostUSD      float64
}

// ListUnreportedAIUsage returns up to limit of this org's ledger rows the meter
// has not acked (reported_to_billing_at IS NULL), oldest first.
func (s *Store) ListUnreportedAIUsage(ctx context.Context, t Tenant, limit int32) ([]UnreportedUsage, error) {
	var out []UnreportedUsage
	err := s.withTx(ctx, t, func(q *db.Queries) error {
		rows, err := q.ListUnreportedAIUsage(ctx, db.ListUnreportedAIUsageParams{OrgID: t.OrgID, Limit: limit})
		if err != nil {
			return err
		}
		out = make([]UnreportedUsage, len(rows))
		for i, r := range rows {
			out[i] = UnreportedUsage{RowID: r.ID, OrgID: r.OrgID, GenerationID: r.OpenrouterGenerationID, CostUSD: r.CostUsd}
		}
		return nil
	})
	return out, err
}

// MarkAIUsageReported marks one ledger row as metered (the retry sweep calls it
// after a successful meter send).
func (s *Store) MarkAIUsageReported(ctx context.Context, t Tenant, rowID string) error {
	return s.withTx(ctx, t, func(q *db.Queries) error {
		return q.MarkAIUsageReported(ctx, db.MarkAIUsageReportedParams{ID: rowID, OrgID: t.OrgID})
	})
}

// earliestExpiry combines a policy link-expiry (RFC3339 or "") with the preview
// deadline, returning the RFC3339 instant the edge should enforce — the earlier
// of the two. A preview host always has a deadline.
func earliestExpiry(policyExpiry string, previewDeadline time.Time) string {
	deadline := previewDeadline.UTC().Format(time.RFC3339)
	if policyExpiry == "" {
		return deadline
	}
	if p, err := time.Parse(time.RFC3339, policyExpiry); err == nil && p.Before(previewDeadline) {
		return policyExpiry
	}
	return deadline
}
