//go:build cloud

package quota

// quota_branches_test.go fills the remaining Allow/siteCap/exceeded branches the
// happy-path table in quota_test.go doesn't reach: the per-USER site caps on the
// paid tiers (Business/Enterprise), the unknown-resource pass-through, and the
// nil-URLBuilder path (a 402 with no CTA URLs).

import (
	"testing"

	corequota "github.com/danielpang/shipped/internal/quota"
)

// Business per-user site cap: 100 sites → 101st rejected, upgrade to enterprise.
func TestBusinessTier_SiteCap(t *testing.T) {
	err := newProvider().Allow("business", corequota.ResourceSitePerUser, 100)
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
	if err := newProvider().Allow("business", corequota.ResourceSitePerUser, 99); err != nil {
		t.Fatalf("99 sites on business should be allowed: %v", err)
	}
}

// Enterprise per-user site cap: 1000 sites → 1001st rejected, contact sales (the
// site cap, unlike members, also tops out at the enterprise→sales boundary).
func TestEnterpriseTier_SiteCap_ContactSales(t *testing.T) {
	err := newProvider().Allow("enterprise", corequota.ResourceSitePerUser, 1000)
	ex, ok := corequota.AsExceeded(err)
	if !ok {
		t.Fatalf("want ExceededError, got %v", err)
	}
	if ex.Max != 1000 || ex.NextTier != "contact_sales" {
		t.Errorf("max=%d next=%q, want 1000/contact_sales", ex.Max, ex.NextTier)
	}
	if ex.SalesURL == "" {
		t.Error("enterprise site cap must carry a sales_url (no self-serve above enterprise)")
	}
	if ex.UpgradeURL != "" {
		t.Error("enterprise site cap must NOT carry an upgrade_url")
	}
}

func TestEnterpriseTier_UnderSiteCap(t *testing.T) {
	if err := newProvider().Allow("enterprise", corequota.ResourceSitePerUser, 999); err != nil {
		t.Fatalf("999 sites on enterprise should be allowed: %v", err)
	}
}

// An unknown plan tier defaults to Free for site caps too (fail-closed to the
// tightest band, symmetric with the member-cap default).
func TestUnknownTier_DefaultsToFree_SiteCap(t *testing.T) {
	if err := newProvider().Allow("platinum", corequota.ResourceSitePerUser, 10); err == nil {
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
	p := NewProvider(nil)

	// A self-serve upgrade boundary: tier+next set, but no upgrade_url.
	err := p.Allow("free", corequota.ResourceMemberPerOrg, 5)
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

	// The contact-sales boundary likewise carries no sales_url with a nil builder.
	salesErr := p.Allow("enterprise", corequota.ResourceMemberPerOrg, 1000)
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
