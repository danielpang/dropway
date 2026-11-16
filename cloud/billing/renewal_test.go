//go:build cloud

package billing

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	stripe "github.com/stripe/stripe-go/v84"
)

// Renewals are their own analytics event. A paid recurring invoice on the org's
// current subscription records a Renewal (→ subscription_renewed); it never
// touches entitlement and is never a plan_upgraded / plan_downgraded.

func renewalEvent(subID, reason, orgID string) Event {
	return Event{ID: "evt_inv", Type: "invoice.paid", Data: EventData{
		OrgID: orgID, StripeSubscriptionID: subID, InvoiceID: "in_1",
		BillingReason: reason, AmountPaid: 15000, Currency: "usd",
	}}
}

func TestRecordRenewal_CurrentSubscription(t *testing.T) {
	rec := &statusRecorder{current: CurrentSubscription{ID: "sub_biz", PlanTier: TierBusiness, Status: "active"}}
	if err := applyEvent(context.Background(), rec, slog.Default(), renewalEvent("sub_biz", "subscription_cycle", "org_1")); err != nil {
		t.Fatalf("applyEvent: %v", err)
	}
	if len(rec.renewals) != 1 {
		t.Fatalf("recorded %d renewals, want 1", len(rec.renewals))
	}
	got := rec.renewals[0]
	want := Renewal{OrgID: "org_1", SubscriptionID: "sub_biz", InvoiceID: "in_1", PlanTier: TierBusiness, AmountPaid: 15000, Currency: "usd"}
	if got != want {
		t.Errorf("renewal = %+v, want %+v", got, want)
	}
	if rec.org != "" {
		t.Errorf("a renewal must not touch entitlement; applied to %q", rec.org)
	}
}

// The stray second subscription's renewal is not the org's renewal.
func TestRecordRenewal_ForeignSubscriptionIgnored(t *testing.T) {
	rec := &statusRecorder{current: CurrentSubscription{ID: "sub_biz", PlanTier: TierBusiness, Status: "active"}}
	err := applyEvent(context.Background(), rec, slog.Default(), renewalEvent("sub_pro", "subscription_cycle", "org_1"))
	if !errors.Is(err, errIgnoredEvent) {
		t.Fatalf("applyEvent err = %v, want errIgnoredEvent", err)
	}
	if len(rec.renewals) != 0 {
		t.Errorf("stray renewal must not be recorded: %+v", rec.renewals)
	}
}

// Only recurring charges are renewals: the first charge, prorations from a plan
// change and one-off invoices record nothing.
func TestRecordRenewal_OtherInvoicesIgnored(t *testing.T) {
	for _, reason := range []string{"subscription_create", "subscription_update", "manual", ""} {
		t.Run(reason, func(t *testing.T) {
			rec := &statusRecorder{current: CurrentSubscription{ID: "sub_biz", PlanTier: TierBusiness, Status: "active"}}
			if err := applyEvent(context.Background(), rec, slog.Default(), renewalEvent("sub_biz", reason, "org_1")); err != nil {
				t.Fatalf("applyEvent: %v", err)
			}
			if len(rec.renewals) != 0 {
				t.Errorf("billing_reason %q must not record a renewal", reason)
			}
		})
	}
}

// Without org_id metadata, the org is found by subscription id; an unknown
// subscription records nothing (and is not an error Stripe would retry).
func TestRecordRenewal_OrgLookup(t *testing.T) {
	rec := &statusRecorder{
		current:  CurrentSubscription{ID: "sub_biz", PlanTier: TierBusiness, Status: "active"},
		orgBySub: map[string]string{"sub_biz": "org_1"},
	}
	if err := applyEvent(context.Background(), rec, slog.Default(), renewalEvent("sub_biz", "subscription_cycle", "")); err != nil {
		t.Fatalf("applyEvent: %v", err)
	}
	if len(rec.renewals) != 1 || rec.renewals[0].OrgID != "org_1" {
		t.Fatalf("renewals = %+v, want one for org_1", rec.renewals)
	}

	unknown := &statusRecorder{}
	if err := applyEvent(context.Background(), unknown, slog.Default(), renewalEvent("sub_x", "subscription_cycle", "")); err != nil {
		t.Fatalf("applyEvent: %v", err)
	}
	if len(unknown.renewals) != 0 {
		t.Errorf("unknown subscription must not record a renewal")
	}
}

