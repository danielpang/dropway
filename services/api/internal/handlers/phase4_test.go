package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/edgerevoke"
	"github.com/danielpang/dropway/internal/logx"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// fakeRevoker is an in-memory projection.Revoker for handler tests. It records the
// max(min_iat) per (kind,id) like the real writers, so a test can assert exactly
// what a revoke handler wrote to the denylist.
type fakeRevoker struct {
	mu      sync.Mutex
	entries map[string]int64
	err     error
}

func newFakeRevoker() *fakeRevoker { return &fakeRevoker{entries: map[string]int64{}} }

func (f *fakeRevoker) Revoke(_ context.Context, kind edgerevoke.Kind, id string, minIAT int64) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := edgerevoke.Key(kind, id)
	if cur, ok := f.entries[k]; !ok || minIAT > cur {
		f.entries[k] = minIAT
	}
	return nil
}

func (f *fakeRevoker) get(kind edgerevoke.Kind, id string) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.entries[edgerevoke.Key(kind, id)]
	return v, ok
}

// mountPhase4 wires the Phase-4 routes (audit + revocation) plus the Phase-2 ones
// the tests reuse, with chi's RequestID middleware in front so logx.RequestID flows
// into the audit row.
func mountPhase4(a *API, c *auth.Claims) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(logx.Middleware(nil))
	v := fakeVerifier{claims: c}
	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Post("/v1/sites", a.CreateSite)
		r.Put("/v1/sites/{id}/access", a.SetSiteAccess)
		r.Post("/v1/sites/{id}/revoke-access", a.RevokeSiteAccess)
		r.Post("/v1/members/{userId}/revoke", a.RevokeMember)
		r.Post("/v1/members/invites", a.RecordMemberInvite)
		r.Post("/v1/members/joined", a.RecordMemberJoin)
		r.Post("/v1/orgs/revoke-access", a.RevokeAccess)
		r.Get("/v1/audit", a.ListAudit)
		r.Put("/v1/orgs/allow-external-sharing", a.SetAllowExternalSharing)
	})
	return r
}

func adminClaims() *auth.Claims  { return claims("u-admin", "org-1", "admin") }
func memberClaims() *auth.Claims { return claims("u-member", "org-1", "member") }

// seedSite adds a published site to the fake store so access/revoke handlers find it.
func seedSite(f *fakeStore, id, slug string) {
	ver := "ver_x"
	f.sites[id] = store.Site{ID: id, OrgID: "org-1", Slug: slug, AccessMode: "public", CurrentVersionID: &ver}
}

// --- audit: a site create writes the expected audit row ------------------------

func TestAudit_SiteCreateWritesRow(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-admin"] = store.RoleAdmin
	f.p2().orgPolicy = true
	a := New(quota.Unlimited{})
	a.Store = f

	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/sites", `{"slug":"hello"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create site: %d %s", rr.Code, rr.Body)
	}

	log := f.auditLog()
	if len(log) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(log))
	}
	e := log[0]
	if e.Action != "site.create" {
		t.Errorf("action = %q", e.Action)
	}
	if e.ActorUser == nil || *e.ActorUser != "u-admin" {
		t.Errorf("actor_user = %v, want u-admin", e.ActorUser)
	}
	if e.RequestID == "" {
		t.Error("audit row missing request_id (X-Request-Id propagation)")
	}
	if e.Metadata["slug"] != "hello" {
		t.Errorf("metadata.slug = %v", e.Metadata["slug"])
	}
}

// --- audit: only admin/owner may read /v1/audit -------------------------------

func TestAudit_NonAdminCannotRead(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-member"] = store.RoleMember
	a := New(quota.Unlimited{})
	a.Store = f

	h := mountPhase4(a, memberClaims())
	rr := getReq(h, "/v1/audit")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("member reading /v1/audit: got %d, want 403\n%s", rr.Code, rr.Body)
	}
}

