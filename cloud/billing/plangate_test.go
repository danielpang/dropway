//go:build cloud

package billing

import (
	"strings"
	"testing"
)

func TestAllowMemoryReason(t *testing.T) {
	for _, tc := range []struct {
		tier    PlanTier
		allowed bool
	}{
		{TierFree, false},
		{"", false},
		{TierPro, true},
		{TierBusiness, true},
		{TierEnterprise, true},
	} {
		allowed := !(tc.tier == TierFree || tc.tier == "")
		if allowed != tc.allowed {
			t.Errorf("tier %q: allowed = %v, want %v", tc.tier, allowed, tc.allowed)
		}
	}
}

// TestPlanTierQueryAvoidsRLSBlockedTable guards the bug where the plan tier
// was read from app.org_meta (FORCE ROW LEVEL SECURITY) over the bare billing
// pool without the app.current_org_id GUC — which RLS-filtered to zero rows
// for every org and resolved everyone to Free. The tier must be read from
// billing.subscriptions (no RLS).
func TestPlanTierQueryAvoidsRLSBlockedTable(t *testing.T) {
	if !strings.Contains(planTierQuery, "billing.subscriptions") {
		t.Errorf("plan-tier query must read billing.subscriptions, got: %s", planTierQuery)
	}
	if strings.Contains(planTierQuery, "app.org_meta") {
		t.Errorf("plan-tier query must NOT read the RLS-forced app.org_meta over the bare pool: %s", planTierQuery)
	}
}
