//go:build cloud

package quota

// quota_branches_test.go fills the remaining Allow/orgSiteCap/exceeded branches the
// happy-path table in quota_test.go doesn't reach: the per-ORG site cap on Business,
// the UNLIMITED Enterprise site tier, the unknown-resource pass-through, and the
// nil-URLBuilder path (a 402 with no CTA URLs).

import (
	"testing"

	corequota "github.com/danielpang/dropway/internal/quota"
)

// Business per-org site cap: 100 sites → 101st rejected, upgrade to enterprise.
func TestBusinessTier_SiteCap(t *testing.T) {
	err := newProvider().Allow("business", corequota.ResourceSitePerOrg, 100)
	ex, ok := corequota.AsExceeded(err)
	if !ok {
		t.Fatalf("want ExceededError, got %v", err)
	}
	if ex.Max != 100 || ex.Current != 100 {
		t.Errorf("max/current = %d/%d, want 100/100", ex.Max, ex.Current)
	}
	if ex.NextTier != "enterprise" {
		t.Errorf("next = %q, want enterprise", ex.NextTier)
	}
	if ex.UpgradeURL == "" {
		t.Error("business→enterprise must carry an upgrade_url")
	}
	if ex.SalesURL != "" {
		t.Error("business site cap must not be a contact-sales boundary")
	}
}

func TestBusinessTier_UnderSiteCap(t *testing.T) {
	if err := newProvider().Allow("business", corequota.ResourceSitePerOrg, 99); err != nil {
		t.Fatalf("99 sites on business should be allowed: %v", err)
	}
}

// Enterprise has UNLIMITED sites: no cap, no 402, no matter how many already exist.
func TestEnterpriseTier_SitesUnlimited(t *testing.T) {
	p := newProvider()
	for _, current := range []int64{1000, 100_000, 10_000_000} {
		if err := p.Allow("enterprise", corequota.ResourceSitePerOrg, current); err != nil {
			t.Errorf("enterprise sites must be unlimited: current=%d gave %v", current, err)
		}
	}
}

// An unknown plan tier defaults to Free for site caps too (fail-closed to the
// tightest band).
func TestUnknownTier_DefaultsToFree_SiteCap(t *testing.T) {
	if err := newProvider().Allow("platinum", corequota.ResourceSitePerOrg, 10); err == nil {
		t.Fatal("an unknown tier must default to free and cap sites at 10")
	}
}

// A resource the cloud policy does not enforce is never capped — the store only
// calls Allow for the resources it gates, so an unknown Resource passes through.
func TestUnknownResource_NotCapped(t *testing.T) {
	if err := newProvider().Allow("free", corequota.Resource("api_calls"), 1_000_000); err != nil {
		t.Errorf("an unenforced resource must pass through, got %v", err)
	}
}

// With a nil URLBuilder the 402 still carries the limit/tier facts but NO CTA URLs
// (NewProvider explicitly permits nil urls — e.g. a dev build with no dashboard).
func TestExceeded_NilURLBuilder_NoCTAURLs(t *testing.T) {
	p := NewProvider(nil, true) // storage enforced so the sales-boundary branch is reachable

	// A self-serve upgrade boundary (free sites → business): tier+next set, no URL.
	err := p.Allow("free", corequota.ResourceSitePerOrg, 10)
	ex, ok := corequota.AsExceeded(err)
	if !ok {
		t.Fatalf("want ExceededError, got %v", err)
	}
	if ex.NextTier != "business" {
		t.Errorf("next = %q, want business", ex.NextTier)
	}
	if ex.UpgradeURL != "" || ex.SalesURL != "" {
		t.Errorf("a nil URLBuilder must produce no CTA URLs, got upgrade=%q sales=%q", ex.UpgradeURL, ex.SalesURL)
	}

	// The contact-sales boundary (enterprise storage) likewise carries no sales_url.
	const gib = int64(1) << 30
	salesErr := p.AllowN("enterprise", corequota.ResourceStorageBytesPerOrg, 500*gib, 1)
	salesEx, ok := corequota.AsExceeded(salesErr)
	if !ok {
		t.Fatalf("want ExceededError, got %v", salesErr)
	}
	if salesEx.NextTier != "contact_sales" {
		t.Errorf("next = %q, want contact_sales", salesEx.NextTier)
	}
	if salesEx.SalesURL != "" || salesEx.UpgradeURL != "" {
		t.Errorf("a nil URLBuilder must produce no CTA URLs at the sales boundary, got sales=%q upgrade=%q", salesEx.SalesURL, salesEx.UpgradeURL)
	}
}
