//go:build cloud

package billing

// stripe_parse_test.go drives the verified-event → EventData parsing branches of
// RealSignatureVerifier (resolveData / fromCheckoutSession / fromSubscription /
// resolveOrgID / idObject) that the happy-path tests in stripe_test.go don't reach,
// plus the StubSignatureVerifier's error branches. Parsing is pure (post-verify),
// so we call the resolvers directly with raw JSON where a signed envelope isn't
// needed.

import (
	"encoding/json"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v84"
)

func newVerifier() RealSignatureVerifier {
	return NewRealSignatureVerifier(whSecret, NewPriceMap("price_pro", "price_biz", "price_ent"))
}

// resolveData returns an empty EventData (no error) when the event carries no data
// object, and for unhandled types acknowledges with an empty EventData so dedupe/
// logging still have an envelope.
func TestResolveData_EmptyAndUnhandled(t *testing.T) {
	v := newVerifier()

	// Nil Data → empty, no error.
	if d, err := v.resolveData(stripe.Event{Type: "checkout.session.completed"}); err != nil || d != (EventData{}) {
		t.Errorf("nil data: got %+v err=%v, want empty/nil", d, err)
	}
	// Empty Raw → empty, no error.
	if d, err := v.resolveData(stripe.Event{Type: "checkout.session.completed", Data: &stripe.EventData{Raw: json.RawMessage{}}}); err != nil || d != (EventData{}) {
		t.Errorf("empty raw: got %+v err=%v, want empty/nil", d, err)
	}
	// An unhandled type (invoice.paid) is acknowledged with an empty EventData.
	raw := json.RawMessage(`{"id":"in_1","object":"invoice"}`)
	if d, err := v.resolveData(stripe.Event{Type: "invoice.paid", Data: &stripe.EventData{Raw: raw}}); err != nil || d != (EventData{}) {
		t.Errorf("unhandled type: got %+v err=%v, want empty/nil", d, err)
	}
}

