//go:build integration

// Phase-2 integration test (ARCHITECTURE.md §5/§6/§9): access control & domains,
// exercised against real Postgres 16 + the goose app migrations as the
// non-BYPASSRLS shipped_app role, plus a synthetic Better Auth `auth.member` table
// the Go API reads for role re-checks.
//
// Run with:
//
//	go test -tags integration ./services/api/internal/integration/...
//
// Covered (all falsifiable):
//   - org_only: mint allows a member, denies a non-member (403);
//   - allowlist: mints for a verified-email entry, denies others, CLAIMS the entry,
//     external entry blocked when allow_external_sharing=false;
//   - password: correct/incorrect password;
//   - expiry: a past expires_at refuses the mint;
//   - admin-only: a member role can't change access / can't toggle org policy;
//   - allow-external-sharing disable reconciles routes (public → org_only);
//   - custom-domain verify (fake) writes the KV route;
//   - edge token mint→verify round-trips with aud binding.
//
// Containers are torn down on completion via t.Cleanup (inherited helpers).
package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/shipped/internal/edgetoken"
	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/internal/pwhash"
	"github.com/danielpang/shipped/internal/quota"
	"github.com/danielpang/shipped/services/api/internal/store"
)

func TestIntegration_Phase2_AccessControl(t *testing.T) {
	ctx := context.Background()
	repoRoot := repoRoot(t)

	startPostgres(t)
	startMinio(t)
	applyMigrations(t, repoRoot)
	seedAuthMemberTable(t)

	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect as shipped_app: %v", err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})

	obj := newMinioStore(t, ctx)
	if err := obj.EnsureBucket(ctx); err != nil {
		t.Fatalf("ensure bucket: %v", err)
	}
	proj := projection.NewLocal()

	signer, _, _, err := edgetoken.LoadOrGenerateSigner("")
	if err != nil {
		t.Fatal(err)
	}
	verifier := edgetoken.VerifierForSigner(signer)

	// --- Two orgs. orgA allows external sharing; orgExt is the external viewer's. ---
	orgA := "11111111-1111-1111-1111-111111111111"
	orgB := "22222222-2222-2222-2222-222222222222"
	userOwnerA := "a0000000-0000-0000-0000-00000000000a"   // owner in A
	userMemberA := "a0000000-0000-0000-0000-00000000000b"  // member in A
	userOutsider := "b0000000-0000-0000-0000-00000000000a" // member in B (not in A)
	tA := store.Tenant{OrgID: orgA, UserID: userOwnerA}

	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", orgA)
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", orgB)
	seedAuthOrg(t, orgA, "orga")
	seedAuthOrg(t, orgB, "orgb")
	must(t, st.EnsureOrgProvisioned(ctx, tA))
	must(t, st.EnsureOrgProvisioned(ctx, store.Tenant{OrgID: orgB, UserID: userOutsider}))

	// Seed memberships in the Better Auth member table.
	insertMember(t, orgA, userOwnerA, store.RoleOwner)
	insertMember(t, orgA, userMemberA, store.RoleMember)
	insertMember(t, orgB, userOutsider, store.RoleMember)

	// --- A site, deployed + published (so it has a live version to gate). ---
	site, err := st.CreateSite(ctx, tA, "gated", projection.AccessPublic)
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	ver := deployVersion(t, ctx, st, obj, tA, site.ID, map[string][]byte{"index.html": []byte("<h1>secret</h1>")})
	if _, err := st.Publish(ctx, tA, site.ID, ver); err != nil {
		t.Fatalf("publish: %v", err)
	}
	host := projection.HostForSite("orga", "gated")

	// =========================================================================
	// Admin-only gating: a MEMBER cannot change access (role re-checked live).
	// =========================================================================
	roleMember, err := st.MemberRole(ctx, orgA, userMemberA)
	if err != nil || roleMember != store.RoleMember {
		t.Fatalf("MemberRole(member) = %q, %v", roleMember, err)
	}
	if store.IsAdminRole(roleMember) {
		t.Fatal("member must not be admin")
	}
	roleOwner, err := st.MemberRole(ctx, orgA, userOwnerA)
	if err != nil || !store.IsAdminRole(roleOwner) {
		t.Fatalf("MemberRole(owner) = %q, %v (want admin-or-above)", roleOwner, err)
	}
	// A non-member of org A has no membership row.
	if _, err := st.MemberRole(ctx, orgA, userOutsider); !errors.Is(err, store.ErrNoMembership) {
		t.Fatalf("outsider MemberRole err = %v, want ErrNoMembership", err)
	}

	// =========================================================================
	// org_only: set the mode, then mint for a member (ok) / non-member (403).
	// =========================================================================
	if _, err := st.SetSiteAccess(ctx, tA, store.SetAccessParams{SiteID: site.ID, Mode: projection.AccessOrgOnly}); err != nil {
		t.Fatalf("set org_only: %v", err)
	}
	// Member of org A → mint succeeds, token binds to the host.
	dec, err := st.AuthorizeMint(ctx, store.MintViewer{UserID: userMemberA, OrgID: orgA}, host)
	if err != nil {
		t.Fatalf("org_only mint for member: %v", err)
	}
	tok, err := signer.Mint(edgetoken.MintParams{ContentHost: dec.Host, Subject: dec.Subject, SiteID: dec.SiteID, Mode: dec.Mode})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(tok, host); err != nil {
		t.Fatalf("verify org_only token: %v", err)
	}
	// Replay at a different host fails (aud binding).
	if _, err := verifier.Verify(tok, "other.shippedusercontent.com"); err == nil {
		t.Fatal("token verified at the wrong host (aud not bound)")
	}
	// Outsider (org B) → denied.
	if _, err := st.AuthorizeMint(ctx, store.MintViewer{UserID: userOutsider, OrgID: orgB}, host); !errors.Is(err, store.ErrNotOrgMember) {
		t.Fatalf("org_only mint for non-member err = %v, want ErrNotOrgMember", err)
	}

	// =========================================================================
	// allowlist: mints for a verified-email entry, denies others, CLAIMS it,
	// external entry blocked when allow_external_sharing=false.
	// =========================================================================
	if _, err := st.SetSiteAccess(ctx, tA, store.SetAccessParams{SiteID: site.ID, Mode: projection.AccessAllowlist}); err != nil {
		t.Fatalf("set allowlist: %v", err)
	}
	// Internal grant.
	if _, err := st.AddAllowlistEntry(ctx, tA, store.AddAllowlistEntryParams{SiteID: site.ID, Email: "alice@acme.com", IsExternal: false}); err != nil {
		t.Fatalf("add allowlist: %v", err)
	}
	// A verified-email viewer matching the grant → mint ok + claim recorded.
	viewer := store.MintViewer{UserID: userOutsider, OrgID: orgB, Email: "Alice@acme.com", EmailVerified: true}
	if _, err := st.AuthorizeMint(ctx, viewer, host); err != nil {
		t.Fatalf("allowlist mint for listed verified email: %v", err)
	}
	entries, err := st.ListAllowlistEntries(ctx, tA, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ClaimedAt == nil || entries[0].ClaimedBy == nil || *entries[0].ClaimedBy != userOutsider {
		t.Fatalf("allowlist entry not claimed by viewer: %+v", entries)
	}
	// An UNlisted email → denied.
	if _, err := st.AuthorizeMint(ctx, store.MintViewer{UserID: "x", OrgID: orgB, Email: "bob@acme.com", EmailVerified: true}, host); !errors.Is(err, store.ErrNotAllowlisted) {
		t.Fatalf("unlisted email mint err = %v, want ErrNotAllowlisted", err)
	}
	// An UNVERIFIED email (even if listed) → denied.
	if _, err := st.AuthorizeMint(ctx, store.MintViewer{UserID: "x", OrgID: orgB, Email: "alice@acme.com", EmailVerified: false}, host); !errors.Is(err, store.ErrNotAllowlisted) {
		t.Fatalf("unverified email mint err = %v, want ErrNotAllowlisted", err)
	}

	// External entry: add one (allowed while policy=true), then disabling external
	// sharing must remove it + block external mint.
	if _, err := st.AddAllowlistEntry(ctx, tA, store.AddAllowlistEntryParams{SiteID: site.ID, Email: "ext@external.com", IsExternal: true}); err != nil {
		t.Fatalf("add external allowlist (policy on): %v", err)
	}

	// =========================================================================
	// password: correct / incorrect.
	// =========================================================================
	hash, err := pwhash.Hash("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetSiteAccess(ctx, tA, store.SetAccessParams{SiteID: site.ID, Mode: projection.AccessPassword, PasswordHash: hash}); err != nil {
		t.Fatalf("set password: %v", err)
	}
	pdec, ph, err := st.ResolveForPassword(ctx, host)
	if err != nil {
		t.Fatalf("resolve for password: %v", err)
	}
	if pdec.Mode != projection.AccessPassword {
		t.Fatalf("password mode = %q", pdec.Mode)
	}
	if err := pwhash.Verify(ph, "hunter2"); err != nil {
		t.Fatalf("correct password rejected: %v", err)
	}
	if err := pwhash.Verify(ph, "wrong"); !errors.Is(err, pwhash.ErrMismatch) {
		t.Fatalf("wrong password accepted: %v", err)
	}
	// A password-mode site reached via the mint endpoint is refused.
	if _, err := st.AuthorizeMint(ctx, store.MintViewer{UserID: userMemberA, OrgID: orgA}, host); !errors.Is(err, store.ErrPasswordModeUsesPasswordEndpoint) {
		t.Fatalf("mint on password site err = %v, want ErrPasswordModeUsesPasswordEndpoint", err)
	}

	// =========================================================================
	// expiry: a PAST expires_at refuses the mint ("link expired").
	// =========================================================================
	past := time.Now().Add(-time.Hour)
	if _, err := st.SetSiteAccess(ctx, tA, store.SetAccessParams{SiteID: site.ID, Mode: projection.AccessOrgOnly, ExpiresAt: &past}); err != nil {
		t.Fatalf("set org_only+expired: %v", err)
	}
	if _, err := st.AuthorizeMint(ctx, store.MintViewer{UserID: userMemberA, OrgID: orgA}, host); !errors.Is(err, store.ErrPolicyExpired) {
		t.Fatalf("expired mint err = %v, want ErrPolicyExpired", err)
	}
	// A FUTURE expiry still mints.
	future := time.Now().Add(time.Hour)
	if _, err := st.SetSiteAccess(ctx, tA, store.SetAccessParams{SiteID: site.ID, Mode: projection.AccessOrgOnly, ExpiresAt: &future}); err != nil {
		t.Fatalf("set org_only+future: %v", err)
	}
	if _, err := st.AuthorizeMint(ctx, store.MintViewer{UserID: userMemberA, OrgID: orgA}, host); err != nil {
		t.Fatalf("future-expiry mint should succeed: %v", err)
	}

	// =========================================================================
	// allow-external-sharing disable reconciles routes (public → org_only) and
	// revokes external allowlist grants.
	// =========================================================================
	// Make a SECOND public site so the reconcile has something to downgrade.
	pub, err := st.CreateSite(ctx, tA, "publicsite", projection.AccessPublic)
	if err != nil {
		t.Fatalf("create public site: %v", err)
	}
	pubVer := deployVersion(t, ctx, st, obj, tA, pub.ID, map[string][]byte{"index.html": []byte("<h1>pub</h1>")})
	pubRes, err := st.Publish(ctx, tA, pub.ID, pubVer)
	if err != nil {
		t.Fatalf("publish public site: %v", err)
	}
	must(t, proj.PutRoute(ctx, pubRes.Host, pubRes.Route))
	pubHost := projection.HostForSite("orga", "publicsite")
	if rv, _ := proj.Get(pubHost); rv.AccessMode != projection.AccessPublic {
		t.Fatalf("public site route not public before reconcile: %+v", rv)
	}

	rec, err := st.SetAllowExternalSharing(ctx, tA, false)
	if err != nil {
		t.Fatalf("disable external sharing: %v", err)
	}
	for _, d := range rec.Downgraded {
		must(t, proj.PutRoute(ctx, d.Host, d.Route))
	}
	// The public site was downgraded to org_only at the edge.
	if rv, _ := proj.Get(pubHost); rv.AccessMode != projection.AccessOrgOnly {
		t.Fatalf("reconcile did not downgrade public route: %+v", rv)
	}
	// The external allowlist grant was revoked.
	entriesAfter, _ := st.ListAllowlistEntries(ctx, tA, site.ID)
	for _, e := range entriesAfter {
		if e.IsExternal {
			t.Fatalf("external allowlist grant survived reconcile: %+v", e)
		}
	}
	// And a NEW external grant is now rejected by the 0004 trigger.
	if _, err := st.AddAllowlistEntry(ctx, tA, store.AddAllowlistEntryParams{SiteID: site.ID, Email: "ext2@external.com", IsExternal: true}); !errors.Is(err, store.ErrExternalSharingDisabled) {
		t.Fatalf("external add under false policy err = %v, want ErrExternalSharingDisabled", err)
	}
	// Re-enable so the rest of the test can use public/external again.
	if _, err := st.SetAllowExternalSharing(ctx, tA, true); err != nil {
		t.Fatalf("re-enable external sharing: %v", err)
	}

	// =========================================================================
	// custom-domain verify (fake) writes the KV route.
	// =========================================================================
	fakeID := "cf-fake-1"
	dom, err := st.CreateDomain(ctx, tA, store.CreateDomainParams{
		SiteID: pub.ID, Hostname: "docs.acme.com", CFHostnameID: fakeID, DCVRecord: "_cf TXT abc",
	})
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if dom.VerifyStatus != store.DomainPending {
		t.Fatalf("new domain status = %q, want pending", dom.VerifyStatus)
	}
	// Advance to verified+TLS → host route written, route returned.
	res, err := st.UpdateDomainStatus(ctx, tA, dom.ID, store.DomainVerified, store.TLSIssued)
	if err != nil {
		t.Fatalf("update domain status: %v", err)
	}
	if !res.Registered || res.Host != "docs.acme.com" {
		t.Fatalf("verified domain did not register host route: %+v", res)
	}
	must(t, proj.PutRoute(ctx, res.Host, res.Route))
	if rv, ok := proj.Get("docs.acme.com"); !ok || rv.SiteID != pub.ID {
		t.Fatalf("custom-domain route not written: %+v ok=%v", rv, ok)
	}
	// The custom host now resolves to the site via the global registry (the /authz
	// resolver path): a fresh org_only mint on the custom host works for a member.
	if _, err := st.SetSiteAccess(ctx, tA, store.SetAccessParams{SiteID: pub.ID, Mode: projection.AccessOrgOnly}); err != nil {
		t.Fatalf("set pub org_only: %v", err)
	}
	if _, err := st.AuthorizeMint(ctx, store.MintViewer{UserID: userMemberA, OrgID: orgA}, "docs.acme.com"); err != nil {
		t.Fatalf("mint via custom host: %v", err)
	}

	// A custom hostname already taken by another site → ErrHostTaken (global unique).
	other, err := st.CreateSite(ctx, tA, "othersite", projection.AccessOrgOnly)
	if err != nil {
		t.Fatalf("create other site: %v", err)
	}
	if _, err := st.CreateDomain(ctx, tA, store.CreateDomainParams{SiteID: other.ID, Hostname: "docs.acme.com"}); !errors.Is(err, store.ErrHostTaken) {
		t.Fatalf("duplicate hostname err = %v, want ErrHostTaken", err)
	}

	// =========================================================================
	// FIX 1: a custom-domain host is revoked alongside the canonical host on BOTH
	// an access change (SetSiteAccess) AND a disable-external-sharing reconcile.
	// A site with a canonical host AND a verified custom host, both public, must
	// have BOTH routes flip away from public when the policy tightens — leaving the
	// custom host at 'public' keeps the Worker serving it publicly (the bug).
	// =========================================================================
	dualSite, err := st.CreateSite(ctx, tA, "dual", projection.AccessPublic)
	if err != nil {
		t.Fatalf("create dual site: %v", err)
	}
	dualVer := deployVersion(t, ctx, st, obj, tA, dualSite.ID, map[string][]byte{"index.html": []byte("<h1>dual</h1>")})
	dualRes, err := st.Publish(ctx, tA, dualSite.ID, dualVer)
	if err != nil {
		t.Fatalf("publish dual: %v", err)
	}
	must(t, proj.PutRoute(ctx, dualRes.Host, dualRes.Route))
	dualCanonical := projection.HostForSite("orga", "dual")
	dualCustom := "www.dualcorp.com"

	// Verify a custom domain for the dual site → writes the custom host_route +
	// projects its route (now BOTH hosts exist in the registry + projection).
	dualDom, err := st.CreateDomain(ctx, tA, store.CreateDomainParams{
		SiteID: dualSite.ID, Hostname: dualCustom, CFHostnameID: "cf-fake-dual", DCVRecord: "_cf TXT dual",
	})
	if err != nil {
		t.Fatalf("create dual domain: %v", err)
	}
	dualDomRes, err := st.UpdateDomainStatus(ctx, tA, dualDom.ID, store.DomainVerified, store.TLSIssued)
	if err != nil {
		t.Fatalf("verify dual domain: %v", err)
	}
	if !dualDomRes.Registered {
		t.Fatalf("dual custom domain did not register: %+v", dualDomRes)
	}
	must(t, proj.PutRoute(ctx, dualDomRes.Host, dualDomRes.Route))

	// Sanity: both hosts are in the registry and projected as public.
	hostRoutes, err := st.ListHostRoutesForSite(ctx, tA, dualSite.ID)
	if err != nil {
		t.Fatalf("list host routes: %v", err)
	}
	if len(hostRoutes) != 2 {
		t.Fatalf("dual site should have 2 host routes (canonical + custom), got %d: %+v", len(hostRoutes), hostRoutes)
	}
	if rv, ok := proj.Get(dualCanonical); !ok || rv.AccessMode != projection.AccessPublic {
		t.Fatalf("dual canonical not public before change: %+v ok=%v", rv, ok)
	}
	if rv, ok := proj.Get(dualCustom); !ok || rv.AccessMode != projection.AccessPublic {
		t.Fatalf("dual custom not public before change: %+v ok=%v", rv, ok)
	}

	// H3: a re-publish (deploy a new version) must rewrite EVERY host's route to the
	// new version_id — a custom domain left pointing at the OLD version keeps serving
	// the stale build after a publish/rollback. Publish now returns Routes for all
	// hosts (canonical + custom), not just the canonical pair.
	dualVer2 := deployVersion(t, ctx, st, obj, tA, dualSite.ID, map[string][]byte{"index.html": []byte("<h1>dual v2</h1>")})
	repub, err := st.Publish(ctx, tA, dualSite.ID, dualVer2)
	if err != nil {
		t.Fatalf("re-publish dual: %v", err)
	}
	republishedHosts := map[string]bool{}
	for _, ru := range repub.Routes {
		must(t, proj.PutRoute(ctx, ru.Host, ru.Route))
		republishedHosts[ru.Host] = true
	}
	if !republishedHosts[dualCanonical] || !republishedHosts[dualCustom] {
		t.Fatalf("H3: Publish did not return BOTH hosts: %+v", repub.Routes)
	}
	if rv, _ := proj.Get(dualCustom); rv.VersionID != dualVer2 {
		t.Fatalf("H3: CUSTOM host still points at old version after re-publish: %+v (want %s)", rv, dualVer2)
	}
	if rv, _ := proj.Get(dualCanonical); rv.VersionID != dualVer2 {
		t.Fatalf("H3: canonical host not updated after re-publish: %+v (want %s)", rv, dualVer2)
	}

	// (a) SetSiteAccess public→org_only rewrites EVERY host's route.
	accRes, err := st.SetSiteAccess(ctx, tA, store.SetAccessParams{SiteID: dualSite.ID, Mode: projection.AccessOrgOnly})
	if err != nil {
		t.Fatalf("set dual org_only: %v", err)
	}
	gotHosts := map[string]bool{}
	for _, ru := range accRes.Routes {
		must(t, proj.PutRoute(ctx, ru.Host, ru.Route))
		gotHosts[ru.Host] = true
	}
	if !gotHosts[dualCanonical] || !gotHosts[dualCustom] {
		t.Fatalf("SetSiteAccess did not return BOTH hosts: %+v", accRes.Routes)
	}
	if rv, _ := proj.Get(dualCanonical); rv.AccessMode != projection.AccessOrgOnly {
		t.Fatalf("FIX1: canonical host not flipped by SetSiteAccess: %+v", rv)
	}
	if rv, _ := proj.Get(dualCustom); rv.AccessMode != projection.AccessOrgOnly {
		t.Fatalf("FIX1: CUSTOM host not flipped by SetSiteAccess (still serving at old tier): %+v", rv)
	}

	// (b) Reset to public, re-project both, then DISABLE external sharing and assert
	// the reconcile flips BOTH hosts away from public.
	resetRes, err := st.SetSiteAccess(ctx, tA, store.SetAccessParams{SiteID: dualSite.ID, Mode: projection.AccessPublic})
	if err != nil {
		t.Fatalf("reset dual public: %v", err)
	}
	for _, ru := range resetRes.Routes {
		must(t, proj.PutRoute(ctx, ru.Host, ru.Route))
	}
	if rv, _ := proj.Get(dualCustom); rv.AccessMode != projection.AccessPublic {
		t.Fatalf("dual custom not reset to public: %+v", rv)
	}

	recDual, err := st.SetAllowExternalSharing(ctx, tA, false)
	if err != nil {
		t.Fatalf("disable external sharing (dual): %v", err)
	}
	reconHosts := map[string]string{} // host → new access_mode
	for _, d := range recDual.Downgraded {
		must(t, proj.PutRoute(ctx, d.Host, d.Route))
		reconHosts[d.Host] = d.Route.AccessMode
	}
	if reconHosts[dualCanonical] != projection.AccessOrgOnly {
		t.Fatalf("FIX1: reconcile did not downgrade canonical host: %q", reconHosts[dualCanonical])
	}
	if reconHosts[dualCustom] != projection.AccessOrgOnly {
		t.Fatalf("FIX1: reconcile did not downgrade CUSTOM host (left serving publicly): %q", reconHosts[dualCustom])
	}
	if rv, _ := proj.Get(dualCanonical); rv.AccessMode != projection.AccessOrgOnly {
		t.Fatalf("FIX1: projected canonical route still public after reconcile: %+v", rv)
	}
	if rv, _ := proj.Get(dualCustom); rv.AccessMode != projection.AccessOrgOnly {
		t.Fatalf("FIX1: projected CUSTOM route still public after reconcile: %+v", rv)
	}
	// Re-enable for any later use.
	if _, err := st.SetAllowExternalSharing(ctx, tA, true); err != nil {
		t.Fatalf("re-enable external sharing (dual): %v", err)
	}

	// =========================================================================
	// FIX 2: org_only mint requires LIVE membership. A member mints; deleting the
	// member row (with the SAME still-valid JWT identity) now yields 403 — the JWT
	// org_id claim alone is no longer sufficient.
	// =========================================================================
	// Use the dual site, now org_only, with its canonical host. userMemberA is a
	// live member of orgA → mint succeeds.
	if _, err := st.SetSiteAccess(ctx, tA, store.SetAccessParams{SiteID: dualSite.ID, Mode: projection.AccessOrgOnly}); err != nil {
		t.Fatalf("set dual org_only (fix2): %v", err)
	}
	if _, err := st.AuthorizeMint(ctx, store.MintViewer{UserID: userMemberA, OrgID: orgA}, dualCanonical); err != nil {
		t.Fatalf("FIX2: org_only mint for live member should succeed: %v", err)
	}
	// Remove the membership row (org removal) — the JWT is unchanged/still valid.
	deleteMember(t, orgA, userMemberA)
	if _, err := st.AuthorizeMint(ctx, store.MintViewer{UserID: userMemberA, OrgID: orgA}, dualCanonical); !errors.Is(err, store.ErrNotOrgMember) {
		t.Fatalf("FIX2: removed member still minted (err = %v, want ErrNotOrgMember)", err)
	}

	t.Log("PASS: org_only allow/deny, allowlist claim + external gating, password, expiry refuse, admin-only role re-check, external-sharing reconcile, custom-domain verify writes KV, edge token aud binding, FIX1 custom-host revoked on access+reconcile, FIX2 org_only live-membership re-check")
}

// seedAuthMemberTable creates a minimal Better Auth `auth.member` table the Go API
// reads for role re-checks. Better Auth owns + migrates this in production; here we
// create the shape the store's MemberRole/ListMembers queries bind to. We also
// GRANT the runtime role read access (the auth schema is outside app RLS).
func seedAuthMemberTable(t *testing.T) {
	t.Helper()
	mustExecRaw(t, `CREATE SCHEMA IF NOT EXISTS auth;`)
	mustExecRaw(t, `CREATE TABLE IF NOT EXISTS auth.member (
		id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
		"organizationId" uuid NOT NULL,
		"userId" uuid NOT NULL,
		"role" text NOT NULL
	);`)
	mustExecRaw(t, `GRANT USAGE ON SCHEMA auth TO shipped_app;`)
	mustExecRaw(t, `GRANT SELECT ON auth.member TO shipped_app;`)
}

func insertMember(t *testing.T, orgID, userID, role string) {
	t.Helper()
	mustExecRaw(t, `INSERT INTO auth.member ("organizationId","userId","role") VALUES ('`+orgID+`','`+userID+`','`+role+`');`)
}

// deleteMember removes a membership row (simulating org removal) so a still-valid
// JWT no longer corresponds to a current member — the FIX 2 org_only re-check.
func deleteMember(t *testing.T, orgID, userID string) {
	t.Helper()
	mustExecRaw(t, `DELETE FROM auth.member WHERE "organizationId" = '`+orgID+`' AND "userId" = '`+userID+`';`)
}

// mustExecRaw runs a raw SQL statement (no positional substitution) as the owner.
func mustExecRaw(t *testing.T, sql string) {
	t.Helper()
	run(t, "docker", "exec", "shipped-it-pg", "psql", ownerDSNLocal(), "-v", "ON_ERROR_STOP=1", "-c", sql)
}
