//go:build cloud

package billing

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v84/webhook"
)

// signHeader builds a valid Stripe-Signature header (t=<unix>,v1=<hex hmac>) for a
// payload, mirroring exactly what Stripe sends — so RealSignatureVerifier exercises
// the real webhook.ConstructEvent path (timestamp tolerance + v1 signature).
func signHeader(t *testing.T, secret string, payload []byte, when time.Time) string {
	t.Helper()
	sig := webhook.ComputeSignature(when, payload, secret)
	return fmt.Sprintf("t=%d,v1=%s", when.Unix(), hex.EncodeToString(sig))
}

const whSecret = "whsec_real_test_secret"

func TestPriceMap_TierAndPriceRoundTrip(t *testing.T) {
	m := NewPriceMap("price_pro", "price_biz", "price_ent")

	if got := m.TierFor("price_biz"); got != TierBusiness {
		t.Errorf("TierFor(price_biz) = %q, want business", got)
	}
	if got := m.TierFor("price_ent"); got != TierEnterprise {
		t.Errorf("TierFor(price_ent) = %q, want enterprise", got)
	}
	if got := m.TierFor("price_unknown"); got != TierFree {
		t.Errorf("TierFor(unknown) = %q, want free", got)
	}
	if got := m.TierFor(""); got != TierFree {
		t.Errorf("TierFor(empty) = %q, want free", got)
	}

	if p, ok := m.PriceFor(TierBusiness); !ok || p != "price_biz" {
		t.Errorf("PriceFor(business) = %q,%v", p, ok)
	}
	if p, ok := m.PriceFor(TierEnterprise); !ok || p != "price_ent" {
		t.Errorf("PriceFor(enterprise) = %q,%v", p, ok)
	}
	if _, ok := m.PriceFor(TierFree); ok {
		t.Error("PriceFor(free) must be !ok (free has no checkout price)")
	}
}

// idObject must parse BOTH a bare id string and an expanded {id:…} object, because
// Stripe's expandable fields arrive either way.
func TestIDObject_ParsesBareAndExpanded(t *testing.T) {
	var bare idObject
	if err := json.Unmarshal([]byte(`"cus_123"`), &bare); err != nil || bare.ID != "cus_123" {
		t.Fatalf("bare = %+v err=%v", bare, err)
	}
	var expanded idObject
	if err := json.Unmarshal([]byte(`{"id":"cus_456","object":"customer"}`), &expanded); err != nil || expanded.ID != "cus_456" {
		t.Fatalf("expanded = %+v err=%v", expanded, err)
	}
	var null idObject
	if err := json.Unmarshal([]byte(`null`), &null); err != nil || null.ID != "" {
		t.Fatalf("null = %+v err=%v", null, err)
	}
}

func TestRealVerifier_CheckoutCompleted_ResolvesOrgAndTier(t *testing.T) {
	v := NewRealSignatureVerifier(whSecret, NewPriceMap("price_pro", "price_biz", "price_ent"))

	payload := []byte(`{
		"id":"evt_checkout_1",
		"object":"event",
		"type":"checkout.session.completed",
		"data":{"object":{
			"object":"checkout.session",
			"client_reference_id":"org_abc",
			"customer":"cus_abc",
			"subscription":"sub_abc",
			"metadata":{"org_id":"org_abc","target_tier":"business"}
		}}
	}`)
	header := signHeader(t, whSecret, payload, time.Now())

	ev, err := v.Verify(payload, header)
	if err != nil {
		t.Fatalf("verify valid signed payload: %v", err)
	}
	if ev.ID != "evt_checkout_1" || ev.Type != "checkout.session.completed" {
		t.Errorf("event id/type = %q/%q", ev.ID, ev.Type)
	}
	if ev.Data.OrgID != "org_abc" {
		t.Errorf("org id = %q, want org_abc", ev.Data.OrgID)
	}
	if ev.Data.PlanTier != TierBusiness {
		t.Errorf("plan tier = %q, want business", ev.Data.PlanTier)
	}
	if ev.Data.StripeCustomerID != "cus_abc" || ev.Data.StripeSubscriptionID != "sub_abc" {
		t.Errorf("customer/sub = %q/%q", ev.Data.StripeCustomerID, ev.Data.StripeSubscriptionID)
	}
}

