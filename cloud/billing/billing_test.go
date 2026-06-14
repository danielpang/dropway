//go:build cloud

package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeDedupe records ids and reports duplicates.
type fakeDedupe struct {
	seen map[string]bool
	err  error
}

func (f *fakeDedupe) MarkProcessed(_ context.Context, id string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[id] {
		return true, nil
	}
	f.seen[id] = true
	return false, nil
}

// fakeSubs records the last persistence call.
type fakeSubs struct {
	upserts  []EventData
	canceled []string
}

func (f *fakeSubs) UpsertSubscription(_ context.Context, d EventData) error {
	f.upserts = append(f.upserts, d)
	return nil
}
func (f *fakeSubs) SetCanceled(_ context.Context, orgID string) error {
	f.canceled = append(f.canceled, orgID)
	return nil
}

const secret = "whsec_test"

func post(t *testing.T, h *Handler, ev Event, withSig bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(string(body)))
	if withSig {
		req.Header.Set("Stripe-Signature", Sign(secret, body))
	} else {
		req.Header.Set("Stripe-Signature", "deadbeef")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func newHandler(d DedupeStore, s SubscriptionStore) *Handler {
	return NewHandler(StubSignatureVerifier{Secret: secret}, d, s, nil)
}

func TestWebhook_CheckoutCompleted_PersistsTier(t *testing.T) {
	subs := &fakeSubs{}
	h := newHandler(&fakeDedupe{}, subs)

	ev := Event{
		ID:   "evt_1",
		Type: "checkout.session.completed",
		Data: EventData{OrgID: "org_1", PlanTier: TierBusiness, Seats: 7, Status: "active"},
	}
	rr := post(t, h, ev, true)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody=%s", rr.Code, rr.Body.String())
	}
	if len(subs.upserts) != 1 {
		t.Fatalf("got %d upserts, want 1", len(subs.upserts))
	}
	if subs.upserts[0].PlanTier != TierBusiness || subs.upserts[0].OrgID != "org_1" {
		t.Errorf("upsert = %+v", subs.upserts[0])
	}
}

func TestWebhook_BadSignature_400_NoPersist(t *testing.T) {
	subs := &fakeSubs{}
	h := newHandler(&fakeDedupe{}, subs)

	ev := Event{ID: "evt_2", Type: "checkout.session.completed", Data: EventData{OrgID: "org_x"}}
	rr := post(t, h, ev, false) // wrong signature

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if len(subs.upserts) != 0 {
		t.Error("entitlement must NOT be written when the signature is invalid")
	}
}

func TestWebhook_DuplicateEvent_Ignored(t *testing.T) {
	subs := &fakeSubs{}
	dd := &fakeDedupe{}
	h := newHandler(dd, subs)

	ev := Event{ID: "evt_dup", Type: "customer.subscription.updated", Data: EventData{OrgID: "o", PlanTier: TierBusiness}}

	if rr := post(t, h, ev, true); rr.Code != http.StatusOK {
		t.Fatalf("first delivery status = %d", rr.Code)
	}
	if rr := post(t, h, ev, true); rr.Code != http.StatusOK {
		t.Fatalf("replay status = %d", rr.Code)
	}
	if len(subs.upserts) != 1 {
		t.Errorf("got %d upserts across a duplicate delivery, want 1", len(subs.upserts))
	}
}

func TestWebhook_SubscriptionDeleted_Downgrades(t *testing.T) {
	subs := &fakeSubs{}
	h := newHandler(&fakeDedupe{}, subs)

	ev := Event{ID: "evt_del", Type: "customer.subscription.deleted", Data: EventData{OrgID: "org_9"}}
	rr := post(t, h, ev, true)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if len(subs.canceled) != 1 || subs.canceled[0] != "org_9" {
		t.Errorf("canceled = %v, want [org_9]", subs.canceled)
	}
}

func TestWebhook_UnhandledType_200NoOp(t *testing.T) {
	subs := &fakeSubs{}
	h := newHandler(&fakeDedupe{}, subs)

	ev := Event{ID: "evt_misc", Type: "invoice.created", Data: EventData{OrgID: "o"}}
	rr := post(t, h, ev, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for unhandled type", rr.Code)
	}
	if len(subs.upserts) != 0 || len(subs.canceled) != 0 {
		t.Error("unhandled type must be a no-op")
	}
}

func TestWebhook_RejectsGET(t *testing.T) {
	h := newHandler(&fakeDedupe{}, &fakeSubs{})
	req := httptest.NewRequest(http.MethodGet, "/webhooks/stripe", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}
