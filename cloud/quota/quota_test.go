//go:build cloud

package quota

import (
	"testing"

	corequota "github.com/danielpang/shipped/internal/quota"
)

func newProvider() *Provider {
	return NewProvider(DashboardURLBuilder{DashboardBaseURL: "https://app.shipped.app"})
}

func TestFreeTier_SiteCap(t *testing.T) {
	// 10 sites already → 11th rejected.
	err := newProvider().Allow("free", corequota.ResourceSitePerUser, 10)
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
	if err := newProvider().Allow("free", corequota.ResourceSitePerUser, 9); err != nil {
		t.Fatalf("9 sites should be allowed: %v", err)
	}
}

func TestEmptyTier_DefaultsToFree(t *testing.T) {
	// An empty/unknown plan tier must be treated as Free (fail-closed to the
	// tightest paid-relevant cap, not unlimited).
	if err := newProvider().Allow("", corequota.ResourceSitePerUser, 10); err == nil {
		t.Fatal("empty tier should default to free and cap at 10 sites")
	}
}

func TestFreeTier_MemberCap(t *testing.T) {
	err := newProvider().Allow("free", corequota.ResourceMemberPerOrg, 5)
	ex, ok := corequota.AsExceeded(err)
	if !ok {
		t.Fatalf("want ExceededError, got %v", err)
	}
	if ex.Max != 5 || ex.NextTier != "business" {
		t.Errorf("max=%d next=%q, want 5/business", ex.Max, ex.NextTier)
	}
}

func TestBusinessTier_MemberCap(t *testing.T) {
	// 99 members on Business → 100th rejected, upgrade to enterprise.
	err := newProvider().Allow("business", corequota.ResourceMemberPerOrg, 99)
	ex, ok := corequota.AsExceeded(err)
	if !ok {
		t.Fatalf("want ExceededError, got %v", err)
	}
	if ex.Max != 99 || ex.NextTier != "enterprise" {
		t.Errorf("max=%d next=%q, want 99/enterprise", ex.Max, ex.NextTier)
	}
}

func TestBusinessTier_UnderMemberCap(t *testing.T) {
	if err := newProvider().Allow("business", corequota.ResourceMemberPerOrg, 98); err != nil {
		t.Fatalf("98 members on business should be allowed: %v", err)
	}
}

func TestEnterpriseTier_MemberCap_ContactSales(t *testing.T) {
	// 1000 members on Enterprise → 1001st rejected, contact sales (no checkout).
	err := newProvider().Allow("enterprise", corequota.ResourceMemberPerOrg, 1000)
	ex, ok := corequota.AsExceeded(err)
	if !ok {
		t.Fatalf("want ExceededError, got %v", err)
	}
	if ex.Max != 1000 || ex.NextTier != "contact_sales" {
		t.Errorf("max=%d next=%q, want 1000/contact_sales", ex.Max, ex.NextTier)
	}
	if ex.SalesURL == "" {
		t.Error("contact_sales boundary must carry a sales_url")
	}
	if ex.UpgradeURL != "" {
		t.Error("contact_sales boundary must NOT carry an upgrade_url (no self-serve)")
	}
}
