//go:build cloud

package billing

import "testing"

// The metered AI price rides on the subscription as a second item. The tier +
// seats must still resolve from the PLAN item, not the metered one (which maps to
// no tier), regardless of item order.
func TestFromSubscription_MeteredItemIgnoredForTier(t *testing.T) {
	// Metered (unknown) price first, plan price second.
	d, err := newVerifier().fromSubscription([]byte(`{
		"id":"sub_two","customer":"cus_x","status":"active",
		"current_period_start":1000,"current_period_end":2000,
		"metadata":{"org_id":"org_two"},
		"items":{"data":[
			{"quantity":0,"price":{"id":"price_ai_metered"}},
			{"quantity":5,"price":{"id":"price_biz"}}
		]}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.PlanTier != TierBusiness {
		t.Errorf("tier = %q, want business (the plan item, not the metered one)", d.PlanTier)
	}
	if d.Seats != 5 {
		t.Errorf("seats = %d, want 5 (from the plan item)", d.Seats)
	}
	if d.UnknownPrice {
		t.Error("UnknownPrice must be false: the plan item's price is recognized")
	}
}

// current_period_start is parsed from the top level, and falls back to the item.
func TestFromSubscription_PeriodStart(t *testing.T) {
	top, err := newVerifier().fromSubscription([]byte(`{
		"id":"s1","customer":"c1","status":"active",
		"current_period_start":1700000000,"current_period_end":1702600000,
		"metadata":{"org_id":"o1"},
		"items":{"data":[{"quantity":1,"price":{"id":"price_pro"}}]}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if top.CurrentPeriodStart != 1700000000 {
		t.Errorf("current_period_start = %d, want 1700000000", top.CurrentPeriodStart)
	}

	// Top level absent → back-filled from the item.
	fromItem, err := newVerifier().fromSubscription([]byte(`{
		"id":"s2","customer":"c2","status":"active",
		"metadata":{"org_id":"o2"},
		"items":{"data":[{"quantity":1,"price":{"id":"price_pro"},"current_period_start":1699999999}]}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fromItem.CurrentPeriodStart != 1699999999 {
		t.Errorf("current_period_start = %d, want 1699999999 (from item)", fromItem.CurrentPeriodStart)
	}
}

// checkoutLineItems adds the metered price (no quantity) only when configured.
func TestCheckoutLineItems(t *testing.T) {
	// Plan only.
	plan := checkoutLineItems(CheckoutParams{PriceID: "price_pro"}, 2)
	if len(plan) != 1 {
		t.Fatalf("plan-only line items = %d, want 1", len(plan))
	}
	if plan[0].Price == nil || *plan[0].Price != "price_pro" || plan[0].Quantity == nil || *plan[0].Quantity != 2 {
		t.Errorf("plan item = %+v", plan[0])
	}

	// Plan + metered.
	both := checkoutLineItems(CheckoutParams{PriceID: "price_pro", MeteredPriceID: "price_ai"}, 2)
	if len(both) != 2 {
		t.Fatalf("plan+metered line items = %d, want 2", len(both))
	}
	if both[1].Price == nil || *both[1].Price != "price_ai" {
		t.Errorf("metered item price = %+v", both[1].Price)
	}
	if both[1].Quantity != nil {
		t.Error("metered item must NOT carry a quantity")
	}
}

// The 3% gross-up + ceil-to-cent math (the number Stripe actually bills).
func TestAIMeterCents(t *testing.T) {
	cases := []struct {
		cost float64
		want int64
	}{
		{1.00, 103},
		{0.0025, 1},
		{0.50, 52},
		{10.00, 1030},
	}
	for _, c := range cases {
		if got := ceilCents(c.cost, aiFeePct); got != c.want {
			t.Errorf("ceilCents(%v) = %d, want %d", c.cost, got, c.want)
		}
	}
}
