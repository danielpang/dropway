//go:build cloud

package billing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
)

// One subscription per org. These cover the regression where an org on Pro bought
// Business through a second Checkout: Stripe then held TWO live subscriptions, and
// each one's monthly renewal (customer.subscription.updated) overwrote the org's
// tier — business→pro at one renewal, pro→business a few hours later, every month.

func TestIgnoreForeignSubscription(t *testing.T) {
	liveBiz := CurrentSubscription{ID: "sub_biz", PlanTier: TierBusiness, Status: "active"}
	canceled := CurrentSubscription{ID: "sub_old", PlanTier: TierFree, Status: "canceled"}
	cases := []struct {
		name   string
		cur    CurrentSubscription
		evSub  string
		cancel bool
		want   bool
	}{
		{"same subscription upsert", liveBiz, "sub_biz", false, false},
		{"same subscription cancel", liveBiz, "sub_biz", true, false},
		{"no current subscription", CurrentSubscription{}, "sub_new", false, false},
		{"event without subscription id", liveBiz, "", false, false},
		{"stray renewal while current is live", liveBiz, "sub_pro", false, true},
		{"stray cancel while current is live", liveBiz, "sub_pro", true, true},
		{"re-subscribe after cancel", canceled, "sub_new", false, false},
		{"incomplete new subscription after cancel", canceled, "sub_new", true, true},
		{"customer row from SaveCustomerID (free, active)", CurrentSubscription{ID: "", PlanTier: TierFree, Status: "active"}, "sub_new", false, false},
		{"past_due current is still live", CurrentSubscription{ID: "sub_biz", PlanTier: TierBusiness, Status: "past_due"}, "sub_pro", false, true},
		{"unpaid current is not live", CurrentSubscription{ID: "sub_biz", PlanTier: TierBusiness, Status: "unpaid"}, "sub_pro", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ignoreForeignSubscription(tc.cur, tc.evSub, tc.cancel); got != tc.want {
				t.Errorf("ignoreForeignSubscription = %v, want %v", got, tc.want)
			}
		})
	}
}

// The regression itself: with Business as the current subscription, the stray Pro
// subscription's renewal, its non-paying update and its deletion must all leave
// entitlement untouched; Business's own renewal still applies.
func TestApplyEvent_SecondSubscriptionCannotChangeTier(t *testing.T) {
	cur := CurrentSubscription{ID: "sub_biz", PlanTier: TierBusiness, Status: "active"}
	ignored := []Event{
		{Type: "customer.subscription.updated", Data: EventData{OrgID: "org_1", StripeSubscriptionID: "sub_pro", PlanTier: TierPro, Status: "active"}},
		{Type: "customer.subscription.updated", Data: EventData{OrgID: "org_1", StripeSubscriptionID: "sub_pro", PlanTier: TierPro, Status: "unpaid"}},
		{Type: "customer.subscription.created", Data: EventData{OrgID: "org_1", StripeSubscriptionID: "sub_pro", PlanTier: TierPro, Status: "active"}},
		{Type: "checkout.session.completed", Data: EventData{OrgID: "org_1", StripeSubscriptionID: "sub_pro", PlanTier: TierPro, Status: "active"}},
		{Type: "customer.subscription.deleted", Data: EventData{OrgID: "org_1", StripeSubscriptionID: "sub_pro", PlanTier: TierPro, Status: "canceled"}},
	}
	for _, ev := range ignored {
		t.Run("ignored "+ev.Type+" "+ev.Data.Status, func(t *testing.T) {
			rec := &statusRecorder{current: cur, canceledStatus: "active"}
			if err := applyEvent(context.Background(), rec, slog.Default(), ev); err != nil {
				t.Fatalf("applyEvent: %v", err)
			}
			if rec.org != "" {
				t.Errorf("stray subscription event must not touch entitlement; applied to %q", rec.org)
			}
		})
	}

	t.Run("current subscription renewal applies", func(t *testing.T) {
		rec := &statusRecorder{current: cur}
		ev := Event{Type: "customer.subscription.updated", Data: EventData{OrgID: "org_1", StripeSubscriptionID: "sub_biz", PlanTier: TierBusiness, Status: "active"}}
		if err := applyEvent(context.Background(), rec, slog.Default(), ev); err != nil {
			t.Fatalf("applyEvent: %v", err)
		}
		if rec.org != "org_1" || rec.status != "active" {
			t.Errorf("current subscription event should upsert; got org=%q status=%q", rec.org, rec.status)
		}
	})

	t.Run("current subscription deletion cancels", func(t *testing.T) {
		rec := &statusRecorder{current: cur, canceledStatus: "over_limit"}
		ev := Event{Type: "customer.subscription.deleted", Data: EventData{OrgID: "org_1", StripeSubscriptionID: "sub_biz"}}
		if err := applyEvent(context.Background(), rec, slog.Default(), ev); err != nil {
			t.Fatalf("applyEvent: %v", err)
		}
		if rec.org != "org_1" || rec.status != "over_limit" {
			t.Errorf("current subscription deletion should cancel; got org=%q status=%q", rec.org, rec.status)
		}
	})
}

