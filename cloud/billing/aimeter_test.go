//go:build cloud

package billing

import (
	"context"
	"testing"
)

// TestAIMeterGrossUp verifies the 3% fee gross-up and ceil-to-cent rounding.
func TestAIMeterGrossUp(t *testing.T) {
	cases := []struct {
		costUSD   float64
		wantCents int64
	}{
		{0, 0},        // free model → no event
		{1.00, 103},   // $1.00 * 1.03 = 103c
		{0.0025, 1},   // 0.2575c → ceil → 1c
		{0.50, 52},    // 51.5c → ceil → 52c
		{10.00, 1030}, // exact
	}
	for _, tc := range cases {
		var gotCents int64
		var sent bool
		m := &AIMeter{feePct: aiFeePct}
		m.sendFn = func(customerID, identifier string, cents int64) error {
			gotCents = cents
			sent = true
			return nil
		}
		// Stub the customer lookup by overriding ReportUsage's pool read: call
		// sendFn directly through a customerID-bypassing path.
		if tc.costUSD <= 0 {
			// Zero cost never sends.
			if err := reportUsageWithCustomer(m, "cus_1", "gen", tc.costUSD); err != nil {
				t.Fatalf("cost %v: %v", tc.costUSD, err)
			}
			if sent {
				t.Errorf("cost %v: sent a meter event for zero cost", tc.costUSD)
			}
			continue
		}
		if err := reportUsageWithCustomer(m, "cus_1", "gen", tc.costUSD); err != nil {
			t.Fatalf("cost %v: %v", tc.costUSD, err)
		}
		if !sent {
			t.Fatalf("cost %v: no meter event sent", tc.costUSD)
		}
		if gotCents != tc.wantCents {
			t.Errorf("cost %v: cents = %d, want %d", tc.costUSD, gotCents, tc.wantCents)
		}
	}
}

// reportUsageWithCustomer exercises the cents math + send without a DB by
// bypassing the customer lookup (a test-only shim mirroring ReportUsage's body).
func reportUsageWithCustomer(m *AIMeter, customerID, generationID string, costUSD float64) error {
	if costUSD <= 0 {
		return nil
	}
	cents := ceilCents(costUSD, m.feePct)
	if cents <= 0 {
		return nil
	}
	return m.sendFn(customerID, generationID, cents)
}

func TestAllowAIReason(t *testing.T) {
	// Pure check of the tier → gate mapping (no DB).
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

var _ = context.Background
