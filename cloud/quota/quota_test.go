//go:build cloud

package quota

import (
	"context"
	"testing"

	corequota "github.com/danielpang/shipped/internal/quota"
)

// fakeCounts is an in-memory Counts for tests (no DB).
type fakeCounts struct {
	tier    PlanTier
	members int64
	sites   int64
}

func (f fakeCounts) PlanTier(context.Context, string) (PlanTier, error)  { return f.tier, nil }
func (f fakeCounts) MembersInOrg(context.Context, string) (int64, error) { return f.members, nil }
func (f fakeCounts) SitesForUser(context.Context, string, string) (int64, error) {
	return f.sites, nil
}

func newProvider(c Counts) *Provider {
	return NewProvider(c, nil, DashboardURLBuilder{DashboardBaseURL: "https://app.shipped.app"})
}

func TestFreeTier_SiteCap(t *testing.T) {
	// 10 sites already → 11th rejected.
	p := newProvider(fakeCounts{tier: TierFree, sites: 10})
	err := p.CheckAndReserve(context.Background(), "org1", "user1", corequota.ResourceSitePerUser)
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
	p := newProvider(fakeCounts{tier: TierFree, sites: 9})
	if err := p.CheckAndReserve(context.Background(), "o", "u", corequota.ResourceSitePerUser); err != nil {
		t.Fatalf("9 sites should be allowed: %v", err)
	}
}

func TestFreeTier_MemberCap(t *testing.T) {
	p := newProvider(fakeCounts{tier: TierFree, members: 5})
	err := p.CheckAndReserve(context.Background(), "o", "u", corequota.ResourceMemberPerOrg)
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
	p := newProvider(fakeCounts{tier: TierBusiness, members: 99})
	err := p.CheckAndReserve(context.Background(), "o", "u", corequota.ResourceMemberPerOrg)
	ex, ok := corequota.AsExceeded(err)
	if !ok {
		t.Fatalf("want ExceededError, got %v", err)
	}
	if ex.Max != 99 || ex.NextTier != "enterprise" {
		t.Errorf("max=%d next=%q, want 99/enterprise", ex.Max, ex.NextTier)
	}
}

func TestEnterpriseTier_MemberCap_ContactSales(t *testing.T) {
	// 1000 members on Enterprise → 1001st rejected, contact sales (no checkout).
	p := newProvider(fakeCounts{tier: TierEnterprise, members: 1000})
	err := p.CheckAndReserve(context.Background(), "o", "u", corequota.ResourceMemberPerOrg)
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

func TestReserverInvokedOnSuccess(t *testing.T) {
	called := false
	r := reserverFunc(func(context.Context, string, string, corequota.Resource) error {
		called = true
		return nil
	})
	p := NewProvider(fakeCounts{tier: TierFree, sites: 0}, r, DashboardURLBuilder{})
	if err := p.CheckAndReserve(context.Background(), "o", "u", corequota.ResourceSitePerUser); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !called {
		t.Error("Reserver.Reserve should be called when within cap")
	}
}

func TestReserverSkippedWhenOverCap(t *testing.T) {
	called := false
	r := reserverFunc(func(context.Context, string, string, corequota.Resource) error {
		called = true
		return nil
	})
	p := NewProvider(fakeCounts{tier: TierFree, sites: 10}, r, DashboardURLBuilder{})
	_ = p.CheckAndReserve(context.Background(), "o", "u", corequota.ResourceSitePerUser)
	if called {
		t.Error("Reserver must NOT run when the cap is already crossed")
	}
}

type reserverFunc func(ctx context.Context, orgID, userID string, res corequota.Resource) error

func (f reserverFunc) Reserve(ctx context.Context, orgID, userID string, res corequota.Resource) error {
	return f(ctx, orgID, userID, res)
}
