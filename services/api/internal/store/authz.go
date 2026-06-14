// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/services/api/internal/store/db"
)

// ResolvedHost is the routing identity of a content host, resolved across orgs by
// the RLS-bypassing app.resolve_host() function (so a site shared with a viewer in
// another org still resolves). It carries NO secrets — the password_hash is read
// separately under the site's tenant context.
type ResolvedHost struct {
	Host       string
	SiteID     string
	OrgID      string
	Slug       string
	AccessMode string
	VersionID  *string
}

// ErrHostNotFound is returned when a content host resolves to no site.
var ErrHostNotFound = errors.New("store: host not found")

// resolveHost calls the SECURITY DEFINER app.resolve_host(host) inside an
// already-open tx (so the call participates in the request transaction). It is raw
// pgx because sqlc can't type a RETURNS TABLE function. The function bypasses RLS,
// so it returns the row even when the active tenant doesn't own the host.
func resolveHost(ctx context.Context, tx pgx.Tx, host string) (ResolvedHost, error) {
	var r ResolvedHost
	row := tx.QueryRow(ctx,
		`SELECT host, site_id, org_id, slug, access_mode, version_id FROM app.resolve_host($1)`, host)
	if err := row.Scan(&r.Host, &r.SiteID, &r.OrgID, &r.Slug, &r.AccessMode, &r.VersionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ResolvedHost{}, ErrHostNotFound
		}
		return ResolvedHost{}, err
	}
	return r, nil
}

// MintViewer carries the VERIFIED viewer identity from the Better Auth JWT that
// the /authz mint endpoint authorizes. EmailVerified must be true for an allowlist
// match to be honored (ARCHITECTURE.md §10 [HIGH]).
type MintViewer struct {
	UserID        string
	OrgID         string
	Email         string
	EmailVerified bool
}

// MintDecision is the result of authorizing a mint: the site identity + the mode
// the resulting edge token should carry, plus the canonical content host (so the
// caller binds aud to exactly that host, never a free-form return URL — §10).
type MintDecision struct {
	Host    string
	SiteID  string
	OrgID   string
	Mode    string
	Subject string // viewer user id (org_only/allowlist)
}

