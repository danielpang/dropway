// Package projection writes the edge routing projection — the KV value at
// `route:<host>` that the Cloudflare serving Worker reads to resolve a host to a
// live version and stream it from R2.
//
// Postgres is authoritative; this projection is a REBUILDABLE cache. The Go API
// is the ONLY writer (the Worker is read-only). RouteValue mirrors the single
// cross-language data contract in contracts/kv-route.schema.json (@dropway/
// contracts, SCHEMA_VERSION=1); a round-trip test asserts the Go shape matches
// the schema and the TS parser.
package projection

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/danielpang/dropway/internal/edgerevoke"
)

// SchemaVersion is the version of THIS contract shape. It MUST equal
// SCHEMA_VERSION in @dropway/contracts (contracts/src/index.ts) and the
// `schema_version` enforced by kv-route.schema.json. Bump in lock-step on any
// breaking change.
//
// v1 → v2 (Phase 2): adds the optional `expires_at` (RFC3339) field so the
// Worker can enforce public/unlisted link expiry at the edge from the RouteValue
// (identity-gated expiry is refused at mint time in the Go API). The parse is
// kept backward compatible — a v1 value (no expires_at) is still accepted.
//
// v2 → v3: adds the optional `plan_tier` field (the owning org's plan, e.g.
// "free"/"pro") so the Worker can decide whether to inject the free-tier
// "Deployed with Dropway" attribution banner. Absent → tier unknown → no banner.
// The parse stays backward compatible (v1/v2 values are still accepted); the Go
// API now writes v3.
//
// v3 → v4: adds the optional `chat_id` field — the site's attached, panel-
// enabled chat log (Share This Session). When present, the Worker injects the
// "How this was made" pill into served HTML and serves the transcript page at
// the reserved /__dropway/chat path (reading the compiled transcript JSON from
// the chat-transcripts/<org>/<chat_id>.json object). Absent → no chat surface.
// The parse stays backward compatible (v1–v3 values are still accepted).
const SchemaVersion = 4

