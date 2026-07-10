// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store/db"
)

// This file unit-tests the store's PURE, extractable decision logic with no live
// database: the reserved-slug blocklist, the Postgres-error classifiers that map
// SQLSTATEs onto the store sentinels, the role check, the email/host normalizers,
// the route-expiry rule, and the DB-row → API-struct conversions. The DB-bound
// CRUD (CreateSite, Publish, AuthorizeMint, the GC version selection, the rebuild
// route collection, …) is exercised by the docker INTEGRATION suite (-tags
// integration), NOT here — this file deliberately covers only the logic that does
// not need a connection.

// ---------------------------------------------------------------------------
// IsReservedSlug — the reserved-slug blocklist.
// ---------------------------------------------------------------------------

func TestIsReservedSlug(t *testing.T) {
	reserved := []string{"www", "app", "api", "admin", "dashboard", "auth", "billing", "cdn", "mail"}
	for _, s := range reserved {
		if !IsReservedSlug(s) {
			t.Errorf("IsReservedSlug(%q) = false, want true (on the blocklist)", s)
		}
	}
	allowed := []string{"my-site", "acme", "docs-internal", "blog2", "Admin", "APP", "", "wwww"}
	for _, s := range allowed {
		if IsReservedSlug(s) {
			t.Errorf("IsReservedSlug(%q) = true, want false (case-sensitive exact match only)", s)
		}
	}
}

// ---------------------------------------------------------------------------
// ValidSlug — the slug grammar guarding the content host + KV route key (H1).
// ---------------------------------------------------------------------------