func TestRealVerifier_SubscriptionUpdated_DerivesTierFromPrice(t *testing.T) {
	v := NewRealSignatureVerifier(whSecret, NewPriceMap("price_pro", "price_biz", "price_ent"))

	payload := []byte(`{
		"id":"evt_sub_1",
		"object":"event",
		"type":"customer.subscription.updated",
		"data":{"object":{
			"object":"subscription",
			"id":"sub_xyz",
			"customer":{"id":"cus_xyz","object":"customer"},
			"status":"active",
			"cancel_at_period_end":false,
			"current_period_end":1893456000,
			"metadata":{"org_id":"org_xyz"},
			"items":{"data":[{"quantity":7,"price":{"id":"price_ent"}}]}
		}}
	}`)
	header := signHeader(t, whSecret, payload, time.Now())

	ev, err := v.Verify(payload, header)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ev.Data.OrgID != "org_xyz" {
		t.Errorf("org id = %q", ev.Data.OrgID)
	}
	if ev.Data.PlanTier != TierEnterprise {
		t.Errorf("tier = %q, want enterprise (from price_ent)", ev.Data.PlanTier)
	}
	if ev.Data.Seats != 7 {
		t.Errorf("seats = %d, want 7", ev.Data.Seats)
	}
	if ev.Data.CurrentPeriodEnd != 1893456000 {
		t.Errorf("current_period_end = %d", ev.Data.CurrentPeriodEnd)
	}
}

// H6: a subscription line item whose price matches NEITHER configured tier price
// must set UnknownPrice (so applyEvent refuses to change entitlement) rather than
// silently resolving to Free — which would downgrade a paying org.
func TestRealVerifier_SubscriptionUpdated_UnknownPrice_Flagged(t *testing.T) {
	v := NewRealSignatureVerifier(whSecret, NewPriceMap("price_pro", "price_biz", "price_ent"))
	payload := []byte(`{
		"id":"evt_sub_unknown",
		"object":"event",
		"type":"customer.subscription.updated",
		"data":{"object":{
			"object":"subscription",
			"id":"sub_u",
			"customer":{"id":"cus_u","object":"customer"},
			"status":"active",
			"metadata":{"org_id":"org_u"},
			"items":{"data":[{"quantity":3,"price":{"id":"price_new_unmapped"}}]}
		}}
	}`)
	header := signHeader(t, whSecret, payload, time.Now())
	ev, err := v.Verify(payload, header)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ev.Data.UnknownPrice {
		t.Error("UnknownPrice should be true for an unmapped non-empty price (H6)")
	}
}

func TestRealVerifier_ForgedSignature_Rejected(t *testing.T) {
	v := NewRealSignatureVerifier(whSecret, NewPriceMap("price_pro", "price_biz", "price_ent"))
	payload := []byte(`{"id":"evt_forged","type":"checkout.session.completed","data":{"object":{}}}`)

	// Signed with the WRONG secret → ConstructEvent must reject.
	header := signHeader(t, "whsec_attacker", payload, time.Now())
	if _, err := v.Verify(payload, header); err == nil {
		t.Fatal("forged signature must be rejected")
	}

	// Garbage header → reject.
	if _, err := v.Verify(payload, "t=1,v1=deadbeef"); err == nil {
		t.Fatal("garbage signature must be rejected")
	}
}

func TestRealVerifier_StaleTimestamp_Rejected(t *testing.T) {
	v := NewRealSignatureVerifier(whSecret, NewPriceMap("price_pro", "price_biz", "price_ent"))
	payload := []byte(`{"id":"evt_old","type":"checkout.session.completed","data":{"object":{"client_reference_id":"o","metadata":{"target_tier":"business"}}}}`)
	// Correctly signed but far outside the default tolerance window.
	header := signHeader(t, whSecret, payload, time.Now().Add(-24*time.Hour))
	if _, err := v.Verify(payload, header); err == nil {
		t.Fatal("a valid signature with a stale timestamp must be rejected (replay protection)")
	}
}

func TestRealVerifier_MissingOrgID_Errors(t *testing.T) {
	v := NewRealSignatureVerifier(whSecret, NewPriceMap("price_pro", "price_biz", "price_ent"))
	payload := []byte(`{"id":"evt_noorg","object":"event","type":"checkout.session.completed","data":{"object":{"object":"checkout.session"}}}`)
	header := signHeader(t, whSecret, payload, time.Now())
	if _, err := v.Verify(payload, header); err == nil {
		t.Fatal("checkout.session.completed with no org id must error (never silently entitle)")
	}
}