// fromCheckoutSession resolves the org from metadata.org_id when there is no
// client_reference_id, honors the enterprise target_tier, and defaults to Free when
// no purchasable target_tier is present (the follow-up subscription event sets it).
func TestFromCheckoutSession_OrgAndTierResolution(t *testing.T) {
	v := newVerifier()

	// No client_reference_id → fall back to metadata.org_id; enterprise target_tier.
	d, err := v.fromCheckoutSession([]byte(`{
		"customer":"cus_1","subscription":"sub_1",
		"metadata":{"org_id":"org_meta","target_tier":"enterprise"}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.OrgID != "org_meta" {
		t.Errorf("org id = %q, want org_meta (from metadata.org_id)", d.OrgID)
	}
	if d.PlanTier != TierEnterprise {
		t.Errorf("tier = %q, want enterprise", d.PlanTier)
	}
	if d.Status != "active" {
		t.Errorf("checkout status should be active, got %q", d.Status)
	}

	// client_reference_id wins over metadata.org_id; no/invalid target_tier → Free.
	d2, err := v.fromCheckoutSession([]byte(`{
		"client_reference_id":"org_ref","customer":"cus_2",
		"metadata":{"org_id":"org_meta","target_tier":"free"}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d2.OrgID != "org_ref" {
		t.Errorf("org id = %q, want org_ref (client_reference_id wins)", d2.OrgID)
	}
	if d2.PlanTier != TierFree {
		t.Errorf("tier = %q, want free (a non-purchasable target_tier defaults to free)", d2.PlanTier)
	}

	// No metadata at all → Free, org from client_reference_id.
	d3, err := v.fromCheckoutSession([]byte(`{"client_reference_id":"org_only"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d3.OrgID != "org_only" || d3.PlanTier != TierFree {
		t.Errorf("got org=%q tier=%q, want org_only/free", d3.OrgID, d3.PlanTier)
	}
}

// A checkout session with no resolvable org id is an error (never silently entitle).
func TestFromCheckoutSession_MissingOrg_Errors(t *testing.T) {
	if _, err := newVerifier().fromCheckoutSession([]byte(`{"customer":"cus_x"}`)); err == nil {
		t.Fatal("checkout with no org id must error")
	}
}

// Malformed JSON in either resolver is a parse error (not a silent empty event).
func TestResolvers_MalformedJSON_Error(t *testing.T) {
	v := newVerifier()
	if _, err := v.fromCheckoutSession([]byte(`{not json`)); err == nil {
		t.Error("malformed checkout session JSON must error")
	}
	if _, err := v.fromSubscription([]byte(`{not json`)); err == nil {
		t.Error("malformed subscription JSON must error")
	}
}

// fromSubscription with no line items leaves seats=0 and tier=Free (no price to
// derive from), and still resolves the org from metadata.
func TestFromSubscription_NoItems_FreeZeroSeats(t *testing.T) {
	d, err := newVerifier().fromSubscription([]byte(`{
		"id":"sub_noitems","customer":"cus_1","status":"active",
		"current_period_end":111,"metadata":{"org_id":"org_ni"},
		"items":{"data":[]}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.OrgID != "org_ni" {
		t.Errorf("org = %q, want org_ni", d.OrgID)
	}
	if d.Seats != 0 {
		t.Errorf("seats = %d, want 0 (no line items)", d.Seats)
	}
	if d.PlanTier != TierFree {
		t.Errorf("tier = %q, want free (no price)", d.PlanTier)
	}
	if d.CurrentPeriodEnd != 111 {
		t.Errorf("current_period_end = %d, want 111", d.CurrentPeriodEnd)
	}
}

// When the top-level current_period_end is absent (0), it is back-filled from the
// line item's current_period_end (newer API versions put it on the item).
func TestFromSubscription_PeriodEndFromItem(t *testing.T) {
	d, err := newVerifier().fromSubscription([]byte(`{
		"id":"sub_item_pe","customer":"cus_2","status":"active",
		"cancel_at_period_end":true,
		"metadata":{"org_id":"org_pe"},
		"items":{"data":[{"quantity":3,"price":{"id":"price_biz"},"current_period_end":222}]}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.CurrentPeriodEnd != 222 {
		t.Errorf("current_period_end = %d, want 222 (back-filled from item)", d.CurrentPeriodEnd)
	}
	if d.PlanTier != TierBusiness {
		t.Errorf("tier = %q, want business (from price_biz)", d.PlanTier)
	}
	if d.Seats != 3 {
		t.Errorf("seats = %d, want 3", d.Seats)
	}
	if !d.CancelAtPeriodEnd {
		t.Error("cancel_at_period_end must be carried through")
	}
}

// A top-level current_period_end is NOT overwritten by the item's value.
func TestFromSubscription_TopLevelPeriodEndWins(t *testing.T) {
	d, err := newVerifier().fromSubscription([]byte(`{
		"id":"sub_pe","customer":"cus_3","status":"active",
		"current_period_end":999,"metadata":{"org_id":"org_x"},
		"items":{"data":[{"quantity":1,"price":{"id":"price_ent"},"current_period_end":222}]}
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.CurrentPeriodEnd != 999 {
		t.Errorf("current_period_end = %d, want 999 (top-level wins over item)", d.CurrentPeriodEnd)
	}
}

// A subscription event with no resolvable org id (no metadata.org_id) errors.
func TestFromSubscription_MissingOrg_Errors(t *testing.T) {
	if _, err := newVerifier().fromSubscription([]byte(`{"id":"sub_x","customer":"cus_x","items":{"data":[]}}`)); err == nil {
		t.Fatal("subscription with no org id must error")
	}
}

// resolveOrgID prefers client_reference_id, then metadata.org_id, then "".
func TestResolveOrgID_Precedence(t *testing.T) {
	if got := resolveOrgID("ref", map[string]string{"org_id": "meta"}); got != "ref" {
		t.Errorf("client_reference_id must win, got %q", got)
	}
	if got := resolveOrgID("", map[string]string{"org_id": "meta"}); got != "meta" {
		t.Errorf("fall back to metadata.org_id, got %q", got)
	}
	if got := resolveOrgID("", map[string]string{"other": "x"}); got != "" {
		t.Errorf("no org id available must be empty, got %q", got)
	}
	if got := resolveOrgID("", nil); got != "" {
		t.Errorf("nil metadata must be empty, got %q", got)
	}
}

// idObject surfaces a JSON error when an expanded object's body is malformed.
func TestIDObject_MalformedExpanded_Errors(t *testing.T) {
	var o idObject
	if err := o.UnmarshalJSON([]byte(`{"id":`)); err == nil {
		t.Error("a malformed expanded id object must surface a JSON error")
	}
	// Empty bytes is a tolerated no-op (no id, no error).
	var empty idObject
	if err := empty.UnmarshalJSON([]byte{}); err != nil || empty.ID != "" {
		t.Errorf("empty bytes: got id=%q err=%v, want empty/nil", empty.ID, err)
	}
}

// Verify maps a verified subscription.deleted event through fromSubscription end to
// end (covers the deleted branch of resolveData's switch under a real signature).
func TestRealVerifier_SubscriptionDeleted_Resolves(t *testing.T) {
	v := newVerifier()
	payload := []byte(`{
		"id":"evt_del_1","object":"event","type":"customer.subscription.deleted",
		"data":{"object":{
			"object":"subscription","id":"sub_del","customer":"cus_del",
			"status":"canceled","metadata":{"org_id":"org_del"},
			"items":{"data":[{"quantity":2,"price":{"id":"price_biz"}}]}
		}}
	}`)
	ev, err := v.Verify(payload, signHeader(t, whSecret, payload, time.Now()))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ev.Type != "customer.subscription.deleted" || ev.Data.OrgID != "org_del" {
		t.Errorf("event = %+v, want type=deleted org=org_del", ev)
	}
	if ev.Data.Status != "canceled" {
		t.Errorf("status = %q, want canceled", ev.Data.Status)
	}
}

// The StubSignatureVerifier rejects a well-signed payload that decodes to an event
// with no id (the dedupe ledger PK can't be empty), and surfaces a JSON error for a
// signed-but-malformed payload.
func TestStubVerifier_ErrorBranches(t *testing.T) {
	stub := StubSignatureVerifier{Secret: secret}

	// Correctly signed but the event has no id.
	noID := []byte(`{"type":"checkout.session.completed"}`)
	if _, err := stub.Verify(noID, Sign(secret, noID)); err == nil {
		t.Error("an event with no id must be rejected (dedupe PK)")
	}

	// Correctly signed but not valid JSON for an Event.
	bad := []byte(`{not json`)
	if _, err := stub.Verify(bad, Sign(secret, bad)); err == nil {
		t.Error("a signed-but-malformed payload must surface a JSON error")
	}

	// Sanity: a well-formed signed event round-trips.
	ok := []byte(`{"id":"evt_ok","type":"invoice.created"}`)
	ev, err := stub.Verify(ok, Sign(secret, ok))
	if err != nil || ev.ID != "evt_ok" {
		t.Errorf("valid signed event: ev=%+v err=%v", ev, err)
	}
}