func TestValidSlug(t *testing.T) {
	valid := []string{
		"a", "acme", "my-site", "docs-internal", "blog2", "a1", "1a",
		"a-b-c", strings.Repeat("a", 63),
	}
	for _, s := range valid {
		if !ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",                      // empty
		"-acme",                 // leading hyphen
		"acme-",                 // trailing hyphen
		"Acme",                  // uppercase
		"ac me",                 // space
		"a/b",                   // path separator → KV-key path injection
		"a.b",                   // dot → extra DNS label
		"a%2e",                  // percent → KV-key escaping
		"a#x",                   // fragment
		"a?x",                   // query
		"victimorg--victimsite", // doubled hyphen → org/app host-namespace collision
		"a--b",                  // any `--` run
		strings.Repeat("a", 64), // too long (max DNS label is 63)
		"a\tb",                  // control char
	}
	for _, s := range invalid {
		if ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = true, want false", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Postgres error classifiers: uniqueViolation / checkViolation / isUndefinedTable.
// ---------------------------------------------------------------------------

// pgErr builds a *pgconn.PgError with the given SQLSTATE + constraint, wrapped so
// the classifiers (which use errors.As) see it through a wrapping like the driver
// returns it in practice.
func pgErr(code, constraint string) error {
	return fmt.Errorf("driver: %w", &pgconn.PgError{Code: code, ConstraintName: constraint})
}

func TestUniqueViolation(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		constraint string
		want       bool
	}{
		{"any 23505, no constraint filter", pgErr("23505", "sites_org_slug_key"), "", true},
		{"23505 matching named constraint", pgErr("23505", "sites_org_slug_key"), "sites_org_slug_key", true},
		{"23505 wrong named constraint", pgErr("23505", "other_key"), "sites_org_slug_key", false},
		{"check violation is not unique", pgErr("23514", ""), "", false},
		{"non-pg error", errors.New("boom"), "", false},
		{"nil error", nil, "", false},
		{"no-rows is not unique", pgx.ErrNoRows, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := uniqueViolation(c.err, c.constraint); got != c.want {
				t.Errorf("uniqueViolation = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCheckViolation(t *testing.T) {
	if !checkViolation(pgErr("23514", "")) {
		t.Error("a 23514 CHECK/trigger raise must classify as a check violation (the external-sharing trigger)")
	}
	if checkViolation(pgErr("23505", "")) {
		t.Error("a 23505 unique violation is NOT a check violation")
	}
	if checkViolation(errors.New("plain")) {
		t.Error("a non-pg error is NOT a check violation")
	}
	if checkViolation(nil) {
		t.Error("nil is NOT a check violation")
	}
}

func TestIsUndefinedTable(t *testing.T) {
	// 42P01 undefined_table and 3F000 invalid_schema_name both mean Better Auth's
	// identity.member table isn't present (self-host pre-migration / DB-less).
	if !isUndefinedTable(pgErr("42P01", "")) {
		t.Error("42P01 (undefined_table) must classify as undefined table")
	}
	if !isUndefinedTable(pgErr("3F000", "")) {
		t.Error("3F000 (invalid_schema_name) must classify as undefined table")
	}
	if isUndefinedTable(pgErr("23505", "")) {
		t.Error("a unique violation is not an undefined table")
	}
	if isUndefinedTable(errors.New("x")) || isUndefinedTable(nil) {
		t.Error("non-pg / nil errors are not undefined tables")
	}
}

// isNoRows wraps pgx.ErrNoRows detection (RLS-filtered reads look like a miss).
func TestIsNoRows(t *testing.T) {
	if !isNoRows(pgx.ErrNoRows) {
		t.Error("pgx.ErrNoRows must be detected as no-rows")
	}
	if !isNoRows(fmt.Errorf("scan: %w", pgx.ErrNoRows)) {
		t.Error("a wrapped pgx.ErrNoRows must still be detected (errors.Is)")
	}
	if isNoRows(errors.New("other")) || isNoRows(nil) {
		t.Error("a non-no-rows / nil error must not be detected as no-rows")
	}
}

// ---------------------------------------------------------------------------
// IsAdminRole — owner ⊇ admin ⊇ member.
// ---------------------------------------------------------------------------

func TestIsAdminRole(t *testing.T) {
	if !IsAdminRole(RoleOwner) || !IsAdminRole(RoleAdmin) {
		t.Error("owner and admin must be admin-or-above")
	}
	if IsAdminRole(RoleMember) {
		t.Error("a plain member is NOT admin-or-above")
	}
	if IsAdminRole("") || IsAdminRole("superuser") {
		t.Error("unknown/empty roles are not admin")
	}
}

// ---------------------------------------------------------------------------
// normalizeEmail / normalizeHostname — case-insensitive trim for matching.
// ---------------------------------------------------------------------------

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  Alice@Example.COM ": "alice@example.com",
		"BOB@x.io":             "bob@x.io",
		"already@lower.com":    "already@lower.com",
		"\t spaced@y.z\n":      "spaced@y.z",
		"":                     "",
	}
	for in, want := range cases {
		if got := normalizeEmail(in); got != want {
			t.Errorf("normalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeHostname(t *testing.T) {
	// normalizeHostname reuses the email normalizer (lowercase + trim).
	if got := normalizeHostname("  DOCS.Acme.COM "); got != "docs.acme.com" {
		t.Errorf("normalizeHostname = %q, want docs.acme.com", got)
	}
	if got := normalizeHostname(""); got != "" {
		t.Errorf("normalizeHostname(empty) = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// routeExpiry — only the PUBLIC tier propagates expires_at into the RouteValue;
// identity-gated modes enforce expiry at mint time, not at the edge.
// ---------------------------------------------------------------------------

func TestRouteExpiry(t *testing.T) {
	at := time.Date(2026, 6, 14, 10, 30, 0, 0, time.FixedZone("x", 3600))
	withExp := AccessPolicy{ExpiresAt: &at}
	noExp := AccessPolicy{}

	// Public + expiry → the RFC3339 UTC timestamp.
	if got := routeExpiry(projection.AccessPublic, withExp); got != at.UTC().Format(time.RFC3339) {
		t.Errorf("public+expiry routeExpiry = %q, want the UTC RFC3339 expiry", got)
	}
	// Public, no expiry → empty.
	if got := routeExpiry(projection.AccessPublic, noExp); got != "" {
		t.Errorf("public/no-expiry routeExpiry = %q, want empty", got)
	}
	// Gated modes never propagate expiry to the edge, even when set.
	for _, mode := range []string{projection.AccessPassword, projection.AccessAllowlist, projection.AccessOrgOnly} {
		if got := routeExpiry(mode, withExp); got != "" {
			t.Errorf("routeExpiry(%q, withExpiry) = %q, want empty (gated modes mint-time enforce)", mode, got)
		}
	}
}

// ---------------------------------------------------------------------------
// GCPolicy.minAge — a zero/negative MinAge must fall back to the SAFE default so a
// caller can never accidentally disable the in-flight-deploy age guard.
// ---------------------------------------------------------------------------

func TestGCPolicy_MinAge(t *testing.T) {
	if got := (GCPolicy{}).minAge(); got != DefaultGCMinAge {
		t.Errorf("unset MinAge = %v, want the safe default %v", got, DefaultGCMinAge)
	}
	if got := (GCPolicy{MinAge: -1}).minAge(); got != DefaultGCMinAge {
		t.Errorf("negative MinAge = %v, want the safe default", got)
	}
	custom := 3 * time.Hour
	if got := (GCPolicy{MinAge: custom}).minAge(); got != custom {
		t.Errorf("explicit MinAge = %v, want %v", got, custom)
	}
	// The default must be the presign TTL + a 1h margin (covers a slow upload/commit).
	if DefaultGCMinAge != 15*time.Minute+time.Hour {
		t.Errorf("DefaultGCMinAge = %v, want presign TTL (15m) + 1h margin", DefaultGCMinAge)
	}
}

// ---------------------------------------------------------------------------
// selectRetained edge cases beyond the existing gc_test.go coverage.
// ---------------------------------------------------------------------------

func TestSelectRetained_NegativeKeepClampsToCurrentOnly(t *testing.T) {
	rows := []db.ListVersionsForGCRow{
		ver("v3", "s", 3, false),
		ver("v2", "s", 2, true), // current
		ver("v1", "s", 1, false),
	}
	// keepLastN < 0 is clamped to 0 → only the current version is retained.
	got := selectRetained(rows, -5, time.Now())
	if len(got) != 1 || got[0].VersionID != "v2" {
		t.Fatalf("keepLastN<0 should retain only the current version, got %+v", got)
	}
}

func TestSelectRetained_PerSiteIndependent(t *testing.T) {
	// Two sites; keepLastN=1 retains the current + newest of EACH site independently.
	rows := []db.ListVersionsForGCRow{
		ver("a2", "site-a", 2, true),
		ver("a1", "site-a", 1, false),
		ver("b2", "site-b", 2, false),
		ver("b1", "site-b", 1, true),
	}
	got := selectRetained(rows, 1, time.Now())
	keep := map[string]bool{}
	for _, v := range got {
		keep[v.VersionID] = true
	}
	// site-a: current a2 is also newest → just a2. site-b: current b1 + newest b2.
	if !keep["a2"] || keep["a1"] {
		t.Errorf("site-a: want only a2 retained, got %v", keep)
	}
	if !keep["b1"] || !keep["b2"] {
		t.Errorf("site-b: want b1 (current) + b2 (newest) retained, got %v", keep)
	}
}

func TestSelectRetained_NoCurrentFlag(t *testing.T) {
	// A site whose rows all have IsCurrent invalid/false (e.g. a never-published
	// site): only the keepLastN newest are retained, none forced by "current".
	rows := []db.ListVersionsForGCRow{
		{VersionID: "v2", SiteID: "s", VersionNo: 2, IsCurrent: pgtype.Bool{}}, // invalid → not current
		{VersionID: "v1", SiteID: "s", VersionNo: 1, IsCurrent: pgtype.Bool{Bool: false, Valid: true}},
	}
	got := selectRetained(rows, 1, time.Now())
	if len(got) != 1 || got[0].VersionID != "v2" {
		t.Fatalf("want only the newest (v2) retained when nothing is current, got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// nullableID / parseIP — the audit row's optional id + IP columns.
// ---------------------------------------------------------------------------

func TestNullableID(t *testing.T) {
	if got := nullableID(""); got != nil {
		t.Errorf("empty id → nil (SQL NULL), got %v", got)
	}
	if got := nullableID("   "); got != nil {
		t.Errorf("whitespace-only id → nil, got %v", got)
	}
	if got := nullableID("  user_7 "); got == nil || *got != "user_7" {
		t.Errorf("non-empty id → trimmed *string, got %v", got)
	}
}

func TestParseIP(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" means nil expected
	}{
		{"bare ipv4", "203.0.113.5", "203.0.113.5"},
		{"ipv4 with port (RemoteAddr)", "203.0.113.5:54321", "203.0.113.5"},
		{"bare ipv6", "2001:db8::1", "2001:db8::1"},
		{"ipv6 with port", "[2001:db8::1]:443", "2001:db8::1"},
		{"empty", "", ""},
		{"garbage", "not-an-ip", ""},
		{"whitespace", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseIP(c.in)
			if c.want == "" {
				if got != nil {
					t.Errorf("parseIP(%q) = %v, want nil", c.in, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseIP(%q) = nil, want %s", c.in, c.want)
			}
			want := netip.MustParseAddr(c.want)
			if *got != want {
				t.Errorf("parseIP(%q) = %v, want %v", c.in, *got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// manifestPrefix / ManifestKeyFor — the canonical R2 manifest paths.
// ---------------------------------------------------------------------------

func TestManifestPrefixAndKey(t *testing.T) {
	if got := manifestPrefix("org-1", "site-9"); got != "manifests/org-1/site-9" {
		t.Errorf("manifestPrefix = %q", got)
	}
	// ManifestKeyFor delegates to storage.ManifestKey; assert they agree (the version
	// row records the directory, serving resolves <prefix>/<version>.json).
	if got, want := ManifestKeyFor("org-1", "site-9", "v3"), storage.ManifestKey("org-1", "site-9", "v3"); got != want {
		t.Errorf("ManifestKeyFor = %q, want storage.ManifestKey = %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// DB-row → API-struct conversions (pure mappers).
// ---------------------------------------------------------------------------

func TestSiteFromDB(t *testing.T) {
	vid := "ver_1"
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := siteFromDB(db.AppSite{
		ID: "site_1", OrgID: "org_1", Slug: "docs", OwnerUserID: "user_1",
		AccessMode: "public", CurrentVersionID: &vid, CreatedAt: created,
	})
	if got.ID != "site_1" || got.Slug != "docs" || got.OwnerUserID != "user_1" || got.AccessMode != "public" {
		t.Errorf("siteFromDB scalar mismatch: %+v", got)
	}
	if got.CurrentVersionID == nil || *got.CurrentVersionID != "ver_1" {
		t.Errorf("siteFromDB CurrentVersionID = %v", got.CurrentVersionID)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("siteFromDB CreatedAt = %v", got.CreatedAt)
	}
}

func TestVersionFromDB(t *testing.T) {
	got := versionFromDB(db.AppSiteVersion{
		ID: "v1", OrgID: "o", SiteID: "s", VersionNo: 7, Status: "ready",
		R2Prefix: "manifests/o/s", ContentHash: "abc", SizeBytes: 42, CreatedBy: "u",
	})
	if got.VersionNo != 7 || got.Status != "ready" || got.SizeBytes != 42 || got.ContentHash != "abc" || got.CreatedBy != "u" {
		t.Errorf("versionFromDB mismatch: %+v", got)
	}
}

func TestDomainFromDB(t *testing.T) {
	// With both optional text columns SET.
	full := domainFromDB(db.AppDomain{
		ID: "dom_1", OrgID: "o", SiteID: "s", Hostname: "docs.acme.com",
		VerifyStatus: DomainVerified, TlsStatus: TLSIssued,
		CfHostnameID: pgtype.Text{String: "cf_123", Valid: true},
		DcvRecord:    pgtype.Text{String: "_acme-challenge TXT ...", Valid: true},
	})
	if full.CFHostnameID != "cf_123" || full.DCVRecord == "" {
		t.Errorf("domainFromDB should carry the optional CF id + DCV record: %+v", full)
	}
	if full.VerifyStatus != DomainVerified || full.TLSStatus != TLSIssued {
		t.Errorf("domainFromDB status mapping wrong: %+v", full)
	}
	// With the optional columns NULL → empty strings, never a panic.
	empty := domainFromDB(db.AppDomain{ID: "dom_2", Hostname: "x.io"})
	if empty.CFHostnameID != "" || empty.DCVRecord != "" {
		t.Errorf("domainFromDB with NULL optionals should be empty strings: %+v", empty)
	}
}

func TestAccessPolicyFromDB(t *testing.T) {
	at := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	full := accessPolicyFromDB(db.AppSiteAccessPolicy{
		SiteID: "s", OrgID: "o", Mode: projection.AccessPassword, Unlisted: true,
		PasswordHash: pgtype.Text{String: "$2a$hash", Valid: true},
		ExpiresAt:    pgtype.Timestamptz{Time: at, Valid: true},
	})
	if full.PasswordHash != "$2a$hash" || !full.Unlisted || full.Mode != projection.AccessPassword {
		t.Errorf("accessPolicyFromDB mismatch: %+v", full)
	}
	if full.ExpiresAt == nil || !full.ExpiresAt.Equal(at) {
		t.Errorf("accessPolicyFromDB ExpiresAt = %v, want %v", full.ExpiresAt, at)
	}
	// NULL password + expiry → empty hash + nil expiry.
	empty := accessPolicyFromDB(db.AppSiteAccessPolicy{SiteID: "s", OrgID: "o", Mode: projection.AccessPublic})
	if empty.PasswordHash != "" || empty.ExpiresAt != nil {
		t.Errorf("accessPolicyFromDB NULL optionals: %+v", empty)
	}
}

func TestAllowlistEntryFromDB(t *testing.T) {
	at := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	uid := "user_9"
	claimed := allowlistEntryFromDB(db.AppAllowlistEntry{
		ID: "al_1", OrgID: "o", SiteID: "s", Email: "a@x.com", IsExternal: true,
		ClaimedAt: pgtype.Timestamptz{Time: at, Valid: true}, ClaimedByUserID: &uid,
	})
	if !claimed.IsExternal || claimed.ClaimedBy == nil || *claimed.ClaimedBy != "user_9" {
		t.Errorf("allowlistEntryFromDB claimed mismatch: %+v", claimed)
	}
	if claimed.ClaimedAt == nil || !claimed.ClaimedAt.Equal(at) {
		t.Errorf("allowlistEntryFromDB ClaimedAt = %v", claimed.ClaimedAt)
	}
	// An unclaimed (pending) grant → nil ClaimedAt.
	pending := allowlistEntryFromDB(db.AppAllowlistEntry{ID: "al_2", Email: "b@y.com"})
	if pending.ClaimedAt != nil {
		t.Errorf("unclaimed grant should have nil ClaimedAt, got %v", pending.ClaimedAt)
	}
}

func TestAuditEntryFromDB(t *testing.T) {
	ip := netip.MustParseAddr("198.51.100.7")
	created := time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)
	full := auditEntryFromDB(db.AppAuditLog{
		ID: "a1", OrgID: "o", Action: "site.create",
		Target:    pgtype.Text{String: "site:s1", Valid: true},
		Metadata:  []byte(`{"slug":"docs","n":3}`),
		Ip:        &ip,
		RequestID: pgtype.Text{String: "req-1", Valid: true},
		TraceID:   pgtype.Text{String: "trace-1", Valid: true},
		CreatedAt: created,
	})
	if full.Target != "site:s1" || full.IP != "198.51.100.7" || full.RequestID != "req-1" || full.TraceID != "trace-1" {
		t.Errorf("auditEntryFromDB scalar mismatch: %+v", full)
	}
	if full.Metadata["slug"] != "docs" {
		t.Errorf("auditEntryFromDB metadata not parsed: %+v", full.Metadata)
	}

	// Malformed/legacy metadata JSON must degrade to an empty map, never panic or
	// leave a nil map (the conversion tolerates odd shapes).
	bad := auditEntryFromDB(db.AppAuditLog{ID: "a2", Metadata: []byte("not json")})
	if bad.Metadata == nil {
		t.Error("auditEntryFromDB must always populate a non-nil metadata map")
	}
	if len(bad.Metadata) != 0 {
		t.Errorf("malformed metadata should yield an empty map, got %+v", bad.Metadata)
	}
	// NULL optional columns → empty strings, nil IP → empty IP string.
	if bad.Target != "" || bad.IP != "" || bad.RequestID != "" {
		t.Errorf("auditEntryFromDB NULL optionals should be empty: %+v", bad)
	}
}

// ---------------------------------------------------------------------------
// Sentinel error identity — the handlers map these onto httpx statuses, so they
// must remain distinct (errors.Is) values, not accidentally aliased.
// ---------------------------------------------------------------------------

func TestStoreSentinelsAreDistinct(t *testing.T) {
	sentinels := []error{
		ErrReservedSlug, ErrSlugTaken, ErrNotFound, ErrVersionMismatch, ErrHostTaken,
		ErrExternalSharingDisabled, ErrInvalidMode, ErrPolicyExpired, ErrNotAllowlisted,
		ErrNotOrgMember, ErrWrongPassword, ErrNoPolicy, ErrBadEmail, ErrBadHostname,
		ErrInvalidDomainStatus, ErrHostNotFound, ErrMissingViewer, ErrNotGated,
		ErrNoMembership, ErrAuthSchemaUnavailable,
	}
	for i, a := range sentinels {
		if a == nil {
			t.Fatalf("sentinel %d is nil", i)
		}
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinels %d (%v) and %d (%v) must be distinct", i, a, j, b)
			}
		}
	}
}

// --- routeValue: the single RouteValue builder (publish + rebuild share it) -------

// TestRouteValue_BuildsCurrentContract asserts the shared builder stamps the live
// SchemaVersion and carries every field — the regression guard for the class of bug
// where publish and rebuild/reprojection drift (e.g. rebuild silently dropping
// expires_at or plan_tier). Both call sites go through this one builder.
func TestRouteValue_BuildsCurrentContract(t *testing.T) {
	rv := routeValue("org-1", "site-1", "ver-1", projection.AccessPublic, "2026-12-31T23:59:59Z", "free")
	want := projection.RouteValue{
		OrgID:         "org-1",
		SiteID:        "site-1",
		VersionID:     "ver-1",
		AccessMode:    projection.AccessPublic,
		SchemaVersion: projection.SchemaVersion,
		ExpiresAt:     "2026-12-31T23:59:59Z",
		PlanTier:      "free",
	}
	if rv != want {
		t.Fatalf("routeValue mismatch:\n got %+v\nwant %+v", rv, want)
	}
	if err := rv.Validate(); err != nil {
		t.Fatalf("built route value failed Validate: %v", err)
	}
	// A paid org with no link-expiry still round-trips with the tier set and no expiry.
	paid := routeValue("o", "s", "v", projection.AccessPublic, "", "pro")
	if paid.PlanTier != "pro" || paid.ExpiresAt != "" || paid.SchemaVersion != projection.SchemaVersion {
		t.Fatalf("paid route value wrong: %+v", paid)
	}
}

// fakeRouteWriter is a projection.Writer that records PutRoute calls and can fail a
// chosen host, so writeRoutes' continue-on-error contract is testable with no DB.
type fakeRouteWriter struct {
	puts     map[string]projection.RouteValue
	failHost string
}

func (f *fakeRouteWriter) PutRoute(_ context.Context, host string, val projection.RouteValue) error {
	if host == f.failHost {
		return fmt.Errorf("boom %s", host)
	}
	if f.puts == nil {
		f.puts = map[string]projection.RouteValue{}
	}
	f.puts[host] = val
	return nil
}
func (f *fakeRouteWriter) DeleteRoute(context.Context, string) error { return nil }
func (f *fakeRouteWriter) RebuildFromDB(context.Context, map[string]projection.RouteValue) error {
	return nil
}

// TestWriteRoutes_ContinueOnError asserts writeRoutes attempts EVERY host even when
// one PutRoute fails (no early abort that would leave the org's hosts split across
// tiers), and returns the failure(s) joined.
func TestWriteRoutes_ContinueOnError(t *testing.T) {
	w := &fakeRouteWriter{failHost: "b.example.com"}
	routes := map[string]projection.RouteValue{
		"a.example.com": routeValue("o", "s", "v", projection.AccessPublic, "", "pro"),
		"b.example.com": routeValue("o", "s", "v", projection.AccessPublic, "", "pro"),
		"c.example.com": routeValue("o", "s", "v", projection.AccessPublic, "", "pro"),
	}
	err := writeRoutes(context.Background(), w, routes)
	if err == nil {
		t.Fatal("expected a joined error for the failing host")
	}
	if !strings.Contains(err.Error(), "b.example.com") {
		t.Fatalf("error should name the failing host, got: %v", err)
	}
	// The two healthy hosts were still written despite b failing mid-iteration.
	if _, ok := w.puts["a.example.com"]; !ok {
		t.Error("a.example.com should have been written")
	}
	if _, ok := w.puts["c.example.com"]; !ok {
		t.Error("c.example.com should have been written")
	}
	if _, ok := w.puts["b.example.com"]; ok {
		t.Error("b.example.com failed and must not be recorded")
	}
}

// TestWriteRoutes_AllSucceed asserts the happy path writes every host and returns nil.
func TestWriteRoutes_AllSucceed(t *testing.T) {
	w := &fakeRouteWriter{}
	routes := map[string]projection.RouteValue{
		"a.example.com": routeValue("o", "s", "v", projection.AccessPublic, "", "free"),
		"b.example.com": routeValue("o", "s", "v", projection.AccessPublic, "", "free"),
	}
	if err := writeRoutes(context.Background(), w, routes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(w.puts) != 2 {
		t.Fatalf("expected 2 routes written, got %d", len(w.puts))
	}
}
