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
	if ex.PlanTier != "free" || ex.NextTier != "pro" {
		t.Errorf("tiers = %q→%q, want free→pro", ex.PlanTier, ex.NextTier)
	}
	if ex.UpgradeURL == "" {
		t.Error("free→pro should carry an upgrade_url")
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

func TestFreeTier_SkillsPerOrgCap(t *testing.T) {
	// 10 skills in the org already → the 11th is rejected (per-ORG cap).
	err := newProvider().Allow("free", corequota.ResourceSkillPerOrg, 10)
	ex, ok := corequota.AsExceeded(err)
	if !ok {
		t.Fatalf("want ExceededError, got %v", err)
	}
	if ex.Max != 10 || ex.Current != 10 {
		t.Errorf("max/current = %d/%d, want 10/10", ex.Max, ex.Current)
	}
	if ex.PlanTier != "free" || ex.NextTier != "pro" {
		t.Errorf("tiers = %q→%q, want free→pro", ex.PlanTier, ex.NextTier)
	}
	if ex.UpgradeURL == "" {
		t.Error("free→pro should carry an upgrade_url")
	}
}

func TestFreeTier_UnderSkillsPerOrgCap(t *testing.T) {
	if err := newProvider().Allow("free", corequota.ResourceSkillPerOrg, 9); err != nil {
		t.Fatalf("the 10th skill should be allowed on free: %v", err)
	}
}

func TestSkillsPerOrg_PaidTiersUnlimited(t *testing.T) {
	p := newProvider()
	for _, tier := range []string{"pro", "business", "enterprise"} {
		if err := p.Allow(tier, corequota.ResourceSkillPerOrg, 10_000); err != nil {
			t.Errorf("skills per org must be unlimited on %q: %v", tier, err)
		}
	}
}

func TestStorageCap_Bands(t *testing.T) {
	const gib = int64(1) << 30
	p := newProvider()

	// Free: filling EXACTLY to the 5 GiB cap is allowed; one byte over → 402 pro.
	if err := p.AllowN("free", corequota.ResourceStorageBytesPerOrg, 0, 5*gib); err != nil {
		t.Fatalf("free: exactly 5 GiB should be allowed: %v", err)
	}
	err := p.AllowN("free", corequota.ResourceStorageBytesPerOrg, 4*gib, 1*gib+1)
	ex, ok := corequota.AsExceeded(err)
	if !ok || ex.PlanTier != "free" || ex.NextTier != "pro" || ex.Max != 5*gib {
		t.Fatalf("free storage over-cap = %v (ex=%+v)", err, ex)
	}

	// Pro: 100 GiB cap; over → business.
	if err := p.AllowN("pro", corequota.ResourceStorageBytesPerOrg, 0, 100*gib); err != nil {
		t.Fatalf("pro: 100 GiB should be allowed: %v", err)
	}
	if proEx, ok := corequota.AsExceeded(
		p.AllowN("pro", corequota.ResourceStorageBytesPerOrg, 100*gib, 1)); !ok || proEx.NextTier != "business" {
		t.Fatalf("pro storage over-cap should point to business")
	}

	// Business: 250 GiB cap; over → enterprise.
	if err := p.AllowN("business", corequota.ResourceStorageBytesPerOrg, 0, 250*gib); err != nil {
		t.Fatalf("business: 250 GiB should be allowed: %v", err)
	}
	if bizEx, ok := corequota.AsExceeded(
		p.AllowN("business", corequota.ResourceStorageBytesPerOrg, 250*gib, 1)); !ok || bizEx.NextTier != "enterprise" {
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
		{"free", 4 * gib, 100 * gib},     // way past the 5 GiB Free band
		{"pro", 100 * gib, 50 * gib},     // past the 100 GiB Pro band
		{"business", 250 * gib, 1 * gib}, // past the 250 GiB Business band
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

// Custom domains are a PAID feature: the free tier is capped at 0, so the first
// custom hostname (current=0 → 0+1 > 0) is rejected with a free→pro upgrade body.
func TestCustomDomains_FreeTierRejected(t *testing.T) {
	err := newProvider().Allow("free", corequota.ResourceCustomDomainPerOrg, 0)
	ex, ok := corequota.AsExceeded(err)
	if !ok {
		t.Fatalf("want ExceededError, got %v", err)
	}
	if ex.Max != 0 || ex.Current != 0 {
		t.Errorf("max/current = %d/%d, want 0/0", ex.Max, ex.Current)
	}
	if ex.PlanTier != "free" || ex.NextTier != "pro" {
		t.Errorf("tiers = %q→%q, want free→pro", ex.PlanTier, ex.NextTier)
	}
	if ex.UpgradeURL == "" {
		t.Error("free→pro should carry an upgrade_url")
	}
	if ex.SalesURL != "" {
		t.Errorf("free→pro should not carry a sales_url, got %q", ex.SalesURL)
	}
}

// Every PAID tier gets unlimited custom domains: Allow must pass regardless of how
// many the org already has.
func TestCustomDomains_PaidTiersUnlimited(t *testing.T) {
	p := newProvider()
	cases := []struct {
		tier    string
		current int64
	}{
		{"pro", 0}, {"pro", 50}, {"business", 0}, {"business", 999},
		{"enterprise", 0}, {"enterprise", 1_000_000},
	}
	for _, c := range cases {
		if err := p.Allow(c.tier, corequota.ResourceCustomDomainPerOrg, c.current); err != nil {
			t.Errorf("custom domains must be unlimited on paid tiers: Allow(%q, %d) = %v, want nil", c.tier, c.current, err)
		}
	}
}

// MFA ENFORCEMENT is a business/enterprise feature: free AND pro are capped at 0,
// so enabling it (current=0 → 0+1 > 0) is rejected with an upgrade-to-business body.
func TestMfaEnforcement_FreeAndProRejected(t *testing.T) {
	for _, tier := range []string{"free", "pro"} {
		err := newProvider().Allow(tier, corequota.ResourceMfaEnforcement, 0)
		ex, ok := corequota.AsExceeded(err)
		if !ok {
			t.Fatalf("tier %q: want ExceededError, got %v", tier, err)
		}
		if ex.Max != 0 || ex.Current != 0 {
			t.Errorf("tier %q: max/current = %d/%d, want 0/0", tier, ex.Max, ex.Current)
		}
		if ex.PlanTier != tier || ex.NextTier != "business" {
			t.Errorf("tiers = %q→%q, want %s→business", ex.PlanTier, ex.NextTier, tier)
		}
		if ex.UpgradeURL == "" {
			t.Errorf("tier %q: should carry an upgrade_url", tier)
		}
	}
}

// Business and enterprise may always enable MFA enforcement.
func TestMfaEnforcement_BusinessEnterpriseAllowed(t *testing.T) {
	p := newProvider()
	for _, tier := range []string{"business", "enterprise"} {
		if err := p.Allow(tier, corequota.ResourceMfaEnforcement, 0); err != nil {
			t.Errorf("Allow(%q, mfa_enforcement) = %v, want nil", tier, err)
		}
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
