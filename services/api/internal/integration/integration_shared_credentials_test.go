//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/handlers"
	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/danielpang/dropway/services/api/internal/router"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_SharedProviderCredentials(t *testing.T) {
	ctx := store.WithHumanSession(context.Background(), true)
	startPostgres(t)
	applyMigrations(t, repoRoot(t))
	// Exercise additive migration downgrade before storing new-scope data.
	for _, args := range [][]string{{"down-to", "22"}, {"up"}} {
		base := []string{"run", "github.com/pressly/goose/v3/cmd/goose@v3.22.0", "-dir", repoRoot(t) + "/db/migrations/app", "postgres", ownerDSN}
		cmd := exec.Command("go", append(base, args...)...)
		cmd.Dir = repoRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("migration %v: %v %s", args, err, out)
		}
	}
	pool, err := pgxpool.New(ctx, appDSN)
	must(t, err)
	defer pool.Close()
	st := store.New(pool, quota.Unlimited{})
	org := "11111111-1111-1111-1111-111111111111"
	owner := store.Tenant{OrgID: org, UserID: "a0000000-0000-0000-0000-000000000001"}
	editor := store.Tenant{OrgID: org, UserID: "a0000000-0000-0000-0000-000000000002"}
	admin := store.Tenant{OrgID: org, UserID: "a0000000-0000-0000-0000-000000000003"}
	outsider := store.Tenant{OrgID: "22222222-2222-2222-2222-222222222222", UserID: editor.UserID}
	st.SetCollaborationGate(func(_ context.Context, _ string, oid string) bool { return oid == org })
	for _, id := range []string{org, outsider.OrgID} {
		mustExec(t, `INSERT INTO app.org_meta(id,allow_external_sharing) VALUES($1,true)`, id)
	}
	seedAuthOrg(t, org, "orga")
	seedAuthOrg(t, outsider.OrgID, "orgb")
	seedAuthMemberTable(t)
	insertMember(t, org, owner.UserID, "owner")
	insertMember(t, org, editor.UserID, "member")
	insertMember(t, org, admin.UserID, "admin")
	insertMember(t, outsider.OrgID, editor.UserID, "owner")
	must(t, st.EnsureOrgProvisioned(ctx, owner))
	must(t, st.SetProjectRoomsEnabled(ctx, owner, true, store.AuditRecord{}))
	site, err := st.CreateSite(ctx, editor, "shared", projection.AccessOrgOnly)
	must(t, err)
	ver, err := st.CreateSiteVersion(ctx, editor, store.CreateSiteVersionParams{SiteID: site.ID, ContentHash: strings.Repeat("ab", 32), SizeBytes: 1, Status: "ready"})
	must(t, err)
	_, err = st.Publish(ctx, editor, site.ID, ver.ID)
	must(t, err)
	room, err := st.CreateProjectRoom(ctx, owner, "Shared", store.AuditRecord{})
	must(t, err)
	must(t, st.AddProjectRoomMember(ctx, owner, room.ID, editor.UserID, "editor", store.AuditRecord{}))
	must(t, st.AddProjectRoomSite(ctx, owner, room.ID, site.ID, store.AuditRecord{}))
	runtime, err := handlers.NewHostedRuntime(st, bytes.Repeat([]byte{8}, 32), strings.Repeat("worker", 8), "http://localhost:8080")
	must(t, err)
	personal, err := st.SaveProviderCredential(ctx, owner, "", "anthropic", "Personal", "sk-ant-personal-secret", runtime.Vault)
	must(t, err)
	must(t, st.ValidateProviderCredential(ctx, owner, personal.ID, personal.Generation))
	if personal.Scope != "personal" || !personal.CanManage {
		t.Fatalf("legacy scope %+v", personal)
	}
	if _, err := st.SaveProviderCredential(ctx, editor, "", "openai", "Illegal team", "sk-openai-secret", runtime.Vault, "team"); !errors.Is(err, hosted.ErrDenied) {
		t.Fatalf("editor created team: %v", err)
	}
	team, err := st.SaveProviderCredential(ctx, owner, "", "openai", "Team", "sk-openai-team-secret", runtime.Vault, "team")
	must(t, err)
	must(t, st.ValidateProviderCredential(ctx, admin, team.ID, team.Generation))
	if _, err := st.SaveProviderCredential(ctx, admin, team.ID, "openai", "Wrong scope", "sk-new-team-secret", runtime.Vault, "personal"); !errors.Is(err, hosted.ErrConflict) {
		t.Fatalf("scope changed: %v", err)
	}
	for _, caller := range []store.Tenant{editor, outsider} {
		if _, _, _, err := st.ProviderCredentialSecret(ctx, caller, team.ID, runtime.Vault); !errors.Is(err, hosted.ErrDenied) {
			t.Fatalf("unauthorized secret: %v", err)
		}
		if err := st.RevokeProviderCredential(ctx, caller, team.ID); !errors.Is(err, hosted.ErrDenied) {
			t.Fatalf("unauthorized revoke: %v", err)
		}
		if _, err := st.SaveProviderCredential(ctx, caller, team.ID, "openai", "Hijack", "sk-new-team-secret", runtime.Vault, "team"); !errors.Is(err, hosted.ErrDenied) {
			t.Fatalf("unauthorized replace: %v", err)
		}
	}
	listed, err := st.ListProviderCredentials(ctx, editor)
	must(t, err)
	if len(listed) != 1 || listed[0].ID != team.ID || listed[0].CanManage {
		t.Fatalf("editor projection %+v", listed)
	}
	listed, err = st.ListProviderCredentials(ctx, admin)
	must(t, err)
	if len(listed) != 1 || !listed[0].CanManage {
		t.Fatalf("admin projection %+v", listed)
	}
	listed, err = st.ListProviderCredentials(ctx, outsider)
	must(t, err)
	if len(listed) != 0 {
		t.Fatal("cross-org credentials exposed")
	}
	config := hosted.Config{Provider: "openai", ModelID: "gpt-4.1", Prompt: "Change title", Budget: hosted.Budget{MaxTokens: 20000, MaxCalls: 4, MaxSeconds: 300, MaxOutputBytes: 1 << 20}}
	personalConfig := config
	personalConfig.Provider = "anthropic"
	personalConfig.ModelID = "claude-sonnet-4-5"
	if _, err := st.QueueExecution(ctx, editor, room.ID, site.ID, ver.ID, personal.ID, "private-borrow", personalConfig); !errors.Is(err, hosted.ErrDenied) {
		t.Fatalf("borrow personal: %v", err)
	}
	queue := func(key string) *store.ExecutionClaim {
		t.Helper()
		_, err := st.QueueExecution(ctx, editor, room.ID, site.ID, ver.ID, team.ID, key, config)
		must(t, err)
		c, err := st.ClaimExecutionJob(ctx, "shared-worker")
		must(t, err)
		if c == nil {
			t.Fatal("no shared claim")
		}
		return c
	}
	c := queue("shared-before-replace")
	// Replacement by a different admin preserves the original encryption AAD.
	team, err = st.SaveProviderCredential(ctx, admin, team.ID, "openai", "Team rotated", "sk-openai-rotated-secret", runtime.Vault, "team")
	must(t, err)
	if team.OwnerUserID != owner.UserID {
		t.Fatal("replacement changed encryption identity")
	}
	if _, err := st.HeartbeatExecution(ctx, c.AttemptToken); !errors.Is(err, hosted.ErrDenied) {
		t.Fatalf("old generation authorized: %v", err)
	}
	must(t, st.CancelExecutionJob(ctx, editor, room.ID, c.Job.ID))
	must(t, st.ValidateProviderCredential(ctx, admin, team.ID, team.Generation))
	c = queue("shared-openai-gateway")
	calls := 0
	runtime.HTTPClient = &http.Client{Transport: hostedTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != "https://api.openai.com/v1/chat/completions" || req.Header.Get("Authorization") != "Bearer sk-openai-rotated-secret" {
			t.Fatalf("wrong OpenAI request %s", req.URL)
		}
		var body map[string]any
		must(t, json.NewDecoder(req.Body).Decode(&body))
		options, _ := body["stream_options"].(map[string]any)
		if options["include_usage"] != true {
			t.Fatal("missing streamed usage request")
		}
		data := "data: {\"choices\":[{\"delta\":{\"content\":\"ready\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n"
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data)), Header: http.Header{"Content-Type": []string{"text/event-stream"}}}, nil
	})}
	api := handlers.NewFull(quota.Unlimited{}, st, storage.NewFake(), projection.NewLocal())
	api.Hosted = runtime
	api.Rooms = st
	r := router.New(hostedVerifier{org, editor.UserID}, api, nil)
	raw := `{"model":"gpt-4.1","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"edit"}]}`
	req := httptest.NewRequest("POST", "/internal/room-gateway/"+c.AttemptID+"/v1/chat/completions", strings.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+c.GatewayToken)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 || calls != 1 || !strings.Contains(rec.Body.String(), "ready") {
		t.Fatalf("OpenAI gateway %d %s", rec.Code, rec.Body.String())
	}
	// Revocation and live membership checks stop team-key use independently of
	// the credential creator's identity and the worker's existing capability.
	deleteMember(t, org, editor.UserID)
	if _, err := st.HeartbeatExecution(ctx, c.AttemptToken); !errors.Is(err, hosted.ErrDenied) {
		t.Fatalf("removed member retained team use: %v", err)
	}
	insertMember(t, org, editor.UserID, "member")
	must(t, st.RevokeProviderCredential(ctx, admin, team.ID))
	if _, err := st.ReserveGatewayCall(ctx, c.GatewayToken, 10, 10, runtime.Vault); !errors.Is(err, hosted.ErrDenied) {
		t.Fatalf("revoked shared gateway: %v", err)
	}
}