func TestAudit_AdminCanRead(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-admin"] = store.RoleAdmin
	// Seed a couple of audit rows directly.
	f.p2().audit = []store.AuditEntry{
		{ID: "a1", Action: "site.create", OrgID: "org-1"},
		{ID: "a2", Action: "deploy.publish", OrgID: "org-1"},
	}
	a := New(quota.Unlimited{})
	a.Store = f

	h := mountPhase4(a, adminClaims())
	rr := getReq(h, "/v1/audit?limit=10")
	if rr.Code != http.StatusOK {
		t.Fatalf("admin reading /v1/audit: %d %s", rr.Code, rr.Body)
	}
	var body struct {
		Events []auditEntryResponse `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Events) != 2 {
		t.Fatalf("expected 2 audit rows, got %d", len(body.Events))
	}
	// Newest-first (the fake reverses): deploy.publish was appended last.
	if body.Events[0].Action != "deploy.publish" {
		t.Errorf("newest-first ordering wrong: %q", body.Events[0].Action)
	}
}

// --- revoke member writes revoked:user ----------------------------------------

func TestRevoke_MemberWritesDenylist(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-admin"] = store.RoleAdmin
	f.p2().members["victim-123"] = store.RoleMember // target is a member of the caller's org
	rev := newFakeRevoker()
	a := New(quota.Unlimited{})
	a.Store = f
	a.Revoker = rev

	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/members/victim-123/revoke", `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke member: %d %s", rr.Code, rr.Body)
	}
	minIAT, ok := rev.get(edgerevoke.KindUser, "victim-123")
	if !ok || minIAT <= 0 {
		t.Fatalf("revoked:user:victim-123 not written (ok=%v iat=%d)", ok, minIAT)
	}
	// And it recorded an audit row.
	if got := lastAuditAction(f); got != "member.revoke" {
		t.Errorf("audit action = %q, want member.revoke", got)
	}
}

func TestRevoke_MemberNonAdminForbidden(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-member"] = store.RoleMember
	rev := newFakeRevoker()
	a := New(quota.Unlimited{})
	a.Store = f
	a.Revoker = rev

	h := mountPhase4(a, memberClaims())
	rr := postJSON(h, "/v1/members/victim-123/revoke", `{}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("member revoking: got %d, want 403", rr.Code)
	}
	if _, ok := rev.get(edgerevoke.KindUser, "victim-123"); ok {
		t.Error("non-admin revoke must NOT write the denylist")
	}
}

// --- L5: a user-targeted revoke is scoped to the caller's org -------------------

// An admin of org A must NOT be able to revoke a user belonging to another org
// (a cross-tenant DoS of that user's edge-token sessions). The target is not a
// member of the caller's org, so RevokeMember refuses and writes nothing.
func TestRevoke_MemberNotInOrgRejected(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-admin"] = store.RoleAdmin // caller is an admin of org-1
	// "outsider-9" is NOT seeded as a member of org-1 (belongs to some other org).
	rev := newFakeRevoker()
	a := New(quota.Unlimited{})
	a.Store = f
	a.Revoker = rev

	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/members/outsider-9/revoke", `{}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-org revoke: got %d, want 404\n%s", rr.Code, rr.Body)
	}
	if _, ok := rev.get(edgerevoke.KindUser, "outsider-9"); ok {
		t.Error("cross-org revoke must NOT write the denylist")
	}
	if len(f.auditLog()) != 0 {
		t.Error("cross-org revoke must NOT write an audit row")
	}
}

// The generic /v1/orgs/revoke-access kind=user path is scoped the same way: a target
// that is not a member of the caller's org is refused and nothing is written.
func TestRevoke_GenericUserNotInOrgRejected(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-admin"] = store.RoleAdmin
	rev := newFakeRevoker()
	a := New(quota.Unlimited{})
	a.Store = f
	a.Revoker = rev

	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/orgs/revoke-access", `{"kind":"user","id":"outsider-9"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-org kind=user revoke: got %d, want 404\n%s", rr.Code, rr.Body)
	}
	if _, ok := rev.get(edgerevoke.KindUser, "outsider-9"); ok {
		t.Error("cross-org kind=user revoke must NOT write the denylist")
	}
}

// A same-org user-targeted revoke is unaffected by the scope check: the target is a
// member of the caller's org, so the denylist write still succeeds.
func TestRevoke_MemberInOrgSucceeds(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-admin"] = store.RoleAdmin
	f.p2().members["teammate-2"] = store.RoleMember // same org as the caller
	rev := newFakeRevoker()
	a := New(quota.Unlimited{})
	a.Store = f
	a.Revoker = rev

	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/members/teammate-2/revoke", `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("same-org revoke: got %d, want 200\n%s", rr.Code, rr.Body)
	}
	if iat, ok := rev.get(edgerevoke.KindUser, "teammate-2"); !ok || iat <= 0 {
		t.Fatalf("same-org revoke should write revoked:user (ok=%v iat=%d)", ok, iat)
	}
}