// After the current subscription is canceled, a NEW subscription (re-subscribe)
// must be accepted even though its id differs.
func TestApplyEvent_ResubscribeAfterCancelApplies(t *testing.T) {
	rec := &statusRecorder{current: CurrentSubscription{ID: "sub_old", PlanTier: TierFree, Status: "canceled"}}
	ev := Event{Type: "customer.subscription.updated", Data: EventData{OrgID: "org_1", StripeSubscriptionID: "sub_new", PlanTier: TierPro, Status: "active"}}
	if err := applyEvent(context.Background(), rec, slog.Default(), ev); err != nil {
		t.Fatalf("applyEvent: %v", err)
	}
	if rec.org != "org_1" || rec.status != "active" {
		t.Errorf("re-subscribe should upsert; got org=%q status=%q", rec.org, rec.status)
	}
}

// A paying org must not be able to start a second Checkout (that is what created
// the second subscription). It gets a 409 pointing it at the Billing Portal.
func TestCheckout_ActiveSubscription_409(t *testing.T) {
	for _, status := range []string{"active", "trialing", "past_due"} {
		t.Run(status, func(t *testing.T) {
			store := &fakeCPStore{hasSub: true, sub: Subscription{
				StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_pro", PlanTier: TierPro, Status: status,
			}}
			sc := &fakeStripe{}
			h := newHandlersUnderAuth(t, store, sc, claimsWith("org_1", "owner"))

			rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business"}`)
			if rr.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s, want 409", rr.Code, rr.Body.String())
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body.Error != "subscription_exists" {
				t.Errorf("error code = %q (%v), want subscription_exists", body.Error, err)
			}
			if sc.checkoutParams.PriceID != "" {
				t.Error("no Checkout session may be created while a subscription is live")
			}
		})
	}
}

// Orgs without a live subscription can still check out: a customer row saved
// before the first Checkout (free/active, no subscription id), or a canceled one.
func TestCheckout_NoLiveSubscription_Allowed(t *testing.T) {
	cases := map[string]Subscription{
		"customer saved, never subscribed": {StripeCustomerID: "cus_1", PlanTier: TierFree, Status: "active"},
		"previous subscription canceled":   {StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_old", PlanTier: TierFree, Status: "canceled"},
		"previous subscription unpaid":     {StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_old", PlanTier: TierPro, Status: "unpaid"},
	}
	for name, sub := range cases {
		t.Run(name, func(t *testing.T) {
			store := &fakeCPStore{hasSub: true, sub: sub}
			sc := &fakeStripe{}
			h := newHandlersUnderAuth(t, store, sc, claimsWith("org_1", "owner"))

			rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"pro"}`)
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s, want 200", rr.Code, rr.Body.String())
			}
			if sc.ensuredCustomer != "cus_1" {
				t.Errorf("should reuse the existing customer, got %q", sc.ensuredCustomer)
			}
		})
	}
}
