//go:build cloud

package billing

// Unit tests for the cloud billing → analytics logic: the pure upgrade/downgrade
// classification, the reason/event-name mapping, the adapter that shapes a
// PlanChange into a vendor-neutral analytics.Event, and the store's post-commit
// emitPlanChange gating (no emit on a no-op move, an empty org, or no emitter).

import (
	"context"
	"testing"

	"github.com/danielpang/dropway/internal/analytics"
)

func TestPlanDirection(t *testing.T) {
	cases := []struct {
		from, to PlanTier
		want     string
	}{
		{TierFree, TierPro, DirectionUpgrade},
		{TierFree, TierEnterprise, DirectionUpgrade},
		{TierPro, TierBusiness, DirectionUpgrade},
		{TierBusiness, TierPro, DirectionDowngrade},
		{TierEnterprise, TierFree, DirectionDowngrade},
		{TierPro, TierFree, DirectionDowngrade},
		{TierPro, TierPro, directionNone},        // seat/status change, no tier move
		{TierFree, TierFree, directionNone},      // cancel of an already-free org
		{"", TierPro, DirectionUpgrade},          // unknown/empty from ranks as free
		{TierPro, "garbage", DirectionDowngrade}, // unknown to ranks as free
	}
	for _, c := range cases {
		if got := planDirection(c.from, c.to); got != c.want {
			t.Errorf("planDirection(%q,%q) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

func TestReasonForEvent(t *testing.T) {
	cases := map[string]string{
		"checkout.session.completed":    "checkout",
		"customer.subscription.created": "subscription_created",
		"customer.subscription.updated": "subscription_updated",
		"customer.subscription.deleted": "subscription_canceled",
		"something.else":                "something.else",
	}
	for in, want := range cases {
		if got := reasonForEvent(in); got != want {
			t.Errorf("reasonForEvent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEventNameForDirection(t *testing.T) {
	if got := eventNameForDirection(DirectionUpgrade); got != "plan_upgraded" {
		t.Errorf("upgrade event name = %q", got)
	}
	if got := eventNameForDirection(DirectionDowngrade); got != "plan_downgraded" {
		t.Errorf("downgrade event name = %q", got)
	}
}

// fakeEmitter records Capture calls (and implements the io.Closer half of Emitter).
type fakeEmitter struct {
	events []analytics.Event
	closed bool
}

func (f *fakeEmitter) Capture(_ context.Context, ev analytics.Event) {
	f.events = append(f.events, ev)
}
func (f *fakeEmitter) Close() error { f.closed = true; return nil }

func TestNewPlanAnalytics_NilEmitter(t *testing.T) {
	if NewPlanAnalytics(nil) != nil {
		t.Error("NewPlanAnalytics(nil) must return nil so the store treats analytics as disabled")
	}
}

func TestCapturePlanChange_ShapesEvent(t *testing.T) {
	em := &fakeEmitter{}
	pa := NewPlanAnalytics(em)

	pa.CapturePlanChange(context.Background(), PlanChange{
		OrgID:     "org_123",
		FromTier:  TierBusiness,
		ToTier:    TierPro,
		Direction: DirectionDowngrade,
		Reason:    "subscription_updated",
	})

	if len(em.events) != 1 {
		t.Fatalf("expected 1 captured event, got %d", len(em.events))
	}
	ev := em.events[0]
	if ev.Event != "plan_downgraded" {
		t.Errorf("event = %q, want plan_downgraded", ev.Event)
	}
	if ev.DistinctID != "org_123" {
		t.Errorf("distinct id = %q, want org_123", ev.DistinctID)
	}
	if ev.Properties["from_tier"] != "business" || ev.Properties["to_tier"] != "pro" {
		t.Errorf("tier props = %v / %v", ev.Properties["from_tier"], ev.Properties["to_tier"])
	}
	if ev.Properties["direction"] != "downgrade" {
		t.Errorf("direction = %v", ev.Properties["direction"])
	}
	if ev.Properties["reason"] != "subscription_updated" {
		t.Errorf("reason = %v", ev.Properties["reason"])
	}
	if ev.Properties["$process_person_profile"] != false {
		t.Errorf("$process_person_profile = %v, want false", ev.Properties["$process_person_profile"])
	}
	if ev.Groups["organization"] != "org_123" {
		t.Errorf("group organization = %v, want org_123", ev.Groups["organization"])
	}
	// business ($150) → pro ($25): both prices known, so the MRR props are stamped.
	if ev.Properties["from_tier_mrr_usd"] != 150.0 || ev.Properties["to_tier_mrr_usd"] != 25.0 {
		t.Errorf("mrr props = %v / %v, want 150 / 25",
			ev.Properties["from_tier_mrr_usd"], ev.Properties["to_tier_mrr_usd"])
	}
	if ev.Properties["mrr_delta_usd"] != -125.0 {
		t.Errorf("mrr_delta_usd = %v, want -125", ev.Properties["mrr_delta_usd"])
	}
}

func TestCapturePlanChange_OmitsMRRForCustomPricing(t *testing.T) {
	em := &fakeEmitter{}
	pa := NewPlanAnalytics(em)

	pa.CapturePlanChange(context.Background(), PlanChange{
		OrgID:     "org_123",
		FromTier:  TierPro,
		ToTier:    TierEnterprise,
		Direction: DirectionUpgrade,
		Reason:    "subscription_updated",
	})

	if len(em.events) != 1 {
		t.Fatalf("expected 1 captured event, got %d", len(em.events))
	}
	ev := em.events[0]
	// Enterprise pricing is custom/negotiated: a fake $0 would corrupt MRR sums,
	// so all three MRR props must be absent.
	for _, k := range []string{"from_tier_mrr_usd", "to_tier_mrr_usd", "mrr_delta_usd"} {
		if _, present := ev.Properties[k]; present {
			t.Errorf("property %s must be omitted for custom-priced tiers, got %v", k, ev.Properties[k])
		}
	}
}

func TestCapturePlanChange_SkipsNoMoveAndEmptyOrg(t *testing.T) {
	em := &fakeEmitter{}
	pa := NewPlanAnalytics(em)

	pa.CapturePlanChange(context.Background(), PlanChange{OrgID: "org_1", Direction: directionNone})
	pa.CapturePlanChange(context.Background(), PlanChange{OrgID: "", Direction: DirectionUpgrade})

	if len(em.events) != 0 {
		t.Errorf("expected no events for no-op move / empty org, got %d", len(em.events))
	}
}

// fakePlanAnalytics records CapturePlanChange calls for the store gating test.
type fakePlanAnalytics struct{ calls []PlanChange }

func (f *fakePlanAnalytics) CapturePlanChange(_ context.Context, ev PlanChange) {
	f.calls = append(f.calls, ev)
}

func TestEmitPlanChange_Gating(t *testing.T) {
	ctx := context.Background()

	// No emitter wired: must be a safe no-op (no panic).
	(&BillingStore{}).emitPlanChange(ctx, "org_1",
		applyResult{fromTier: TierFree, toTier: TierPro, orgStatus: "active"}, "checkout.session.completed")

	fake := &fakePlanAnalytics{}
	s := (&BillingStore{}).WithPlanAnalytics(fake)

	// Upgrade → one call, reason from the checkout event.
	s.emitPlanChange(ctx, "org_1",
		applyResult{fromTier: TierFree, toTier: TierPro, orgStatus: "active"}, "checkout.session.completed")
	// Downgrade → one call, reason from the subscription.updated event.
	s.emitPlanChange(ctx, "org_1",
		applyResult{fromTier: TierBusiness, toTier: TierPro, orgStatus: "over_limit"}, "customer.subscription.updated")
	// Cancel → downgrade to free, reason subscription_canceled.
	s.emitPlanChange(ctx, "org_1",
		applyResult{fromTier: TierPro, toTier: TierFree, orgStatus: "active"}, "customer.subscription.deleted")
	// No tier move → no emit.
	s.emitPlanChange(ctx, "org_1",
		applyResult{fromTier: TierPro, toTier: TierPro, orgStatus: "active"}, "customer.subscription.updated")
	// Empty org → no emit.
	s.emitPlanChange(ctx, "",
		applyResult{fromTier: TierFree, toTier: TierPro, orgStatus: "active"}, "checkout.session.completed")

	if len(fake.calls) != 3 {
		t.Fatalf("expected 3 emitted plan changes, got %d: %+v", len(fake.calls), fake.calls)
	}
	if fake.calls[0].Direction != DirectionUpgrade || fake.calls[0].Reason != "checkout" {
		t.Errorf("call[0] = %+v, want upgrade/checkout", fake.calls[0])
	}
	if fake.calls[1].Direction != DirectionDowngrade || fake.calls[1].Reason != "subscription_updated" {
		t.Errorf("call[1] = %+v, want downgrade/subscription_updated", fake.calls[1])
	}
	if fake.calls[2].Direction != DirectionDowngrade || fake.calls[2].Reason != "subscription_canceled" {
		t.Errorf("call[2] = %+v, want downgrade/subscription_canceled", fake.calls[2])
	}
	if fake.calls[2].ToTier != TierFree {
		t.Errorf("cancel to_tier = %q, want free", fake.calls[2].ToTier)
	}
}
