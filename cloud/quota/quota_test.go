//go:build cloud

package quota

import (
	"testing"

	corequota "github.com/danielpang/dropway/internal/quota"
)

// newProvider builds a provider with STORAGE ENFORCEMENT ON, so the storage-band
// tests exercise the cap. Production defaults to off (see TestStorage_NotEnforced).
func newProvider() *Provider {
	return NewProvider(DashboardURLBuilder{DashboardBaseURL: "https://app.dropway.dev"}, true)
}

func TestFreeTier_SiteCap(t *testing.T) {
	// 10 sites in the org already → 11th rejected (per-ORG cap, pooled across members).
	err := newProvider().Allow("free", corequota.ResourceSitePerOrg, 10)
	ex, ok := corequota.AsExceeded(err)
	if !ok {
		t.Fatalf("want ExceededError, got %v", err)
	}
	if ex.Max != 10 || ex.Current != 10 {
		t.Errorf("max/current = %d/%d, want 10/10", ex.Max, ex.Current)
	}
	if ex.PlanTier != "free" || ex.NextTier != "business" {
		t.Errorf("tiers = %q→%q, want free→business", ex.PlanTier, ex.NextTier)
	}
	if ex.UpgradeURL == "" {
		t.Error("free→business should carry an upgrade_url")
	}
	if ex.SalesURL != "" {
		t.Error("free tier should not carry a sales_url")
	}
}

func TestFreeTier_UnderSiteCap(t *testing.T) {
	if err := newProvider().Allow("free", corequota.ResourceSitePerOrg, 9); err != nil {
		t.Fatalf("9 sites should be allowed: %v", err)
	}
}

func TestStorageCap_Bands(t *testing.T) {
	const gib = int64(1) << 30
	p := newProvider()

	// Free: filling EXACTLY to the 5 GiB cap is allowed; one byte over → 402 business.
	if err := p.AllowN("free", corequota.ResourceStorageBytesPerOrg, 0, 5*gib); err != nil {
		t.Fatalf("free: exactly 5 GiB should be allowed: %v", err)
	}
	err := p.AllowN("free", corequota.ResourceStorageBytesPerOrg, 4*gib, 1*gib+1)
	ex, ok := corequota.AsExceeded(err)
	if !ok || ex.PlanTier != "free" || ex.NextTier != "business" || ex.Max != 5*gib {
		t.Fatalf("free storage over-cap = %v (ex=%+v)", err, ex)
	}

	// Business: 100 GiB cap; over → enterprise.
	if err := p.AllowN("business", corequota.ResourceStorageBytesPerOrg, 0, 100*gib); err != nil {
		t.Fatalf("business: 100 GiB should be allowed: %v", err)
	}
	if bizEx, ok := corequota.AsExceeded(
		p.AllowN("business", corequota.ResourceStorageBytesPerOrg, 100*gib, 1)); !ok || bizEx.NextTier != "enterprise" {
		t.Fatalf("business storage over-cap should point to enterprise")
	}
}

// Allow is AllowN with n=1; confirm the delegation on a continuous resource (a
// 1-byte add is within the Free cap).
func TestAllow_DelegatesToAllowN(t *testing.T) {
	if err := newProvider().Allow("free", corequota.ResourceStorageBytesPerOrg, 0); err != nil {
		t.Fatalf("Allow(+1 byte) within cap should pass: %v", err)
	}
}

func TestEmptyTier_DefaultsToFree(t *testing.T) {
	// An empty/unknown plan tier must be treated as Free (fail-closed to the
	// tightest paid-relevant cap, not unlimited).
	if err := newProvider().Allow("", corequota.ResourceSitePerOrg, 10); err == nil {
		t.Fatal("empty tier should default to free and cap at 10 sites")
	}
}

// Storage gating is OFF by default (ENFORCE_STORAGE_QUOTA=false): storage is metered
// but a deploy is never rejected for crossing a band, no matter how large the delta.
func TestStorage_NotEnforcedByDefault(t *testing.T) {
	const gib = int64(1) << 30
	p := NewProvider(DashboardURLBuilder{DashboardBaseURL: "https://app.dropway.dev"}, false)
	cases := []struct {
		tier           string
		current, delta int64
	}{
		{"free", 4 * gib, 100 * gib},      // way past the 5 GiB Free band
		{"business", 100 * gib, 50 * gib}, // past the 100 GiB Pro band
		{"enterprise", 500 * gib, 1 * gib},
	}
	for _, c := range cases {
		if err := p.AllowN(c.tier, corequota.ResourceStorageBytesPerOrg, c.current, c.delta); err != nil {
			t.Errorf("storage must not be gated when disabled: AllowN(%q, %d, +%d) = %v", c.tier, c.current, c.delta, err)
		}
	}
	// Site caps are still enforced regardless of the storage flag.
	if err := p.Allow("free", corequota.ResourceSitePerOrg, 10); err == nil {
		t.Error("the site cap must still fire even with storage gating off")
	}
}

// Seats are free: members are unlimited on EVERY plan. The cloud
// provider must never 402 on the member resource, regardless of tier or count.
func TestMembers_AlwaysUnlimited(t *testing.T) {
	p := newProvider()
	cases := []struct {
		tier    string
		current int64
	}{
		{"free", 0}, {"free", 5}, {"free", 9999},
		{"business", 100}, {"enterprise", 1_000_000}, {"", 50},
	}
	for _, c := range cases {
		if err := p.Allow(c.tier, corequota.ResourceMemberPerOrg, c.current); err != nil {
			t.Errorf("members must be unlimited: Allow(%q, members, %d) = %v, want nil", c.tier, c.current, err)
		}
	}
}