// --- revoke site-access writes revoked:site -----------------------------------

func TestRevoke_SiteAccessWritesDenylist(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-admin"] = store.RoleAdmin
	seedSite(f, "site-9", "niner")
	rev := newFakeRevoker()
	a := New(quota.Unlimited{})
	a.Store = f
	a.Revoker = rev

	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/sites/site-9/revoke-access", `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke site access: %d %s", rr.Code, rr.Body)
	}
	if iat, ok := rev.get(edgerevoke.KindSite, "site-9"); !ok || iat <= 0 {
		t.Fatalf("revoked:site:site-9 not written (ok=%v iat=%d)", ok, iat)
	}
}

// --- tightening a site's access ALSO writes revoked:site ----------------------

func TestRevoke_SetAccessAlsoWritesSiteDenylist(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-admin"] = store.RoleAdmin
	f.p2().orgPolicy = true
	seedSite(f, "site-3", "three")
	rev := newFakeRevoker()
	a := New(quota.Unlimited{})
	a.Store = f
	a.Revoker = rev

	h := mountPhase4(a, adminClaims())
	rr := putJSON(h, "/v1/sites/site-3/access", `{"mode":"org_only"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("set access: %d %s", rr.Code, rr.Body)
	}
	if _, ok := rev.get(edgerevoke.KindSite, "site-3"); !ok {
		t.Fatal("access tighten should ALSO write revoked:site")
	}
}

// --- disabling allow_external_sharing writes revoked:org ----------------------

func TestRevoke_DisableExternalSharingWritesOrgDenylist(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-admin"] = store.RoleAdmin
	f.p2().orgPolicy = true
	rev := newFakeRevoker()
	a := New(quota.Unlimited{})
	a.Store = f
	a.Revoker = rev

	h := mountPhase4(a, adminClaims())
	rr := putJSON(h, "/v1/orgs/allow-external-sharing", `{"enabled":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("disable external sharing: %d %s", rr.Code, rr.Body)
	}
	if _, ok := rev.get(edgerevoke.KindOrg, "org-1"); !ok {
		t.Fatal("disabling external sharing should write revoked:org")
	}

	// Re-ENABLING must NOT write a denylist (loosening access never revokes).
	rev2 := newFakeRevoker()
	a.Revoker = rev2
	rr = putJSON(mountPhase4(a, adminClaims()), "/v1/orgs/allow-external-sharing", `{"enabled":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("enable external sharing: %d %s", rr.Code, rr.Body)
	}
	if _, ok := rev2.get(edgerevoke.KindOrg, "org-1"); ok {
		t.Error("ENABLING external sharing must NOT write a denylist")
	}
}

// --- generic /v1/orgs/revoke-access dispatches by kind --------------------------

func TestRevoke_GenericByKind(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		seed     func(f *fakeStore)
		wantCode int
		check    func(t *testing.T, rev *fakeRevoker)
	}{
		{
			name:     "user",
			body:     `{"kind":"user","id":"victim-1"}`,
			seed:     func(f *fakeStore) { f.p2().members["victim-1"] = store.RoleMember },
			wantCode: http.StatusOK,
			check: func(t *testing.T, rev *fakeRevoker) {
				if _, ok := rev.get(edgerevoke.KindUser, "victim-1"); !ok {
					t.Error("kind=user should write revoked:user")
				}
			},
		},
		{
			name:     "org ignores client id and uses active org",
			body:     `{"kind":"org","id":"some-other-org"}`,
			wantCode: http.StatusOK,
			check: func(t *testing.T, rev *fakeRevoker) {
				if _, ok := rev.get(edgerevoke.KindOrg, "org-1"); !ok {
					t.Error("kind=org must revoke the caller's OWN org, not the client id")
				}
				if _, ok := rev.get(edgerevoke.KindOrg, "some-other-org"); ok {
					t.Error("kind=org must NOT honor a client-supplied org id (no cross-org kill)")
				}
			},
		},
		{
			name:     "site",
			body:     `{"kind":"site","id":"site-7"}`,
			seed:     func(f *fakeStore) { seedSite(f, "site-7", "seven") },
			wantCode: http.StatusOK,
			check: func(t *testing.T, rev *fakeRevoker) {
				if _, ok := rev.get(edgerevoke.KindSite, "site-7"); !ok {
					t.Error("kind=site should write revoked:site")
				}
			},
		},
		{
			name:     "site not owned → 404",
			body:     `{"kind":"site","id":"ghost"}`,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "bad kind → 400",
			body:     `{"kind":"banana","id":"x"}`,
			wantCode: http.StatusBadRequest,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeStore()
			f.p2().members["u-admin"] = store.RoleAdmin
			if c.seed != nil {
				c.seed(f)
			}
			rev := newFakeRevoker()
			a := New(quota.Unlimited{})
			a.Store = f
			a.Revoker = rev

			h := mountPhase4(a, adminClaims())
			rr := postJSON(h, "/v1/orgs/revoke-access", c.body)
			if rr.Code != c.wantCode {
				t.Fatalf("got %d, want %d\n%s", rr.Code, c.wantCode, rr.Body)
			}
			if c.check != nil {
				c.check(t, rev)
			}
		})
	}
}

// --- membership audit: invites + joins -----------------------------------------

func TestRecordMemberInvite_WritesAuditRow(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-admin"] = store.RoleAdmin
	a := New(quota.Unlimited{})
	a.Store = f

	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/members/invites", `{"email":"new@team.com","role":"admin"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("record invite: %d %s", rr.Code, rr.Body)
	}
	log := f.auditLog()
	if len(log) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(log))
	}
	e := log[0]
	if e.Action != "member.invite" {
		t.Errorf("action = %q, want member.invite", e.Action)
	}
	if e.Target != "invite:new@team.com" {
		t.Errorf("target = %q", e.Target)
	}
	if e.ActorUser == nil || *e.ActorUser != "u-admin" {
		t.Errorf("actor_user = %v, want u-admin", e.ActorUser)
	}
	if e.Metadata["email"] != "new@team.com" || e.Metadata["role"] != "admin" {
		t.Errorf("metadata = %v", e.Metadata)
	}
}