// invoice.paid is parsed in both API shapes: the subscription at the top level
// (before 2025-03-31) and under parent.subscription_details (after).
func TestFromInvoice_BothShapes(t *testing.T) {
	cases := map[string]string{
		"legacy": `{"id":"in_1","customer":"cus_1","billing_reason":"subscription_cycle","amount_paid":2500,"currency":"usd",
			"subscription":"sub_1","subscription_details":{"metadata":{"org_id":"org_1"}}}`,
		"parent": `{"id":"in_1","customer":"cus_1","billing_reason":"subscription_cycle","amount_paid":2500,"currency":"usd",
			"parent":{"subscription_details":{"subscription":"sub_1","metadata":{"org_id":"org_1"}}}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			v := RealSignatureVerifier{}
			d, err := v.resolveData(stripe.Event{Type: "invoice.paid", Data: &stripe.EventData{Raw: json.RawMessage(raw)}})
			if err != nil {
				t.Fatalf("resolveData: %v", err)
			}
			want := EventData{OrgID: "org_1", StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1",
				InvoiceID: "in_1", BillingReason: "subscription_cycle", AmountPaid: 2500, Currency: "usd"}
			if d != want {
				t.Errorf("got %+v, want %+v", d, want)
			}
		})
	}
}

func TestCaptureRenewal_Event(t *testing.T) {
	em := &fakeEmitter{}
	pa := NewPlanAnalytics(em)
	pa.CaptureRenewal(context.Background(), Renewal{
		OrgID: "org_1", SubscriptionID: "sub_biz", InvoiceID: "in_1",
		PlanTier: TierBusiness, AmountPaid: 15000, Currency: "usd",
	})
	pa.CaptureRenewal(context.Background(), Renewal{
		OrgID: "org_2", SubscriptionID: "sub_pro", InvoiceID: "in_2",
		PlanTier: TierPro, AmountPaid: 3800, Currency: "jpy",
	})
	pa.CaptureRenewal(context.Background(), Renewal{}) // no org → nothing

	if len(em.events) != 2 {
		t.Fatalf("emitted %d events, want 2", len(em.events))
	}
	ev := em.events[0]
	if ev.Event != "subscription_renewed" || ev.DistinctID != "org_1" || ev.Groups["organization"] != "org_1" {
		t.Errorf("event = %+v", ev)
	}
	p := ev.Properties
	if p["plan_tier"] != "business" || p["amount_paid"] != 150.0 || p["currency"] != "USD" || p["mrr_usd"] != 150.0 {
		t.Errorf("properties = %+v", p)
	}
	if _, ok := p["direction"]; ok {
		t.Error("a renewal has no upgrade/downgrade direction")
	}
	if got := em.events[1].Properties["amount_paid"]; got != 3800.0 {
		t.Errorf("zero-decimal currency amount = %v, want 3800", got)
	}
}

// The store emits a recorded renewal after the commit; with no emitter wired it
// is a safe no-op.
func TestEmitRenewal_Gating(t *testing.T) {
	(&BillingStore{}).emitRenewal(context.Background(), Renewal{OrgID: "org_1"})

	fake := &fakePlanAnalytics{}
	s := (&BillingStore{}).WithPlanAnalytics(fake)
	s.emitRenewal(context.Background(), Renewal{OrgID: "org_1", PlanTier: TierPro})
	if len(fake.renewals) != 1 || len(fake.calls) != 0 {
		t.Errorf("renewals=%d plan changes=%d, want 1 and 0", len(fake.renewals), len(fake.calls))
	}
}
