//go:build cloud

package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/middleware"
)

// fakeVerifier drives middleware.Auth without a live JWKS — it returns canned
// claims so the handlers see a given org id + role in context (exactly as a
// verified JWT would supply). Mirrors the handler-package test double.
type fakeVerifier struct{ claims *auth.Claims }

func (f fakeVerifier) Verify(context.Context, string) (*auth.Claims, error) { return f.claims, nil }

func claimsWith(orgID, role string) *auth.Claims {
	c := &auth.Claims{OrgID: orgID, Role: role, Email: "owner@example.com"}
	c.Subject = "user_1"
	return c
}

// fakeStripe records calls and returns canned URLs.
type fakeStripe struct {
	ensuredCustomer  string
	createdFromExist string
	checkoutParams   CheckoutParams
	portalCustomer   string
}

func (f *fakeStripe) EnsureCustomer(existingID, orgID, email string) (string, error) {
	f.createdFromExist = existingID
	if existingID != "" {
		f.ensuredCustomer = existingID
		return existingID, nil
	}
	f.ensuredCustomer = "cus_new_" + orgID
	return f.ensuredCustomer, nil
}
func (f *fakeStripe) CreateCheckoutSession(p CheckoutParams) (string, error) {
	f.checkoutParams = p
	return "https://checkout.stripe.test/session", nil
}
func (f *fakeStripe) CreatePortalSession(customerID, returnURL string) (string, error) {
	f.portalCustomer = customerID
	return "https://portal.stripe.test/session", nil
}

// fakeCPStore implements CheckoutPortalStore in-memory.
type fakeCPStore struct {
	sub      Subscription
	hasSub   bool
	savedCID string
	tier     PlanTier
}

func (f *fakeCPStore) GetSubscription(_ context.Context, _ string) (Subscription, bool, error) {
	return f.sub, f.hasSub, nil
}
func (f *fakeCPStore) SaveCustomerID(_ context.Context, _, cid string) error {
	f.savedCID = cid
	return nil
}
func (f *fakeCPStore) ReadPlanTier(_ context.Context, _ string) (PlanTier, error) {
	if f.tier == "" {
		return TierFree, nil
	}
	return f.tier, nil
}

// fakeRoles is a RoleChecker stub returning a fixed LIVE role / unavailable flag,
// independent of the JWT claim — so a test can simulate a demoted admin whose JWT
// still says "admin", or an unavailable member table.
type fakeRoles struct {
	role        string
	unavailable bool
	err         error
}

func (f fakeRoles) LiveRole(context.Context, string, string) (string, bool, error) {
	return f.role, f.unavailable, f.err
}

func newHandlersUnderAuth(t *testing.T, store CheckoutPortalStore, sc StripeClient, c *auth.Claims) http.Handler {
	t.Helper()
	// Default: the live member role agrees with the claim (the common case).
	return newHandlersUnderAuthLive(t, store, sc, c, fakeRoles{role: c.Role})
}

// newHandlersUnderAuthLive lets a test inject the LIVE auth.member role separately
// from the JWT claim role (to exercise the stale-claim re-check, FIX 2).
func newHandlersUnderAuthLive(t *testing.T, store CheckoutPortalStore, sc StripeClient, c *auth.Claims, roles RoleChecker) http.Handler {
	t.Helper()
	h := NewHandlers(store, sc, NewPriceMap("price_biz", "price_ent"), "https://app.dropway.dev", roles, false, nil)
	mux := http.NewServeMux()
	auth := middleware.Auth(fakeVerifier{claims: c})
	mux.Handle("POST /v1/billing/checkout", auth(http.HandlerFunc(h.Checkout)))
	mux.Handle("POST /v1/billing/portal", auth(http.HandlerFunc(h.Portal)))
	mux.Handle("GET /v1/billing", auth(http.HandlerFunc(h.Current)))
	return mux
}

// TestCheckout_StaleAdminClaim_DeniedByLiveRole is the FIX 2 regression test: a
// caller whose JWT still says role=admin but whose LIVE auth.member role is member
// must be denied (403) — billing re-checks the live role, never the stale claim.
func TestCheckout_StaleAdminClaim_DeniedByLiveRole(t *testing.T) {
	store := &fakeCPStore{}
	sc := &fakeStripe{}
	// JWT claim says admin; the live member table says member (demoted).
	h := newHandlersUnderAuthLive(t, store, sc, claimsWith("org_1", "admin"), fakeRoles{role: "member"})

	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (stale admin claim must be denied)", rr.Code)
	}
	if sc.checkoutParams.PriceID != "" || store.savedCID != "" {
		t.Error("no Stripe customer/session should be created for a denied caller")
	}
}