// MinSchemaVersion is the oldest contract shape the parser still accepts. v1
// values (written by a Phase-1 Go API) carry no expires_at and are read as
// "never expires"; a v2 value carries no plan_tier and is read as "tier unknown".
// The Worker upgrades them on the next publish.
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
//
// PlanTier (v3) is the OPTIONAL owning-org plan tier (e.g. "free"/"pro"), read
// from app.org_meta.plan_tier when the projection is written. The Worker uses it
// to gate the free-tier "Deployed with Dropway" attribution banner. Empty → tier
// unknown → no banner. It is `omitempty` so a paid/unknown route serializes
// compactly and a value without it round-trips cleanly.
// ChatID (v4) is the OPTIONAL id of the site's attached, panel-enabled chat
// log. The Worker uses it to inject the "How this was made" pill and to
// resolve the compiled transcript object at the reserved /__dropway/chat
// path. Empty → no chat surface. It is `omitempty` so a chat-less route
// serializes compactly and older values round-trip cleanly.
type RouteValue struct {
	OrgID         string `json:"org_id"`
	SiteID        string `json:"site_id"`
	VersionID     string `json:"version_id"`
	AccessMode    string `json:"access_mode"`
	SchemaVersion int    `json:"schema_version"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	PlanTier      string `json:"plan_tier,omitempty"`
	ChatID        string `json:"chat_id,omitempty"`
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
// from Postgres (the DR drill / drift reconciler).
type Writer interface {
	// PutRoute upserts the route value for host (e.g.
	// "<org>--<app>.dropwaycontent.com").
	PutRoute(ctx context.Context, host string, val RouteValue) error
	// DeleteRoute removes a host's route (unshare / delete).
	DeleteRoute(ctx context.Context, host string) error
	// RebuildFromDB clears the projection and re-writes it from the supplied
	// authoritative rows. routes maps host → RouteValue.
	RebuildFromDB(ctx context.Context, routes map[string]RouteValue) error
}

// Revoker writes the hard-revocation denylist the serving Worker + the /authz
// exchange read (the edgerevoke contract). The Go API is the ONLY writer; readers
// are read-only. Revoke is IDEMPOTENT — it takes max(existing, new) min_iat, so the
// denylist only ever tightens — and a write FAILS CLOSED (a lost key just forces an
// extra re-auth), so it is safe to retry and to rebuild.
//
// The production CloudflareKV and the dev/test Local writers both implement it on
// the same KV namespace as the route projection (the "revoked:" prefix), so no
// separate binding is required.
type Revoker interface {
	// Revoke records that every edge token for (kind, id) issued before minIAT
	// (unix seconds) is invalid. Idempotent: an existing entry with a LATER min_iat
	// is preserved (max wins).
	Revoke(ctx context.Context, kind edgerevoke.Kind, id string, minIAT int64) error
}

// OrgStatusWriter projects the per-org SUSPENSION / over-limit signal the serving
// Worker reads at `org_status:<org_id>` (edge/serving-worker ratelimit.ts). The Go
// API / cloud billing is the ONLY writer; the Worker is read-only.
//
// This is what makes edge suspension actually BLOCK: cloud/billing writes
// org_status to the DB (billing.subscriptions / app.org_meta), but the Worker can
// only see a fast KV flag — without this projection the suspension is dead (fails
// open). It is best-effort AFTER the DB commit (DB is source of truth; KV is a
// rebuildable projection), so a write failure is logged, not fatal.
//
// The model is READ-ONLY / NON-DESTRUCTIVE: a blocking status (`suspended` /
// `over_limit`) makes the edge serve a platform block page; it is NOT a token
// revocation and never deletes data. "active" CLEARS the flag (DeleteOrgStatus-
// equivalent), so a re-subscribe immediately restores serving.
type OrgStatusWriter interface {
	// SetOrgStatus projects the org's status. A blocking status ("suspended" /
	// "over_limit" / "past_due") writes `org_status:<orgID>`; "active" (or "") CLEARS
	// the key (the org may be served). Idempotent and rebuildable from the DB.
	SetOrgStatus(ctx context.Context, orgID, status string) error
}

// OrgStatusKey returns the KV key for an org's status flag ("org_status:<orgID>").
// It MUST match the Worker's ORG_STATUS_PREFIX (edge/serving-worker ratelimit.ts).
func OrgStatusKey(orgID string) string { return "org_status:" + orgID }

// OrgStatusActive is the status that means "serve normally" — writing it CLEARS the
// projected flag (the absence of a key is what the Worker treats as servable).
const OrgStatusActive = "active"

// HostForSite returns the canonical content host for a site — the ORG-NAMESPACED
// single DNS label under the content domain: `<orgSlug>--<appSlug>.<ContentDomain>`
// (e.g. acme/blog → "acme--blog.dropwaycontent.com").
//
// HOST SCHEME (documented decision): putting the ORG in the host (org first, then
// the app slug, separated by a DOUBLE dash) makes the global KV route namespace
// unambiguous — app.sites is UNIQUE(org_id, slug), so two orgs may both publish a
// site named "blog"; the org-namespaced host keeps each on its own origin and
// off-limits to the other. It MUST remain a SINGLE DNS label before the domain:
// the serving Worker's `*.<ContentDomain>` wildcard cert matches exactly one
// label, so `--` (not `.`) separates the two slugs. `--` is collision-free because
// slugify collapses dash runs, so neither slug can itself contain `--`. The
// publish path still treats route:<host> as the global host registry and refuses
// to overwrite a host owned by another org/site (see store.Store.Publish and the
// reserved-slug list).
func HostForSite(orgSlug, appSlug string) string {
	return orgSlug + "--" + appSlug + "." + ContentDomain
}

// PreviewHostForSite returns the time-limited preview host for one site
// VERSION: `<shortVersionID>--<orgSlug>--<appSlug>.<ContentDomain>`. Like
// HostForSite it stays a SINGLE DNS label (wildcard-cert constraint); the
// version label goes FIRST so a preview host can never collide with a
// canonical `<org>--<app>` host (slugs are slugified, the label is hex).
func PreviewHostForSite(versionID, orgSlug, appSlug string) string {
	return PreviewLabel(versionID) + "--" + HostForSite(orgSlug, appSlug)
}

// PreviewLabel is the short, URL-safe version-id prefix used as the leading
// label segment of a preview host (8 chars of the uuid with dashes stripped).
func PreviewLabel(versionID string) string {
	const n = 8
	stripped := ""
	for _, c := range versionID {
		if c != '-' {
			stripped += string(c)
		}
		if len(stripped) == n {
			break
		}
	}
	if stripped == "" {
		return "preview"
	}
	return stripped
}

// ContentDomain is the registrable, PSL-listed content domain under which
// every tenant site is served. It is env-overridable via
// CONTENT_DOMAIN (default "dropwaycontent.com") so a self-host/dev deployment
// can serve under its own apex without recompiling.
var ContentDomain = envOr("CONTENT_DOMAIN", "dropwaycontent.com")

// envOr returns the environment value for key, or def when it's unset/empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// RouteKey returns the KV key for a host ("route:<host>").
func RouteKey(host string) string { return "route:" + host }