func TestRecordMemberInvite_NonAdminForbidden(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-member"] = store.RoleMember
	a := New(quota.Unlimited{})
	a.Store = f

	h := mountPhase4(a, memberClaims())
	rr := postJSON(h, "/v1/members/invites", `{"email":"new@team.com","role":"member"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("member recording invite: got %d, want 403\n%s", rr.Code, rr.Body)
	}
	if len(f.auditLog()) != 0 {
		t.Errorf("non-admin must not write an audit row")
	}
}

func TestRecordMemberInvite_BadEmail400(t *testing.T) {
	f := newFakeStore()
	f.p2().members["u-admin"] = store.RoleAdmin
	a := New(quota.Unlimited{})
	a.Store = f

	h := mountPhase4(a, adminClaims())
	rr := postJSON(h, "/v1/members/invites", `{"email":"not-an-email"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad email: got %d, want 400\n%s", rr.Code, rr.Body)
	}
}

func TestRecordMemberJoin_WritesAuditRow(t *testing.T) {
	f := newFakeStore()
	// The caller is a member of the (now-active) joined org.
	f.p2().members["u-member"] = store.RoleMember
	a := New(quota.Unlimited{})
	a.Store = f

	h := mountPhase4(a, memberClaims())
	rr := postJSON(h, "/v1/members/joined", `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("record join: %d %s", rr.Code, rr.Body)
	}
	log := f.auditLog()
	if len(log) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(log))
	}
	e := log[0]
	if e.Action != "member.join" {
		t.Errorf("action = %q, want member.join", e.Action)
	}
	// Target is the caller's own id (from claims), never the request body.
	if e.Target != "member:u-member" {
		t.Errorf("target = %q, want member:u-member", e.Target)
	}
	if e.Metadata["role"] != "member" {
		t.Errorf("metadata.role = %v, want member", e.Metadata["role"])
	}
}

func TestRecordMemberJoin_NonMemberRejected(t *testing.T) {
	f := newFakeStore() // no member rows → MemberRole returns ErrNoMembership
	a := New(quota.Unlimited{})
	a.Store = f

	h := mountPhase4(a, memberClaims())
	rr := postJSON(h, "/v1/members/joined", `{}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("non-member join: got %d, want 400\n%s", rr.Code, rr.Body)
	}
	if len(f.auditLog()) != 0 {
		t.Errorf("non-member must not write an audit row")
	}
}

// lastAuditAction returns the action of the most recent captured audit row.
func lastAuditAction(f *fakeStore) string {
	log := f.auditLog()
	if len(log) == 0 {
		return ""
	}
	return log[len(log)-1].Action
}
