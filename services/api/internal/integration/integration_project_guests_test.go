//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/handlers"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/router"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type guestVerifier struct{ org, user, class string }

func (v guestVerifier) Verify(context.Context, string) (*auth.Claims, error) {
	return &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: v.user}, OrgID: v.org}, nil
}
func (v guestVerifier) CredentialClass(*auth.Claims) string { return v.class }
func TestIntegration_ProjectGuestSharing(t *testing.T) {
	ctx := store.WithHumanSession(context.Background(), true)
	startPostgres(t)
	applyMigrations(t, repoRoot(t))
	for _, direction := range []string{"down", "up"} {
		cmd := exec.Command("go", "run", "github.com/pressly/goose/v3/cmd/goose@v3.22.0", "-dir", repoRoot(t)+"/db/migrations/app", "postgres", ownerDSN, direction)
		cmd.Dir = repoRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("guest migration %s: %v %s", direction, err, out)
		}
	}

	pool, err := pgxpool.New(ctx, appDSN)
	must(t, err)
	defer pool.Close()
	must(t, store.CheckSchema(ctx, pool))
	mustExecRaw(t, `REVOKE EXECUTE ON FUNCTION app.discover_guest_projects(uuid,text,uuid) FROM dropway_app`)
	if err := store.CheckSchema(ctx, pool); err == nil {
		t.Fatal("preflight ignored missing guest discovery permission")
	}
	mustExecRaw(t, `GRANT EXECUTE ON FUNCTION app.discover_guest_projects(uuid,text,uuid) TO dropway_app`)
	must(t, store.CheckSchema(ctx, pool))

	st := store.New(pool, quota.Unlimited{})
	org, otherOrg := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	owner, guest, viewer, wrong := "a0000000-0000-0000-0000-000000000001", "a0000000-0000-0000-0000-000000000002", "a0000000-0000-0000-0000-000000000003", "a0000000-0000-0000-0000-000000000004"
	tn := store.Tenant{OrgID: org, UserID: owner}
	var enabled atomic.Bool
	enabled.Store(true)
	gate := func(_ context.Context, _ string, id string) bool { return enabled.Load() && id == org }
	st.SetCollaborationGate(gate)
	for _, id := range []string{org, otherOrg} {
		mustExec(t, `INSERT INTO app.org_meta(id,allow_external_sharing) VALUES($1,true)`, id)
	}
	seedAuthOrg(t, org, "orga")
	seedAuthOrg(t, otherOrg, "orgb")
	seedAuthMemberTable(t)
	insertMember(t, org, owner, "owner")
	insertMember(t, org, wrong, "admin")
	mustExecRaw(t, `CREATE TABLE identity."user"(id text PRIMARY KEY,email text NOT NULL UNIQUE,"emailVerified" boolean NOT NULL,banned boolean NOT NULL DEFAULT false); GRANT SELECT,UPDATE ON identity."user" TO dropway_app;`)
	for _, u := range [][2]string{{owner, "owner@example.com"}, {guest, "guest@example.com"}, {viewer, "viewer@example.com"}, {wrong, "wrong@example.com"}} {
		mustExec(t, `INSERT INTO identity."user"(id,email,"emailVerified") VALUES($1,$2,true)`, u[0], u[1])
	}
	must(t, st.EnsureOrgProvisioned(ctx, tn))
	must(t, st.SetProjectRoomsEnabled(ctx, tn, true, store.AuditRecord{}))
	room, err := st.CreateProjectRoom(ctx, tn, "Shared project", store.AuditRecord{})
	must(t, err)
	private, err := st.CreateProjectRoom(ctx, tn, "Private project", store.AuditRecord{})
	must(t, err)
	site, err := st.CreateSite(ctx, tn, "shared-site", projection.AccessOrgOnly)
	must(t, err)
	secretSite, err := st.CreateSite(ctx, tn, "other-site", projection.AccessOrgOnly)
	must(t, err)
	obj := storage.NewFake()
	html, script := []byte("<main>Project fragment</main>"), []byte("const secret='private';")
	h := sha256.Sum256(html)
	sha := hex.EncodeToString(h[:])
	h = sha256.Sum256(script)
	scriptSHA := hex.EncodeToString(h[:])
	files := []manifest.File{{Path: "index.html", SHA256: sha}, {Path: "script.js", SHA256: scriptSHA}}
	hash := manifest.Digest(files)
	ver, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{SiteID: site.ID, ContentHash: hash, SizeBytes: int64(len(html) + len(script)), Status: "ready"})
	must(t, err)
	old, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{SiteID: site.ID, ContentHash: strings.Repeat("ab", 32), Status: "ready"})
	must(t, err)
	_, err = st.Publish(ctx, tn, site.ID, ver.ID)
	must(t, err)
	must(t, obj.PutBlob(ctx, org, sha, bytes.NewReader(html), int64(len(html)), "text/html"))
	must(t, obj.PutBlob(ctx, org, scriptSHA, bytes.NewReader(script), int64(len(script)), "application/javascript"))
	raw, _ := json.Marshal(manifest.Stored{SchemaVersion: manifest.SchemaVersion, Files: map[string]manifest.Target{"index.html": {SHA256: sha, Size: int64(len(html)), ContentType: "text/html"}, "script.js": {SHA256: scriptSHA, Size: int64(len(script)), ContentType: "application/javascript"}}})
	must(t, obj.PutManifest(ctx, org, site.ID, ver.ID, raw))
	must(t, st.AddProjectRoomSite(ctx, tn, room.ID, site.ID, store.AuditRecord{}))
	must(t, st.AddProjectRoomSite(ctx, tn, private.ID, secretSite.ID, store.AuditRecord{}))
	if _, _, err := st.InviteProjectGuest(ctx, store.Tenant{OrgID: org, UserID: wrong}, room.ID, "intruder@example.com", "viewer", 7); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatalf("nonmember admin shared private project: %v", err)
	}
	share, token, err := st.InviteProjectGuest(ctx, tn, room.ID, "Guest@Example.com", "reviewer", 7)
	must(t, err)
	if strings.Contains(share.ID, token) || share.Email != "guest@example.com" {
		t.Fatal("bad invitation")
	}
	if _, _, err := st.InviteProjectGuest(ctx, tn, room.ID, share.Email, "reviewer", 7); !errors.Is(err, store.ErrRoomConflict) {
		t.Fatalf("resend cooldown: %v", err)
	}
	_, viewToken, err := st.InviteProjectGuest(ctx, tn, room.ID, "viewer@example.com", "viewer", 7)
	must(t, err)
	if _, err := st.GuestProject(ctx, wrong, "", token); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatalf("wrong email accepted: %v", err)
	}
	mustExec(t, `UPDATE identity."user" SET "emailVerified"=false WHERE id=$1`, guest)
	if _, err := st.GuestProject(ctx, guest, "", token); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatalf("unverified email accepted: %v", err)
	}
	mustExec(t, `UPDATE identity."user" SET "emailVerified"=true WHERE id=$1`, guest)
	api := handlers.NewFull(quota.Unlimited{}, st, obj, projection.NewLocal())
	api.Rooms = st
	api.CollaborationAllowed = gate
	request := func(user, claimOrg, class, method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		r := router.New(guestVerifier{claimOrg, user, class}, api, nil)
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer verified")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := request(guest, "", auth.ClassHumanSession, "POST", "/v1/shared-projects/accept", map[string]string{"token": token})
	if rec.Code != 200 {
		t.Fatalf("orgless accept %d %s", rec.Code, rec.Body.String())
	}
	// A token in a GET never accepts an invitation.
	rec = request(viewer, "", auth.ClassHumanSession, "GET", "/v1/shared-projects/accept?token="+viewToken, nil)
	if rec.Code < 400 {
		t.Fatal("GET accepted invitation")
	}
	if _, err := st.GuestProject(ctx, viewer, room.ID, ""); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatal("GET changed pending invitation")
	}
	_, err = st.GuestProject(ctx, viewer, "", viewToken)
	must(t, err)
	rec = request(guest, otherOrg, auth.ClassHumanSession, "GET", "/v1/shared-projects/"+room.ID, nil)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), private.ID) {
		t.Fatalf("foreign JWT org steered guest %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"/v1/sites/" + site.ID, "/v1/provider-credentials", "/v1/project-rooms/" + room.ID} {
		rec = request(guest, "", auth.ClassHumanSession, "GET", p, nil)
		if rec.Code < 400 {
			t.Fatalf("guest reached generic route %s %d", p, rec.Code)
		}
	}
	rec = request(guest, "", auth.ClassMCPOAuth, "GET", "/v1/shared-projects", nil)
	if rec.Code < 400 {
		t.Fatal("MCP credential entered guest API")
	}
	preview := "/v1/shared-projects/" + room.ID + "/sites/" + site.ID + "/versions/" + ver.ID + "/files/content?path="
	rec = request(guest, "", auth.ClassHumanSession, "GET", preview+"index.html", nil)
	if rec.Code != 200 {
		t.Fatalf("verified preview %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"content_type":"text/html"`) {
		t.Fatalf("HTML fragment MIME %s", rec.Body.String())
	}
	rec = request(guest, "", auth.ClassHumanSession, "GET", preview+"script.js", nil)
	if rec.Code != 403 {
		t.Fatalf("guest downloaded script %d", rec.Code)
	}
	if _, err := st.GuestVersion(ctx, guest, room.ID, site.ID, old.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unrelated historic source exposed %v", err)
	}
	if _, err := st.GuestProject(ctx, guest, private.ID, ""); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatalf("other room exposed %v", err)
	}
	if _, err := st.GuestVersion(ctx, guest, room.ID, secretSite.ID, ver.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other site exposed %v", err)
	}
	th, err := st.GuestCreateThread(ctx, guest, room.ID, site.ID, ver.ID, hash, "index.html", "page", json.RawMessage(`{}`), "Please improve this")
	must(t, err)
	rec = request(viewer, "", auth.ClassHumanSession, "POST", "/v1/shared-projects/"+room.ID+"/threads/"+th.ID+"/replies", map[string]string{"body": "not permitted"})
	if rec.Code != 403 {
		t.Fatalf("HTTP viewer write %d", rec.Code)
	}
	for i := 0; i < 6; i++ {
		_, err = st.GuestAddReply(ctx, guest, room.ID, th.ID, "Additional feedback")
		must(t, err)
	}
	page, err := st.GuestThreads(ctx, guest, room.ID, "", "", "")
	must(t, err)
	if len(page.Threads) != 1 || page.Threads[0].RepliesNextCursor == "" {
		t.Fatalf("missing pagination %+v", page)
	}
	cursor := page.Threads[0].RepliesNextCursor
	if _, err := st.GuestThreads(ctx, viewer, room.ID, "", th.ID, cursor); !errors.Is(err, projectroom.ErrBadCursor) {
		t.Fatalf("cross-user cursor accepted %v", err)
	}
	tail, err := st.GuestThreads(ctx, guest, room.ID, "", th.ID, cursor)
	must(t, err)
	if len(tail.Threads[0].Replies) != 2 {
		t.Fatalf("reply page %+v", tail)
	}
	if _, err := st.GuestAddReply(ctx, viewer, room.ID, th.ID, "Denied"); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatalf("viewer commented %v", err)
	}
	if err := st.GuestSignoff(ctx, viewer, room.ID, site.ID, ver.ID, hash); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatalf("viewer signed off %v", err)
	}
	must(t, st.GuestSignoff(ctx, guest, room.ID, site.ID, ver.ID, hash))
	// Pending token expiry does not terminate an already accepted invitation.
	mustExec(t, `UPDATE app.project_guest_shares SET expires_at=now()-interval '1 day',last_invited_at=now()-interval '2 minutes' WHERE id=$1`, share.ID)
	_, err = st.GuestProject(ctx, guest, room.ID, "")
	must(t, err)
	_, rotated, err := st.InviteProjectGuest(ctx, tn, room.ID, share.Email, "reviewer", 7)
	must(t, err)
	_, err = st.GuestProject(ctx, guest, room.ID, "")
	must(t, err)
	if _, err := st.GuestProject(ctx, guest, "", token); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatalf("old invitation token survived rotation %v", err)
	}
	_, err = st.GuestProject(ctx, guest, "", rotated)
	must(t, err)
	for _, mutation := range []string{`UPDATE identity."user" SET email='changed@example.com' WHERE id=$1`, `UPDATE identity."user" SET "emailVerified"=false WHERE id=$1`, `UPDATE identity."user" SET banned=true WHERE id=$1`} {
		mustExec(t, mutation, guest)
		if _, err := st.GuestProject(ctx, guest, room.ID, ""); !errors.Is(err, store.ErrRoomForbidden) {
			t.Fatalf("live identity change retained grant %v", err)
		}
		mustExec(t, `UPDATE identity."user" SET email='guest@example.com',"emailVerified"=true,banned=false WHERE id=$1`, guest)
	}
	enabled.Store(false)
	if _, err := st.GuestProject(ctx, guest, room.ID, ""); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatal("flag off retained grant")
	}
	enabled.Store(true)
	mustExec(t, `UPDATE app.org_meta SET allow_external_sharing=false WHERE id=$1`, org)
	if _, err := st.GuestProject(ctx, guest, room.ID, ""); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatal("policy off retained grant")
	}
	mustExec(t, `UPDATE app.org_meta SET allow_external_sharing=true WHERE id=$1`, org)
	// No member rows are created in either authorization system.
	var members int
	must(t, pool.QueryRow(ctx, `SELECT count(*) FROM identity.member WHERE "userId"=$1`, guest).Scan(&members))
	if members != 0 {
		t.Fatal("guest enrolled in organization")
	}
	tx, err := pool.Begin(ctx)
	must(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('app.current_org_id',$1,true)`, otherOrg)
	must(t, err)
	must(t, tx.QueryRow(ctx, `SELECT count(*) FROM app.project_guest_shares WHERE org_id=$1`, org).Scan(&members))
	if members != 0 {
		t.Fatal("RLS exposed host shares")
	}
	must(t, tx.Rollback(ctx))
	must(t, st.RemoveProjectRoomSite(ctx, tn, room.ID, site.ID, store.AuditRecord{}))
	if _, err := st.GuestVersion(ctx, guest, room.ID, site.ID, ver.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("detached preview %v", err)
	}
	page, err = st.GuestThreads(ctx, guest, room.ID, "", "", "")
	must(t, err)
	if len(page.Threads) != 0 {
		t.Fatal("detached thread exposed")
	}
	if _, err := st.GuestAddReply(ctx, guest, room.ID, th.ID, "Denied"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("detached reply %v", err)
	}
	// One authenticated user bucket applies even without an org claim.
	limited := router.New(guestVerifier{"", guest, auth.ClassHumanSession}, api, nil)
	for i := 0; i < 31; i++ {
		req := httptest.NewRequest("GET", "/v1/shared-projects", nil)
		req.Header.Set("Authorization", "Bearer verified")
		rr := httptest.NewRecorder()
		limited.ServeHTTP(rr, req)
		if i == 30 && rr.Code != 429 {
			t.Fatalf("orgless guest bypassed read limiter %d", rr.Code)
		}
	}
	must(t, st.RevokeProjectGuest(ctx, tn, room.ID, share.ID))
	if _, err := st.GuestProject(ctx, guest, room.ID, ""); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatal("revoked grant retained access")
	}
	rec = request(guest, "", auth.ClassHumanSession, "GET", preview+"index.html", nil)
	if rec.Code != 403 {
		t.Fatalf("HTTP revoked preview %d", rec.Code)
	}
	projects, err := st.ListGuestProjects(ctx, guest)
	must(t, err)
	if len(projects) != 0 {
		t.Fatal("revoked project still listed")
	}
}