// AuthorizeMint resolves host and authorizes the viewer for an org_only/allowlist
// site, per the AUTHZ RULES (source of truth — claims are a hint, re-checked live):
//
//   - org_only:  viewer.OrgID == site.org_id (membership is re-checked by the
//     caller via the member table before this — see store.MemberRole).
//   - allowlist: viewer's VERIFIED email ∈ allowlist_entries(site); a pending entry
//     is CLAIMED (user_id + claimed_at) on first match; an is_external entry
//     requires the site org's allow_external_sharing=true.
//   - expiry:    if the policy's expires_at is past → ErrPolicyExpired (refuse).
//
// password mode is NOT handled here (it takes a password, not a viewer) — see
// AuthorizePassword. public needs no token. The whole decision runs in ONE tx: the
// host is resolved cross-org (definer function), then the policy/allowlist are read
// + the claim is written under the SITE's tenant context (its own data).
func (s *Store) AuthorizeMint(ctx context.Context, v MintViewer, host string) (MintDecision, error) {
	if v.UserID == "" || v.OrgID == "" {
		return MintDecision{}, ErrMissingViewer
	}

	var out MintDecision
	// We run under the VIEWER's tenant context first only to open a valid tx; the
	// resolve_host call bypasses RLS. We then re-establish the SITE's tenant context
	// for the policy/allowlist reads+writes (its own org owns that data).
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MintDecision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Resolve cross-org. Set a benign tenant context first (the viewer's) so the tx
	// has GUCs set; resolve_host ignores RLS anyway.
	if err := setTenant(ctx, tx, v.UserID, v.OrgID); err != nil {
		return MintDecision{}, err
	}
	rh, err := resolveHost(ctx, tx, host)
	if err != nil {
		return MintDecision{}, err
	}
	if rh.VersionID == nil {
		// No live version → nothing to serve; treat as not found rather than mint a
		// token for an empty site.
		return MintDecision{}, ErrHostNotFound
	}

	// Switch to the SITE's tenant context for its own policy/allowlist data.
	if err := setTenant(ctx, tx, v.UserID, rh.OrgID); err != nil {
		return MintDecision{}, err
	}
	q := db.New(tx)

	// Load the policy to enforce expiry (and confirm the mode).
	pol, err := q.GetSiteAccessPolicy(ctx, rh.SiteID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return MintDecision{}, err
	}
	mode := rh.AccessMode
	if err == nil {
		mode = pol.Mode
		if pol.ExpiresAt.Valid && !pol.ExpiresAt.Time.After(time.Now()) {
			return MintDecision{}, ErrPolicyExpired
		}
	}

	switch mode {
	case projection.AccessOrgOnly:
		// The JWT org_id claim must match the site's org (fast hint), AND the viewer
		// must be a CURRENT member of the site's org per the live auth.member table.
		// Membership is authoritative: a user removed from the org but still holding
		// an unexpired JWT must NOT be able to mint an edge token (FIX 2 /
		// ARCHITECTURE.md §6 "org-only → viewer ∈ member(site.org_id) (re-check)").
		if v.OrgID != rh.OrgID {
			return MintDecision{}, ErrNotOrgMember
		}
		if err := s.requireLiveMembership(ctx, rh.OrgID, v.UserID); err != nil {
			return MintDecision{}, err
		}

	case projection.AccessAllowlist:
		if !v.EmailVerified || v.Email == "" {
			return MintDecision{}, ErrNotAllowlisted
		}
		email := normalizeEmail(v.Email)
		entry, err := q.GetAllowlistEntryByEmail(ctx, db.GetAllowlistEntryByEmailParams{
			SiteID: rh.SiteID, Email: email,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return MintDecision{}, ErrNotAllowlisted
			}
			return MintDecision{}, err
		}
		// External grants require the SITE org's policy to permit external sharing.
		if entry.IsExternal {
			meta, err := q.GetOrgMeta(ctx, rh.OrgID)
			if err != nil {
				return MintDecision{}, err
			}
			if !meta.AllowExternalSharing {
				return MintDecision{}, ErrExternalSharingDisabled
			}
		}
		// Claim the grant on first match (idempotent: keeps the original claimant).
		uid := v.UserID
		if err := q.ClaimAllowlistEntry(ctx, db.ClaimAllowlistEntryParams{
			ID: entry.ID, ClaimedByUserID: &uid,
		}); err != nil {
			return MintDecision{}, err
		}

	case projection.AccessPassword:
		// password mode is minted via AuthorizePassword (anon), not here.
		return MintDecision{}, ErrPasswordModeUsesPasswordEndpoint

	default: // public or unknown
		return MintDecision{}, ErrNotGated
	}

	if err := tx.Commit(ctx); err != nil {
		return MintDecision{}, err
	}

	out = MintDecision{
		Host:    rh.Host,
		SiteID:  rh.SiteID,
		OrgID:   rh.OrgID,
		Mode:    mode,
		Subject: v.UserID,
	}
	return out, nil
}

// PasswordDecision is the result of authorizing a password-mode request. The
// resulting edge token carries an anon subject (no viewer identity).
type PasswordDecision struct {
	Host   string
	SiteID string
	OrgID  string
	Mode   string
}

// ResolveForPassword resolves host and returns the site identity + the stored
// bcrypt password hash for a password-mode site, enforcing expiry. The CALLER does
// the constant-time bcrypt compare (so the hash never leaves the store boundary
// without an explicit reason) and mints the anon token on success. A non-password
// site → ErrNotGated.
func (s *Store) ResolveForPassword(ctx context.Context, host string) (PasswordDecision, string, error) {
	var out PasswordDecision
	var hash string

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PasswordDecision{}, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// No viewer here; use a system-ish context where org==user is a valid uuid GUC
	// only after we know the site org. resolve_host bypasses RLS, so set the GUCs
	// to the resolved org right after.
	// First resolve with a placeholder tenant; resolve_host ignores RLS.
	if err := setTenantUnsafe(ctx, tx); err != nil {
		return PasswordDecision{}, "", err
	}
	rh, err := resolveHost(ctx, tx, host)
	if err != nil {
		return PasswordDecision{}, "", err
	}
	if rh.VersionID == nil {
		return PasswordDecision{}, "", ErrHostNotFound
	}

	// Read the policy under the site's tenant context.
	if err := setTenant(ctx, tx, rh.OrgID, rh.OrgID); err != nil {
		return PasswordDecision{}, "", err
	}
	q := db.New(tx)
	pol, err := q.GetSiteAccessPolicy(ctx, rh.SiteID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PasswordDecision{}, "", ErrNoPolicy
		}
		return PasswordDecision{}, "", err
	}
	if pol.Mode != projection.AccessPassword {
		return PasswordDecision{}, "", ErrNotGated
	}
	if pol.ExpiresAt.Valid && !pol.ExpiresAt.Time.After(time.Now()) {
		return PasswordDecision{}, "", ErrPolicyExpired
	}
	if !pol.PasswordHash.Valid || pol.PasswordHash.String == "" {
		return PasswordDecision{}, "", ErrNoPolicy
	}
	hash = pol.PasswordHash.String

	if err := tx.Commit(ctx); err != nil {
		return PasswordDecision{}, "", err
	}
	out = PasswordDecision{Host: rh.Host, SiteID: rh.SiteID, OrgID: rh.OrgID, Mode: pol.Mode}
	return out, hash, nil
}

// ---------------------------------------------------------------------------
// errors + tiny tx helpers
// ---------------------------------------------------------------------------

var (
	// ErrMissingViewer is returned when the mint viewer lacks user/org.
	ErrMissingViewer = errors.New("store: missing viewer identity")
	// ErrNotGated is returned when a host is public (no token needed) or an
	// unexpected mode is requested on a typed endpoint.
	ErrNotGated = errors.New("store: site is not gated for this exchange")
	// ErrPasswordModeUsesPasswordEndpoint signals the caller used the mint endpoint
	// for a password site; password sites mint via the /authz/password endpoint.
	ErrPasswordModeUsesPasswordEndpoint = errors.New("store: password site uses the password exchange")
)

// setTenant sets the per-tx RLS GUCs (mirrors middleware.SetTenantContext but uses
// pgx.Tx directly so authz can switch the tenant mid-tx after a cross-org resolve).
func setTenant(ctx context.Context, tx pgx.Tx, userID, orgID string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_user_id', $1, true)`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_org_id', $1, true)`, orgID); err != nil {
		return err
	}
	return nil
}

// setTenantUnsafe clears the tenant GUCs to empty (DEFAULT-DENY for normal tables).
// Only used immediately before a resolve_host() call, which bypasses RLS; any
// subsequent normal query under this context sees nothing until setTenant runs.
func setTenantUnsafe(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_user_id', '', true)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_org_id', '', true)`); err != nil {
		return err
	}
	return nil
}
