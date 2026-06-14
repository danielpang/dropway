// Package projection writes the edge routing projection — the KV value at
// `route:<host>` that the Cloudflare serving Worker reads to resolve a host to a
// live version and stream it from R2 (docs/ARCHITECTURE.md §3/§6/§8).
//
// Postgres is authoritative; this projection is a REBUILDABLE cache. The Go API
// is the ONLY writer (the Worker is read-only). RouteValue mirrors the single
// cross-language data contract in contracts/kv-route.schema.json (@shipped/
// contracts, SCHEMA_VERSION=1); a round-trip test asserts the Go shape matches
// the schema and the TS parser.
package projection

import (
	"context"
	"fmt"
	"time"
)

// SchemaVersion is the version of THIS contract shape. It MUST equal
// SCHEMA_VERSION in @shipped/contracts (contracts/src/index.ts) and the
// `schema_version` enforced by kv-route.schema.json. Bump in lock-step on any
// breaking change. (ARCHITECTURE.md §8, §13 row 11.)
//
// v1 → v2 (Phase 2): adds the optional `expires_at` (RFC3339) field so the
// Worker can enforce public/unlisted link expiry at the edge from the RouteValue
// (identity-gated expiry is refused at mint time in the Go API). The parse is
// kept backward compatible — a v1 value (no expires_at) is still accepted — but
// the Go API now writes v2.
const SchemaVersion = 2

// MinSchemaVersion is the oldest contract shape the parser still accepts. v1
// values (written by a Phase-1 Go API) carry no expires_at and are read as
// "never expires"; the Worker upgrades them on the next publish.
const MinSchemaVersion = 1

// Access modes mirror app.sites.access_mode and the enum in
// kv-route.schema.json. Phase 1 implements `public` only; the rest are Phase 2.
const (
	AccessPublic    = "public"
	AccessPassword  = "password"
	AccessAllowlist = "allowlist"
	AccessOrgOnly   = "org_only"
)

// RouteValue is the value stored at KV key `route:<host>`. Field names and JSON
// tags are kept in EXACT sync with kv-route.schema.json and the TS KVRouteValue
// interface — the round-trip test (projection_test.go) fails on drift.
//
// ExpiresAt (v2, Phase 2) is an OPTIONAL RFC3339 timestamp; when set, the Worker
// refuses to serve the host after it (public/unlisted link expiry enforced at the
// edge). Empty → no edge expiry. It is `omitempty` so a non-expiring route
// serializes byte-for-byte like a value without the field, keeping the contract
// compact and a v1↔v2 round-trip clean.
type RouteValue struct {
	OrgID         string `json:"org_id"`
	SiteID        string `json:"site_id"`
	VersionID     string `json:"version_id"`
	AccessMode    string `json:"access_mode"`
	SchemaVersion int    `json:"schema_version"`
	ExpiresAt     string `json:"expires_at,omitempty"`
}

// Validate checks the value is well-formed before it can be written, mirroring
// the schema's required fields + enum + schema_version constraint so a malformed
// projection can never be pushed to the edge.
func (v RouteValue) Validate() error {
	for name, val := range map[string]string{
		"org_id":     v.OrgID,
		"site_id":    v.SiteID,
		"version_id": v.VersionID,
	} {
		if val == "" {
			return fmt.Errorf("projection: route value missing %s", name)
		}
	}
	switch v.AccessMode {
	case AccessPublic, AccessPassword, AccessAllowlist, AccessOrgOnly:
	default:
		return fmt.Errorf("projection: invalid access_mode %q", v.AccessMode)
	}
	// Accept any supported version on the way in (MinSchemaVersion..SchemaVersion)
	// so a rebuild that replays a stored v1 value isn't rejected; the Go API only
	// ever WRITES SchemaVersion (set by the store), so new writes are always v2.
	if v.SchemaVersion < MinSchemaVersion || v.SchemaVersion > SchemaVersion {
		return fmt.Errorf("projection: schema_version %d not in [%d,%d]",
			v.SchemaVersion, MinSchemaVersion, SchemaVersion)
	}
	// expires_at, when present, must be a valid RFC3339 timestamp.
	if v.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, v.ExpiresAt); err != nil {
			return fmt.Errorf("projection: invalid expires_at %q: %w", v.ExpiresAt, err)
		}
	}
	return nil
}

// Writer is the edge-projection surface. The Go API calls PutRoute on publish and
// DeleteRoute on unshare/delete; RebuildFromDB re-pushes the entire projection
// from Postgres (the DR drill / drift reconciler — §13 row 8).
type Writer interface {
	// PutRoute upserts the route value for host (e.g.
	// "<slug>.shippedusercontent.com").
	PutRoute(ctx context.Context, host string, val RouteValue) error
	// DeleteRoute removes a host's route (unshare / delete).
	DeleteRoute(ctx context.Context, host string) error
	// RebuildFromDB clears the projection and re-writes it from the supplied
	// authoritative rows. routes maps host → RouteValue.
	RebuildFromDB(ctx context.Context, routes map[string]RouteValue) error
}

// HostForSlug returns the canonical Phase-1 content host for a site slug.
//
// SLUG SCHEME (documented decision): the site slug is the single DNS label under
// the content domain — `<slug>.shippedusercontent.com`. The serving Worker's
// `*.shippedusercontent.com` wildcard matches exactly one label, so a single-
// label host is required. app.sites enforces (org_id, slug) uniqueness, so slugs
// are unique WITHIN an org but not globally; the KV namespace is global, so the
// publish path treats the route key as the global host registry and refuses to
// overwrite a host already owned by a different org/site (see store.Store.Publish
// and the reserved-slug list). This keeps Phase 1 self-contained; a future phase
// can move to `<site>--<org>` two-token labels or a global slug reservation table
// without changing the Worker contract.
func HostForSlug(slug string) string {
	return slug + "." + ContentDomain
}

// ContentDomain is the registrable, PSL-listed content domain (ARCHITECTURE.md
// §3) under which every tenant site is served.
const ContentDomain = "shippedusercontent.com"

// RouteKey returns the KV key for a host ("route:<host>").
func RouteKey(host string) string { return "route:" + host }
