//go:build cloud

package billing

// Unit tests for the cloud billing → PostHog plan-change analytics: the pure
// upgrade/downgrade classification, the reason/event-name mapping, the synchronous
// HTTP capture (shape of the /capture/ payload), and the store's post-commit
// emitPlanChange gating (no emit on a no-op move, an empty org, or no emitter).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
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

func TestNewPostHogAnalytics_DisabledWithoutKey(t *testing.T) {
	if ph := NewPostHogAnalytics("", "https://us.i.posthog.com", "production", nil); ph != nil {
		t.Error("NewPostHogAnalytics with empty key must return nil (disabled)")
	}
	// A nil *PostHogAnalytics must be safe to call (defensive guard).
	var ph *PostHogAnalytics
	ph.CapturePlanChange(context.Background(), PlanChange{OrgID: "org_1", Direction: DirectionUpgrade})
}

func TestPostHogAnalytics_CaptureSendsExpectedPayload(t *testing.T) {
	type captured struct {
		APIKey     string         `json:"api_key"`
		Event      string         `json:"event"`
		DistinctID string         `json:"distinct_id"`
		Timestamp  string         `json:"timestamp"`
		Properties map[string]any `json:"properties"`
	}

	var (
		mu   sync.Mutex
		got  captured
		path string
		hits int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		hits++
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	ph := NewPostHogAnalytics("phc_test", srv.URL, "production", nil)
	if ph == nil {
		t.Fatal("expected a non-nil emitter for a configured key")
	}
	ph.CapturePlanChange(context.Background(), PlanChange{
		OrgID:     "org_123",
		FromTier:  TierBusiness,
		ToTier:    TierPro,
		Direction: DirectionDowngrade,
		Reason:    "subscription_updated",
	})

	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("expected exactly 1 capture POST, got %d", hits)
	}
	if path != "/capture/" {
		t.Errorf("POST path = %q, want /capture/", path)
	}
	if got.APIKey != "phc_test" {
		t.Errorf("api_key = %q", got.APIKey)
	}
	if got.Event != "plan_downgraded" {
		t.Errorf("event = %q, want plan_downgraded", got.Event)
	}
	if got.DistinctID != "org_123" {
		t.Errorf("distinct_id = %q, want org_123", got.DistinctID)
	}
	if got.Timestamp == "" {
		t.Error("timestamp must be set")
	}
	if got.Properties["from_tier"] != "business" || got.Properties["to_tier"] != "pro" {
		t.Errorf("tier props = %v / %v", got.Properties["from_tier"], got.Properties["to_tier"])
	}
	if got.Properties["direction"] != "downgrade" {
		t.Errorf("direction = %v", got.Properties["direction"])
	}
	if got.Properties["reason"] != "subscription_updated" {
		t.Errorf("reason = %v", got.Properties["reason"])
	}
	if got.Properties["environment"] != "production" {
		t.Errorf("environment = %v", got.Properties["environment"])
	}
	if got.Properties["organization"] != "org_123" {
		t.Errorf("organization = %v", got.Properties["organization"])
	}
	// Group analytics association so the dashboard can roll up per-org.
	groups, ok := got.Properties["$groups"].(map[string]any)
	if !ok || groups["organization"] != "org_123" {
		t.Errorf("$groups.organization = %v (ok=%v)", got.Properties["$groups"], ok)
	}
	// System event: no person profile should be minted.
	if got.Properties["$process_person_profile"] != false {
		t.Errorf("$process_person_profile = %v, want false", got.Properties["$process_person_profile"])
	}
}

func TestPostHogAnalytics_NoSendOnNoMove(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ph := NewPostHogAnalytics("phc_test", srv.URL, "production", nil)
	// directionNone and empty-org must not POST anything.
	ph.CapturePlanChange(context.Background(), PlanChange{OrgID: "org_1", Direction: directionNone})
	ph.CapturePlanChange(context.Background(), PlanChange{OrgID: "", Direction: DirectionUpgrade})
	if hits != 0 {
		t.Errorf("expected no capture POSTs, got %d", hits)
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
