//go:build integration

package integration

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/apikey"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/handlers"
	"github.com/danielpang/dropway/services/api/internal/router"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_LegacyKeyUnenrolledSite(t *testing.T) {
	startPostgres(t)
	applyMigrations(t, repoRoot(t))
	ctx := store.WithHumanSession(context.Background(), true)
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := store.New(pool, quota.Unlimited{})
	org, user := "11111111-1111-1111-1111-111111111111", "a0000000-0000-0000-0000-000000000001"
	tn := store.Tenant{OrgID: org, UserID: user}
	mustExec(t, `INSERT INTO app.org_meta(id,api_keys_enabled) VALUES($1,true)`, org)
	seedAuthOrg(t, org, "legacykey")
	seedAuthMemberTable(t)
	insertMember(t, org, user, "owner")
	must(t, st.EnsureOrgProvisioned(ctx, tn))
	secret, err := apikey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, `INSERT INTO app.api_keys(org_id,created_by,name,key_hash,key_prefix) VALUES($1,$2,'existing automation',$3,'dw_test')`, org, user, apikey.Hash(secret))
	site, err := st.CreateSite(ctx, tn, "legacy-workflow", projection.AccessOrgOnly)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{SiteID: site.ID, ContentHash: strings.Repeat("ab", 32), SizeBytes: 1, Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{SiteID: site.ID, ContentHash: strings.Repeat("cd", 32), SizeBytes: 1, Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	api := handlers.NewFull(quota.Unlimited{}, st, storage.NewFake(), projection.NewLocal())
	api.Rooms = st
	api.KeyAuth = handlers.NewKeyAuthenticator(st, nil, nil, false)
	r := router.New(hostedVerifier{org, user}, api, nil)
	request := func(method, path, body string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s got%d want%d: %s", method, path, w.Code, want, w.Body.String())
		}
	}
	request("GET", "/v1/sites", "", 200)
	request("POST", "/v1/sites/"+site.ID+"/publish", `{"version_id":"`+v1.ID+`"}`, 200)
	request("POST", "/v1/sites/"+site.ID+"/publish", `{"version_id":"`+v2.ID+`"}`, 200)
	request("POST", "/v1/sites/"+site.ID+"/publish", `{"version_id":"`+v1.ID+`"}`, 200)
	// Opting in protects only enrolled sites; disabling restores the old route.
	must(t, st.SetProjectRoomsEnabled(ctx, tn, true, store.AuditRecord{}))
	room, err := st.CreateProjectRoom(ctx, tn, "Explicit opt in", store.AuditRecord{})
	if err != nil {
		t.Fatal(err)
	}
	request("POST", "/v1/sites/"+site.ID+"/publish", `{"version_id":"`+v2.ID+`"}`, 200)
	must(t, st.AddProjectRoomSite(ctx, tn, room.ID, site.ID, store.AuditRecord{}))
	request("POST", "/v1/sites/"+site.ID+"/publish", `{"version_id":"`+v1.ID+`"}`, 403)
	must(t, st.SetProjectRoomsEnabled(ctx, tn, false, store.AuditRecord{}))
	request("POST", "/v1/sites/"+site.ID+"/publish", `{"version_id":"`+v1.ID+`"}`, 200)
	request("DELETE", "/v1/sites/"+site.ID, "", 204)
}
