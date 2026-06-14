package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielpang/shipped/internal/auth"
	"github.com/danielpang/shipped/internal/httpx"
	"github.com/danielpang/shipped/internal/middleware"
	"github.com/danielpang/shipped/internal/quota"
)

// withClaims returns a request whose context carries the given claims, the same
// way the Auth middleware would. We can't set the unexported key directly, so we
// run a real Auth middleware with a fake verifier in front of the handler in the
// tests that need it; here we use the exported test seam via middleware.Auth.
func authed(handler http.HandlerFunc, c *auth.Claims) http.Handler {
	v := fakeVerifier{claims: c}
	return middleware.Auth(v)(handler)
}

type fakeVerifier struct{ claims *auth.Claims }

func (f fakeVerifier) Verify(context.Context, string) (*auth.Claims, error) {
	return f.claims, nil
}

func claims(user, org, role string) *auth.Claims {
	c := &auth.Claims{OrgID: org, Role: role}
	c.Subject = user
	return c
}

func TestHealthz(t *testing.T) {
	a := New(quota.Unlimited{})
	rr := httptest.NewRecorder()
	a.Healthz(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("body = %v", body)
	}
}

func TestMe_EchoesClaims(t *testing.T) {
	a := New(quota.Unlimited{})
	h := authed(a.Me, claims("user_1", "org_1", "owner"))

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var body meResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.UserID != "user_1" || body.OrgID != "org_1" || body.Role != "owner" {
		t.Errorf("me = %+v", body)
	}
}

func TestCreateSite_Unlimited_201(t *testing.T) {
	a := New(quota.Unlimited{})
	h := authed(a.CreateSite, claims("user_1", "org_1", "member"))

	req := httptest.NewRequest(http.MethodPost, "/v1/sites", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}
	var body siteResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.OrgID != "org_1" || body.OwnerID != "user_1" {
		t.Errorf("site = %+v", body)
	}
}

// quotaStub returns a configured ExceededError to exercise the 402 path.
type quotaStub struct{ err error }

func (q quotaStub) CheckAndReserve(context.Context, string, string, quota.Resource) error {
	return q.err
}

func TestCreateSite_QuotaExceeded_402(t *testing.T) {
	ex := &quota.ExceededError{
		Limit:      quota.ResourceSitePerUser,
		Current:    10,
		Max:        10,
		PlanTier:   "free",
		NextTier:   "business",
		UpgradeURL: "https://app.shipped.app/billing/upgrade?tier=business",
	}
	a := New(quotaStub{err: ex})
	h := authed(a.CreateSite, claims("user_1", "org_1", "member"))

	req := httptest.NewRequest(http.MethodPost, "/v1/sites", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rr.Code)
	}
	var body quota.ExceededError
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.NextTier != "business" || body.UpgradeURL == "" {
		t.Errorf("402 body = %+v", body)
	}
}

// Ensure the package's unauthorized helper maps via httpx (defensive branch).
func TestCreateSite_NoClaims_401(t *testing.T) {
	a := New(quota.Unlimited{})
	// Call the handler directly with a bare context (no Auth middleware).
	req := httptest.NewRequest(http.MethodPost, "/v1/sites", nil)
	rr := httptest.NewRecorder()
	a.CreateSite(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
	_ = httpx.ErrUnauthorized // keep import meaningful/documented
}
