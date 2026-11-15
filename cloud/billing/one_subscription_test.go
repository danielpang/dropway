//go:build cloud

package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"
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

// --- Two Checkout links before the first webhook ---------------------------
//
// The DB row only changes when a webhook lands, so the 409 above can't stop an
// org from opening two Checkout links (two tabs, a double click) before its first
// subscription is recorded. Paying both would create two subscriptions; ignoring
// the second webhook protects the plan but not the customer's card.

// A returning org (customer exists, no live subscription) opening a new Checkout
// expires its earlier open sessions first, so only the new link is payable.
func TestCheckout_ExpiresEarlierOpenSessions(t *testing.T) {
	store := &fakeCPStore{hasSub: true, sub: Subscription{StripeCustomerID: "cus_1", PlanTier: TierFree, Status: "canceled"}}
	sc := &fakeStripe{openSessions: 1}
	h := newHandlersUnderAuth(t, store, sc, claimsWith("org_1", "owner"))

	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"pro"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if sc.expired != 1 {
		t.Errorf("expired %d earlier sessions, want 1", sc.expired)
	}
	if sc.checkoutParams.CustomerID != "cus_1" {
		t.Errorf("new session customer = %q, want cus_1", sc.checkoutParams.CustomerID)
	}
}

// The first Checkout was paid but its webhook hasn't arrived: our row still says
// no subscription, Stripe already has a live one. Ask Stripe, and refuse.
func TestCheckout_PaidBeforeWebhook_409(t *testing.T) {
	store := &fakeCPStore{hasSub: true, sub: Subscription{StripeCustomerID: "cus_1", PlanTier: TierFree, Status: "active"}}
	sc := &fakeStripe{liveSub: true, openSessions: 1}
	h := newHandlersUnderAuth(t, store, sc, claimsWith("org_1", "owner"))

	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"pro"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", rr.Code, rr.Body.String())
	}
	if sc.checkoutParams.PriceID != "" || sc.expired != 0 {
		t.Error("no session may be created or expired once Stripe holds a live subscription")
	}
}

// If an earlier session can't be expired (e.g. it was paid in the meantime), fail
// closed: no new session. A retry then finds the paid subscription and 409s.
func TestCheckout_ExpireFails_NoNewSession(t *testing.T) {
	store := &fakeCPStore{hasSub: true, sub: Subscription{StripeCustomerID: "cus_1", PlanTier: TierFree, Status: "active"}}
	sc := &fakeStripe{openSessions: 1, expireErr: errors.New("session is not open")}
	h := newHandlersUnderAuth(t, store, sc, claimsWith("org_1", "owner"))

	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"pro"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", rr.Code, rr.Body.String())
	}
	if sc.checkoutParams.PriceID != "" {
		t.Error("no new session may be created when an earlier one could not be expired")
	}
}

// A brand-new customer has no subscriptions or sessions, so Stripe isn't asked.
func TestCheckout_NewCustomer_SkipsStripeChecks(t *testing.T) {
	store := &fakeCPStore{}
	sc := &fakeStripe{}
	h := newHandlersUnderAuth(t, store, sc, claimsWith("org_1", "owner"))

	if rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"pro"}`); rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if sc.liveChecked || sc.expired != 0 {
		t.Error("a brand-new customer needs no subscription or session lookup")
	}
}

// The reported scenario end to end: two Checkout requests for the same org at the
// same time, before any webhook. Exactly one Stripe customer and exactly one
// payable session may remain, for a new org and for a returning one.
func TestCheckout_ConcurrentRequests_OnePayableSession(t *testing.T) {
	cases := map[string]*memCheckoutStore{
		"new org":       {},
		"returning org": {has: true, sub: Subscription{StripeCustomerID: "cus_0", PlanTier: TierFree, Status: "canceled"}},
	}
	for name, store := range cases {
		t.Run(name, func(t *testing.T) {
			sc := newMemStripe()
			h := newHandlersUnderAuth(t, store, sc, claimsWith("org_1", "owner"))

			var wg sync.WaitGroup
			codes := make([]int, 2)
			for i := range codes {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					codes[i] = doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"pro"}`).Code
				}(i)
			}
			wg.Wait()

			for i, c := range codes {
				if c != http.StatusOK {
					t.Errorf("request %d status=%d, want 200", i, c)
				}
			}
			if cs := sc.customerIDs(); len(cs) != 1 {
				t.Errorf("sessions span customers %v, want exactly one", cs)
			}
			if open := sc.openCount(); open != 1 {
				t.Errorf("%d payable sessions remain, want exactly 1", open)
			}
		})
	}
}

// memCheckoutStore is a stateful CheckoutPortalStore whose WithCheckoutLock is a
// real mutex, like the production advisory lock.
type memCheckoutStore struct {
	lock sync.Mutex // the per-org checkout lock
	mu   sync.Mutex // guards sub/has
	sub  Subscription
	has  bool
}

func (s *memCheckoutStore) GetSubscription(context.Context, string) (Subscription, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sub, s.has, nil
}
func (s *memCheckoutStore) SaveCustomerID(_ context.Context, _, cid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sub = Subscription{StripeCustomerID: cid, PlanTier: TierFree, Status: "active"}
	s.has = true
	return nil
}
func (s *memCheckoutStore) ReadPlanTier(context.Context, string) (PlanTier, error) {
	return TierFree, nil
}
func (s *memCheckoutStore) WithCheckoutLock(_ context.Context, _ string, fn func() error) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	return fn()
}

// memStripe is a stateful StripeClient: it mints customers and tracks each
// Checkout session's customer and whether it is still open. Small sleeps widen
// the window two unserialized requests would interleave in.
type memStripe struct {
	mu        sync.Mutex
	customers int
	sessions  []memSession
}

type memSession struct {
	customer string
	open     bool
}

func newMemStripe() *memStripe { return &memStripe{} }

func (m *memStripe) EnsureCustomer(existingID, _, _ string) (string, error) {
	if existingID != "" {
		return existingID, nil
	}
	time.Sleep(5 * time.Millisecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.customers++
	return fmt.Sprintf("cus_new_%d", m.customers), nil
}
func (m *memStripe) CreateCheckoutSession(p CheckoutParams) (string, error) {
	time.Sleep(5 * time.Millisecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = append(m.sessions, memSession{customer: p.CustomerID, open: true})
	return fmt.Sprintf("https://checkout.stripe.test/%d", len(m.sessions)), nil
}
func (m *memStripe) CreatePortalSession(string, string) (string, error) {
	return "https://portal.stripe.test/session", nil
}
func (m *memStripe) HasLiveSubscription(string) (bool, error) { return false, nil }
func (m *memStripe) ExpireOpenCheckoutSessions(customerID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for i := range m.sessions {
		if m.sessions[i].customer == customerID && m.sessions[i].open {
			m.sessions[i].open = false
			n++
		}
	}
	return n, nil
}
func (m *memStripe) openCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sessions {
		if s.open {
			n++
		}
	}
	return n
}
func (m *memStripe) customerIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, s := range m.sessions {
		if !seen[s.customer] {
			seen[s.customer] = true
			out = append(out, s.customer)
		}
	}
	return out
}
