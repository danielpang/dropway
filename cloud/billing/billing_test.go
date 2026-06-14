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

// fakeProcessor is an in-memory EventProcessor that mirrors the production store's
// ATOMIC dedupe+apply contract (FIX 1): it records the event id and applies the
// entitlement change as one unit. A replay (already-seen id) is a no-op
// (applied=false). If the apply errors, the id is NOT recorded — so a retry of the
// same id re-applies cleanly, exactly like the single-tx store. It also records the
// applied upserts/cancels so the handler tests can assert what was persisted.
type fakeProcessor struct {
	seen     map[string]bool
	upserts  []EventData
	canceled []string

	// dedupeErr fails the ledger step (a transient store error → handler 500).
	dedupeErr error
	// applyErrFor injects a (transient) apply error for the given event ids on
	// their FIRST delivery only; the id is left unrecorded so a retry succeeds.
	applyErrFor map[string]error
	// unfulfillableFor injects errUnfulfillableEvent for the given event ids
	// (permanent → handler 400, no retry).
	unfulfillableFor map[string]bool
}

func (f *fakeProcessor) ProcessEvent(_ context.Context, ev Event) (bool, error) {
	if f.dedupeErr != nil {
		return false, f.dedupeErr
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[ev.ID] {
		return false, nil // duplicate → no-op
	}
	if f.unfulfillableFor[ev.ID] {
		// Permanent failure: nothing recorded, nothing applied. The handler maps
		// this to a 400 acknowledgment so Stripe stops retrying.
		return false, errUnfulfillableEvent
	}
	if err := f.applyErrFor[ev.ID]; err != nil {
		// Transient apply failure: roll back atomically — DON'T record the id, so a
		// retry re-applies. Consume the injected error (next delivery succeeds).
		delete(f.applyErrFor, ev.ID)
		return false, err
	}
	// Apply: dispatch by type into the recorded slices, then record the id (atomic).
	switch ev.Type {
	case "checkout.session.completed",
		"customer.subscription.created",
		"customer.subscription.updated":
		f.upserts = append(f.upserts, ev.Data)
	case "customer.subscription.deleted":
		f.canceled = append(f.canceled, ev.Data.OrgID)
	default:
		// Unhandled type: record the id (so it isn't reprocessed) but no entitlement
		// write — applied=true, no-op.
	}
	f.seen[ev.ID] = true
	return true, nil
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

func newHandler(p EventProcessor) *Handler {
	return NewHandler(StubSignatureVerifier{Secret: secret}, p, nil)
}

func TestWebhook_CheckoutCompleted_PersistsTier(t *testing.T) {
	proc := &fakeProcessor{}
	h := newHandler(proc)

	ev := Event{
		ID:   "evt_1",
		Type: "checkout.session.completed",
		Data: EventData{OrgID: "org_1", PlanTier: TierBusiness, Seats: 7, Status: "active"},
	}
	rr := post(t, h, ev, true)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody=%s", rr.Code, rr.Body.String())
	}
	if len(proc.upserts) != 1 {
		t.Fatalf("got %d upserts, want 1", len(proc.upserts))
	}
	if proc.upserts[0].PlanTier != TierBusiness || proc.upserts[0].OrgID != "org_1" {
		t.Errorf("upsert = %+v", proc.upserts[0])
	}
}

func TestWebhook_BadSignature_400_NoPersist(t *testing.T) {
	proc := &fakeProcessor{}
	h := newHandler(proc)

	ev := Event{ID: "evt_2", Type: "checkout.session.completed", Data: EventData{OrgID: "org_x"}}
	rr := post(t, h, ev, false) // wrong signature

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if len(proc.upserts) != 0 {
		t.Error("entitlement must NOT be written when the signature is invalid")
	}
}

func TestWebhook_DuplicateEvent_Ignored(t *testing.T) {
	proc := &fakeProcessor{}
	h := newHandler(proc)

	ev := Event{ID: "evt_dup", Type: "customer.subscription.updated", Data: EventData{OrgID: "o", PlanTier: TierBusiness}}

	if rr := post(t, h, ev, true); rr.Code != http.StatusOK {
		t.Fatalf("first delivery status = %d", rr.Code)
	}
	if rr := post(t, h, ev, true); rr.Code != http.StatusOK {
		t.Fatalf("replay status = %d", rr.Code)
	}
	if len(proc.upserts) != 1 {
		t.Errorf("got %d upserts across a duplicate delivery, want 1", len(proc.upserts))
	}
}

func TestWebhook_SubscriptionDeleted_Downgrades(t *testing.T) {
	proc := &fakeProcessor{}
	h := newHandler(proc)

	ev := Event{ID: "evt_del", Type: "customer.subscription.deleted", Data: EventData{OrgID: "org_9"}}
	rr := post(t, h, ev, true)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if len(proc.canceled) != 1 || proc.canceled[0] != "org_9" {
		t.Errorf("canceled = %v, want [org_9]", proc.canceled)
	}
}

func TestWebhook_UnhandledType_200NoOp(t *testing.T) {
	proc := &fakeProcessor{}
	h := newHandler(proc)

	ev := Event{ID: "evt_misc", Type: "invoice.created", Data: EventData{OrgID: "o"}}
	rr := post(t, h, ev, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for unhandled type", rr.Code)
	}
	if len(proc.upserts) != 0 || len(proc.canceled) != 0 {
		t.Error("unhandled type must be a no-op")
	}
	// The id must still be recorded so the event isn't reprocessed.
	if !proc.seen["evt_misc"] {
		t.Error("unhandled type must still record the event id (no reprocessing)")
	}
}

func TestWebhook_RejectsGET(t *testing.T) {
	h := newHandler(&fakeProcessor{})
	req := httptest.NewRequest(http.MethodGet, "/webhooks/stripe", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}

// TestWebhook_ApplyFails_ThenRetrySucceeds is the FIX 1 regression test: the lost-
// update bug. On the FIRST delivery the apply ERRORS, so the handler 500s and — because
// dedupe+apply are atomic — the event id is NOT recorded (no ledger row committed).
// On the SECOND delivery of the SAME id the apply succeeds and the entitlement flips.
// The old design committed the ledger row before the apply, so the retry would have
// short-circuited to "duplicate_ignored" and the plan_tier would have stayed stale.
func TestWebhook_ApplyFails_ThenRetrySucceeds(t *testing.T) {
	proc := &fakeProcessor{
		applyErrFor: map[string]error{"evt_flap": context.DeadlineExceeded},
	}
	h := newHandler(proc)

	ev := Event{
		ID:   "evt_flap",
		Type: "checkout.session.completed",
		Data: EventData{OrgID: "org_pay", PlanTier: TierBusiness, Status: "active"},
	}

	// First delivery: apply fails → 500, NOTHING applied, id NOT recorded.
	rr := post(t, h, ev, true)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("first delivery status = %d, want 500\nbody=%s", rr.Code, rr.Body.String())
	}
	if len(proc.upserts) != 0 {
		t.Fatalf("apply failure must persist nothing, got %d upserts", len(proc.upserts))
	}
	if proc.seen["evt_flap"] {
		t.Fatal("a failed apply must NOT record the event id (atomic rollback) — else the retry is swallowed")
	}

	// Stripe retries the SAME event id: now the apply succeeds and plan_tier flips.
	rr = post(t, h, ev, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200\nbody=%s", rr.Code, rr.Body.String())
	}
	if len(proc.upserts) != 1 || proc.upserts[0].PlanTier != TierBusiness {
		t.Fatalf("retry must apply the entitlement; upserts=%+v", proc.upserts)
	}
}

// TestWebhook_DedupeStoreError_500 covers a transient ledger/store failure: the
// handler 500s (Stripe retries) and nothing is applied.
func TestWebhook_DedupeStoreError_500(t *testing.T) {
	proc := &fakeProcessor{dedupeErr: context.DeadlineExceeded}
	h := newHandler(proc)

	ev := Event{ID: "evt_dberr", Type: "checkout.session.completed", Data: EventData{OrgID: "o", PlanTier: TierBusiness}}
	rr := post(t, h, ev, true)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if len(proc.upserts) != 0 {
		t.Error("nothing must be applied on a store error")
	}
}

// TestWebhook_UnfulfillableEvent_400 covers FIX 3 at the handler seam: a permanent
// (non-retryable) apply failure is acknowledged with a 400 so Stripe stops retrying.
func TestWebhook_UnfulfillableEvent_400(t *testing.T) {
	proc := &fakeProcessor{unfulfillableFor: map[string]bool{"evt_nocust": true}}
	h := newHandler(proc)

	ev := Event{ID: "evt_nocust", Type: "customer.subscription.updated", Data: EventData{OrgID: "o"}}
	rr := post(t, h, ev, true)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unfulfillable event\nbody=%s", rr.Code, rr.Body.String())
	}
	if len(proc.upserts) != 0 {
		t.Error("an unfulfillable event must persist nothing")
	}
}