func doReq(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer x") // fakeVerifier ignores the token value
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestCheckout_OwnerCreatesSession(t *testing.T) {
	store := &fakeCPStore{}
	sc := &fakeStripe{}
	h := newHandlersUnderAuth(t, store, sc, claimsWith("org_1", "owner"))

	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business","seats":3}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["checkout_url"] == "" {
		t.Fatalf("no checkout_url in %s", rr.Body.String())
	}
	// The new customer id was persisted before checkout.
	if store.savedCID == "" {
		t.Error("expected SaveCustomerID for a first-time customer")
	}
	// Checkout was created with the right price, client_reference_id, and metadata.
	p := sc.checkoutParams
	if p.PriceID != "price_biz" {
		t.Errorf("price = %q, want price_biz", p.PriceID)
	}
	if p.ClientReferenceID != "org_1" {
		t.Errorf("client_reference_id = %q, want org_1", p.ClientReferenceID)
	}
	if p.Metadata["org_id"] != "org_1" || p.Metadata["target_tier"] != "business" {
		t.Errorf("metadata = %v", p.Metadata)
	}
	if p.Quantity != 3 {
		t.Errorf("seats = %d, want 3", p.Quantity)
	}
}

func TestCheckout_ReusesExistingCustomer(t *testing.T) {
	store := &fakeCPStore{hasSub: true, sub: Subscription{StripeCustomerID: "cus_existing"}}
	sc := &fakeStripe{}
	h := newHandlersUnderAuth(t, store, sc, claimsWith("org_1", "admin"))

	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"enterprise"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if sc.ensuredCustomer != "cus_existing" {
		t.Errorf("should reuse existing customer, got %q", sc.ensuredCustomer)
	}
	if store.savedCID != "" {
		t.Error("must NOT re-save customer id when one already exists")
	}
	if sc.checkoutParams.Quantity != 1 {
		t.Errorf("default seats should be 1, got %d", sc.checkoutParams.Quantity)
	}
}

func TestCheckout_MemberForbidden(t *testing.T) {
	h := newHandlersUnderAuth(t, &fakeCPStore{}, &fakeStripe{}, claimsWith("org_1", "member"))
	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"business"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("member must be 403, got %d", rr.Code)
	}
}

func TestCheckout_UnknownTier_400(t *testing.T) {
	h := newHandlersUnderAuth(t, &fakeCPStore{}, &fakeStripe{}, claimsWith("org_1", "owner"))
	rr := doReq(h, http.MethodPost, "/v1/billing/checkout", `{"target_tier":"free"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown/unpurchasable tier must be 400, got %d", rr.Code)
	}
}

func TestPortal_OwnerWithCustomer(t *testing.T) {
	store := &fakeCPStore{hasSub: true, sub: Subscription{StripeCustomerID: "cus_p"}}
	sc := &fakeStripe{}
	h := newHandlersUnderAuth(t, store, sc, claimsWith("org_1", "owner"))

	rr := doReq(h, http.MethodPost, "/v1/billing/portal", `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if sc.portalCustomer != "cus_p" {
		t.Errorf("portal customer = %q, want cus_p", sc.portalCustomer)
	}
}

func TestPortal_NoCustomer_409(t *testing.T) {
	h := newHandlersUnderAuth(t, &fakeCPStore{hasSub: false}, &fakeStripe{}, claimsWith("org_1", "owner"))
	rr := doReq(h, http.MethodPost, "/v1/billing/portal", `{}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("no customer must be 409, got %d", rr.Code)
	}
}

func TestPortal_MemberForbidden(t *testing.T) {
	h := newHandlersUnderAuth(t, &fakeCPStore{}, &fakeStripe{}, claimsWith("org_1", "member"))
	rr := doReq(h, http.MethodPost, "/v1/billing/portal", `{}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("member must be 403, got %d", rr.Code)
	}
}

func TestCurrent_AnyMemberReadsPlan(t *testing.T) {
	store := &fakeCPStore{tier: TierBusiness, hasSub: true, sub: Subscription{Status: "active", OrgStatus: "active", Seats: 4}}
	h := newHandlersUnderAuth(t, store, &fakeStripe{}, claimsWith("org_1", "member"))

	rr := doReq(h, http.MethodGet, "/v1/billing", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp currentPlanResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.PlanTier != TierBusiness || resp.Status != "active" || resp.Seats != 4 {
		t.Errorf("resp = %+v", resp)
	}
}
