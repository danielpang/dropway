//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/danielpang/dropway/services/api/internal/router"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type hostedVerifier struct{ org, user string }

func (v hostedVerifier) Verify(_ context.Context, token string) (*auth.Claims, error) {
	if token != "human" {
		return nil, errors.New("invalid")
	}
	return &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: v.user}, OrgID: v.org}, nil
}
func (v hostedVerifier) CredentialClass(*auth.Claims) string { return auth.ClassHumanSession }

type hostedTransport func(*http.Request) (*http.Response, error)

func (f hostedTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestIntegration_HostedExecution(t *testing.T) {
	ctx := store.WithHumanSession(context.Background(), true)
	startPostgres(t)
	applyMigrations(t, repoRoot(t))
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})
	var rollout atomic.Bool
	rollout.Store(true)
	org, owner := "11111111-1111-1111-1111-111111111111", "a0000000-0000-0000-0000-000000000001"
	tn := store.Tenant{OrgID: org, UserID: owner}
	gate := func(_ context.Context, userID, orgID string) bool {
		return rollout.Load() && userID == owner && orgID == org
	}
	st.SetCollaborationGate(gate)
	mustExec(t, `INSERT INTO app.org_meta(id,allow_external_sharing) VALUES($1,true)`, org)
	seedAuthOrg(t, org, "orga")
	seedAuthMemberTable(t)
	insertMember(t, org, owner, "owner")
	must(t, st.EnsureOrgProvisioned(ctx, tn))
	must(t, st.SetProjectRoomsEnabled(ctx, tn, true, store.AuditRecord{}))
	site, err := st.CreateSite(ctx, tn, "hosted", projection.AccessOrgOnly)
	if err != nil {
		t.Fatal(err)
	}
	obj := storage.NewFake()
	source := []byte("<h1>Original</h1>")
	sha := hosted.Hash(string(source))
	digest := manifest.Digest([]manifest.File{{Path: "index.html", SHA256: sha}})
	ver, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{SiteID: site.ID, ContentHash: digest, SizeBytes: int64(len(source)), Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	must(t, obj.PutBlob(ctx, org, sha, bytes.NewReader(source), int64(len(source)), "text/html"))
	raw, _ := json.Marshal(manifest.Stored{SchemaVersion: manifest.SchemaVersion, Files: map[string]manifest.Target{"index.html": {SHA256: sha, Size: int64(len(source)), ContentType: "text/html"}}})
	must(t, obj.PutManifest(ctx, org, site.ID, ver.ID, raw))
	if _, err := st.Publish(ctx, tn, site.ID, ver.ID); err != nil {
		t.Fatal(err)
	}
	room, err := st.CreateProjectRoom(ctx, tn, "Hosted", store.AuditRecord{})
	if err != nil {
		t.Fatal(err)
	}
	must(t, st.AddProjectRoomSite(ctx, tn, room.ID, site.ID, store.AuditRecord{}))
	runtime, err := handlers.NewHostedRuntime(st, bytes.Repeat([]byte{7}, 32), strings.Repeat("worker-secret", 4), "http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	var upstream atomic.Int32
	var missingUsage atomic.Bool
	runtime.HTTPClient = &http.Client{Transport: hostedTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.anthropic.com" {
			t.Fatalf("unexpected upstream %s", req.URL.Host)
		}
		if req.Header.Get("x-api-key") != "sk-ant-test-provider-secret" {
			t.Fatal("gateway key missing or wrong")
		}
		body := `{}`
		if req.Method == http.MethodPost {
			upstream.Add(1)
			if missingUsage.Load() {
				body = `{"content":[]}`
			} else {
				body = `{"content":[{"type":"text","text":"change prepared"}],"usage":{"input_tokens":10,"output_tokens":5}}`
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
	})}
	api := handlers.NewFull(quota.Unlimited{}, st, obj, projection.NewLocal())
	api.Rooms = st
	api.Hosted = runtime
	api.CollaborationAllowed = gate
	r := router.New(hostedVerifier{org, owner}, api, nil)
	request := func(method, path, token string, body any) (int, []byte) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	rollout.Store(false)
	code, capability := request("GET", "/v1/project-collaboration/capabilities", "human", nil)
	if code != 200 || !bytes.Contains(capability, []byte(`"enabled":false`)) || !bytes.Contains(capability, []byte(`"execution_configured":true`)) {
		t.Fatalf("disabled capabilities %d %s", code, capability)
	}
	if code, _ := request("POST", "/v1/provider-credentials", "human", nil); code != 403 {
		t.Fatalf("disabled credential route %d", code)
	}
	if code, _ := request("GET", "/v1/project-rooms", "human", nil); code != 403 {
		t.Fatalf("disabled room route %d", code)
	}
	if code, _ := request("PATCH", "/v1/orgs/project-rooms", "human", map[string]bool{"enabled": true}); code != 403 {
		t.Fatalf("disabled rollout enabled org %d", code)
	}
	if code, body := request("PATCH", "/v1/orgs/project-rooms", "human", map[string]bool{"enabled": false}); code != 200 {
		t.Fatalf("disabled rollout blocks org consent withdrawal %d %s", code, body)
	}
	must(t, st.SetProjectRoomsEnabled(ctx, tn, true, store.AuditRecord{}))
	api.Hosted = nil
	code, capability = request("GET", "/v1/project-collaboration/capabilities", "human", nil)
	if code != 200 || !bytes.Contains(capability, []byte(`"execution_configured":false`)) {
		t.Fatalf("missing runtime capabilities %d %s", code, capability)
	}
	api.Hosted = runtime
	rollout.Store(true)
	code, capability = request("GET", "/v1/project-collaboration/capabilities", "human", nil)
	if code != 200 || !bytes.Contains(capability, []byte(`"enabled":true`)) {
		t.Fatalf("enabled capabilities %d %s", code, capability)
	}
	status, body := request("POST", "/v1/provider-credentials", "human", map[string]any{"provider": "anthropic", "label": "Personal", "api_key": "sk-ant-test-provider-secret"})
	if status != 200 || bytes.Contains(body, []byte("sk-ant-test-provider-secret")) {
		t.Fatalf("credential save %d %s", status, body)
	}
	var credential struct {
		Credential store.ProviderCredential `json:"credential"`
	}
	if err := json.Unmarshal(body, &credential); err != nil {
		t.Fatal(err)
	}
	status, body = request("POST", "/v1/provider-credentials/"+credential.Credential.ID+"/validate", "human", nil)
	if status != 200 {
		t.Fatalf("validate %d %s", status, body)
	}
	config := hosted.Config{Provider: "anthropic", ModelID: "claude-sonnet-4-5", Prompt: "Change the title", Budget: hosted.Budget{MaxTokens: 20000, MaxCalls: 5, MaxSeconds: 300, MaxOutputBytes: 1 << 20}}
	queue := func(key string) store.ExecutionJob {
		t.Helper()
		job, err := st.QueueExecution(ctx, tn, room.ID, site.ID, ver.ID, credential.Credential.ID, key, config)
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	claim := func() *store.ExecutionClaim {
		t.Helper()
		c, err := st.ClaimExecutionJob(ctx, "test-worker")
		if err != nil || c == nil {
			t.Fatalf("claim %+v %v", c, err)
		}
		return c
	}
	j := queue("run-idempotency-1")
	again := queue("run-idempotency-1")
	if j.ID != again.ID {
		t.Fatal("run duplicated")
	}
	c := claim()
	if c.Job.ID != j.ID {
		t.Fatal("wrong job")
	}
	if duplicate, err := st.ClaimExecutionJob(ctx, "duplicate"); err != nil || duplicate != nil {
		t.Fatalf("duplicate active claim %+v %v", duplicate, err)
	}
	status, body = request("GET", "/internal/room-attempts/"+c.AttemptID+"/source", c.AttemptToken, nil)
	if status != 200 || !bytes.Contains(body, []byte(base64.StdEncoding.EncodeToString(source))) {
		t.Fatalf("source %d %s", status, body)
	}
	status, body = request("POST", "/internal/room-gateway/"+c.AttemptID+"/v1/messages", c.GatewayToken, map[string]any{"model": config.ModelID, "max_tokens": 100, "messages": []any{map[string]any{"role": "user", "content": "edit"}}})
	if status != 200 || upstream.Load() != 1 {
		t.Fatalf("gateway %d %s", status, body)
	}
	files := []hosted.File{{Path: "index.html", ContentType: "text/html", ContentBase64: base64.StdEncoding.EncodeToString([]byte("<h1>Edited</h1>"))}}
	cand, err := st.CompleteExecutionCandidate(ctx, c.AttemptToken, "candidate-once", "Updated title", files, obj)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := st.CompleteExecutionCandidate(ctx, c.AttemptToken, "candidate-once", "Updated title", files, obj)
	if err != nil || repeat.ID != cand.ID {
		t.Fatalf("candidate idempotency %+v %v", repeat, err)
	}
	live, err := st.GetSite(ctx, tn, site.ID)
	if err != nil || live.CurrentVersionID == nil || *live.CurrentVersionID != ver.ID {
		t.Fatal("candidate changed live")
	}
	// Capabilities do not authenticate to legacy deploy/publish routes.
	status, _ = request("POST", "/v1/sites/"+site.ID+"/publish", c.AttemptToken, map[string]string{"version_id": ver.ID})
	if status != http.StatusUnauthorized {
		t.Fatalf("capability authenticated as user: %d", status)
	}

	// Reusing content-addressed bytes must not rewrite an existing immutable
	// version's MIME metadata or manifest before human approval.
	_, _, proposedVersion, err := st.CandidateVersions(ctx, tn, room.ID, cand.ID)
	if err != nil {
		t.Fatal(err)
	}
	originalManifest, err := obj.GetManifest(ctx, org, site.ID, proposedVersion.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = queue("run-reuse-artifact")
	reuse := claim()
	differentMIME := append([]hosted.File{}, files...)
	differentMIME[0].ContentType = "image/png"
	if _, err := st.CompleteExecutionCandidate(ctx, reuse.AttemptToken, "reuse-output", "Reuse", differentMIME, obj); !errors.Is(err, store.ErrCandidateArtifact) {
		t.Fatalf("metadata overwrite accepted: %v", err)
	}
	retained, err := obj.GetManifest(ctx, org, site.ID, proposedVersion.ID)
	if err != nil || !bytes.Equal(originalManifest, retained) {
		t.Fatal("immutable manifest changed")
	}
	if _, err := st.CompleteExecutionCandidate(ctx, reuse.AttemptToken, "reuse-output", "Reuse", files, obj); err != nil {
		t.Fatal(err)
	}
	_ = queue("run-clarification")
	clar := claim()
	session, err := st.ClarifyExecution(ctx, clar.AttemptToken, "Which color?", json.RawMessage(`[{"role":"user","content":"edit"}]`), files)
	if err != nil {
		t.Fatal(err)
	}
	same, err := st.ClarifyExecution(ctx, clar.AttemptToken, "Which color?", json.RawMessage(`[{"role":"user","content":"edit"}]`), files)
	if err != nil || same != session {
		t.Fatalf("clarification retry %v", err)
	}
	if _, err := st.HeartbeatExecution(ctx, clar.AttemptToken); err == nil {
		t.Fatal("clarification retained attempt")
	}
	resumed, err := st.ResumeExecution(ctx, tn, room.ID, session, "Blue", "answer-once")
	if err != nil {
		t.Fatal(err)
	}
	fresh := claim()
	if fresh.AttemptID == clar.AttemptID || fresh.Job.ID != resumed.ID || fresh.Answer != "Blue" {
		t.Fatal("resume did not use fresh pinned attempt")
	}
	_, staged, err := st.ExecutionSource(ctx, fresh.AttemptToken, obj)
	if err != nil || len(staged) != 1 || staged[0].ContentBase64 != files[0].ContentBase64 {
		t.Fatalf("resume snapshot lost %+v %v", staged, err)
	}
	must(t, st.CancelExecutionJob(ctx, tn, room.ID, fresh.Job.ID))
	if _, err := st.CompleteExecutionCandidate(ctx, fresh.AttemptToken, "late-result", "late", files, obj); err == nil {
		t.Fatal("cancelled result accepted")
	}
	_ = queue("run-lost-lease")
	lost := claim()
	mustExec(t, `UPDATE app.execution_attempts SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, lost.AttemptID)
	if _, err := st.HeartbeatExecution(ctx, lost.AttemptToken); err == nil {
		t.Fatal("expired lease renewed")
	}
	replacement := claim()
	if replacement.AttemptID == lost.AttemptID || replacement.Job.ID != lost.Job.ID {
		t.Fatal("loss did not get fresh attempt")
	}
	must(t, st.FailExecution(ctx, replacement.AttemptToken, "test_finished"))
	_ = queue("run-no-usage")
	noUsage := claim()
	missingUsage.Store(true)
	status, body = request("POST", "/internal/room-gateway/"+noUsage.AttemptID+"/v1/messages", noUsage.GatewayToken, map[string]any{"model": config.ModelID, "max_tokens": 100, "messages": []any{map[string]any{"role": "user", "content": "edit"}}})
	if status != http.StatusBadGateway {
		t.Fatalf("usage absent accepted %d %s", status, body)
	}
	if _, err := st.HeartbeatExecution(ctx, noUsage.AttemptToken); err == nil {
		t.Fatal("usage failure allowed tools")
	}
	missingUsage.Store(false)

	// Credential references are personal even within the same organization.
	other := store.Tenant{OrgID: org, UserID: "a0000000-0000-0000-0000-000000000002"}
	insertMember(t, org, other.UserID, "member")
	must(t, st.AddProjectRoomMember(ctx, tn, room.ID, other.UserID, "reviewer", store.AuditRecord{}))
	if _, _, _, err := st.ProviderCredentialSecret(ctx, other, credential.Credential.ID, runtime.Vault); err == nil {
		t.Fatal("teammate read provider secret")
	}
	if _, err := st.QueueExecution(ctx, other, room.ID, site.ID, ver.ID, credential.Credential.ID, "borrowed-key", config); err == nil {
		t.Fatal("teammate borrowed credential")
	}
	own, err := st.SaveProviderCredential(ctx, other, "", "anthropic", "Reviewer", "sk-ant-reviewer-secret", runtime.Vault)
	if err != nil {
		t.Fatal(err)
	}
	must(t, st.ValidateProviderCredential(ctx, other, own.ID, own.Generation))
	if _, err := st.QueueExecution(ctx, other, room.ID, site.ID, ver.ID, own.ID, "reviewer-run", config); err == nil {
		t.Fatal("reviewer ran execution")
	}
	small := config
	small.Budget.MaxTokens = 1
	if _, err := st.QueueExecution(ctx, tn, room.ID, site.ID, ver.ID, credential.Credential.ID, "budget-small", small); err != nil {
		t.Fatal(err)
	}
	bounded := claim()
	beforeBudget := upstream.Load()
	status, _ = request("POST", "/internal/room-gateway/"+bounded.AttemptID+"/v1/messages", bounded.GatewayToken, map[string]any{"model": config.ModelID, "max_tokens": 100, "messages": []any{map[string]any{"role": "user", "content": "edit"}}})
	if status != http.StatusConflict || upstream.Load() != beforeBudget {
		t.Fatal("exhausted budget reached provider")
	}
	must(t, st.FailExecution(ctx, bounded.AttemptToken, "budget_test"))
	// Runtime rollout withdrawal is rechecked for existing attempt capabilities,
	// including gateway and candidate paths that bypass the user router.
	_ = queue("run-disabled-attempt")
	gated := claim()
	rollout.Store(false)
	if _, err := st.HeartbeatExecution(ctx, gated.AttemptToken); !errors.Is(err, hosted.ErrDenied) {
		t.Fatalf("disabled heartbeat %v", err)
	}
	if _, err := st.CompleteExecutionCandidate(ctx, gated.AttemptToken, "disabled-output", "disabled", files, obj); !errors.Is(err, hosted.ErrDenied) {
		t.Fatalf("disabled candidate %v", err)
	}
	beforeGate := upstream.Load()
	status, _ = request("POST", "/internal/room-gateway/"+gated.AttemptID+"/v1/messages", gated.GatewayToken, map[string]any{"model": config.ModelID, "max_tokens": 100, "messages": []any{map[string]any{"role": "user", "content": "edit"}}})
	if status != 403 || upstream.Load() != beforeGate {
		t.Fatal("disabled gateway reached provider")
	}
	rollout.Store(true)
	must(t, st.CancelExecutionJob(ctx, tn, room.ID, gated.Job.ID))
	_ = queue("run-disabled-claim")
	rollout.Store(false)
	if claim, err := st.ClaimExecutionJob(ctx, "disabled-worker"); err != nil || claim != nil {
		t.Fatalf("disabled claim %+v %v", claim, err)
	}
	rollout.Store(true)
	_ = queue("run-revoked-member")
	memberRun := claim()
	deleteMember(t, org, owner)
	if _, err := st.HeartbeatExecution(ctx, memberRun.AttemptToken); err == nil {
		t.Fatal("removed org member retained execution")
	}
	insertMember(t, org, owner, "owner")
	must(t, st.CancelExecutionJob(ctx, tn, room.ID, memberRun.Job.ID))
	_ = queue("run-revoked-key")
	revoked := claim()
	before := upstream.Load()
	must(t, st.RevokeProviderCredential(ctx, tn, credential.Credential.ID))
	status, _ = request("POST", "/internal/room-gateway/"+revoked.AttemptID+"/v1/messages", revoked.GatewayToken, map[string]any{"model": config.ModelID, "max_tokens": 100, "messages": []any{map[string]any{"role": "user", "content": "edit"}}})
	if status != http.StatusForbidden || upstream.Load() != before {
		t.Fatal("revoked credential reached provider")
	}

	// Queued execution baselines remain readable even after a newer live version
	// moves ahead; GC must not erase source needed to report/review conflicts.
	restored, err := st.SaveProviderCredential(ctx, tn, credential.Credential.ID, "anthropic", "Restored", "sk-ant-test-provider-secret", runtime.Vault)
	if err != nil {
		t.Fatal(err)
	}
	must(t, st.ValidateProviderCredential(ctx, tn, restored.ID, restored.Generation))
	otherSite, err := st.CreateSite(ctx, tn, "execution-gc", projection.AccessOrgOnly)
	if err != nil {
		t.Fatal(err)
	}
	oldSource, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{SiteID: otherSite.ID, ContentHash: strings.Repeat("29", 32), SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Publish(ctx, tn, otherSite.ID, oldSource.ID); err != nil {
		t.Fatal(err)
	}
	must(t, st.AddProjectRoomSite(ctx, tn, room.ID, otherSite.ID, store.AuditRecord{}))
	beforeGC, err := st.GCOrg(ctx, storage.NewFake(), org, store.GCPolicy{KeepLastN: 0, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.QueueExecution(ctx, tn, room.ID, otherSite.ID, oldSource.ID, restored.ID, "gc-baseline-run", config); err != nil {
		t.Fatal(err)
	}
	newSource, err := st.CreateSiteVersion(ctx, tn, store.CreateSiteVersionParams{SiteID: otherSite.ID, ContentHash: strings.Repeat("30", 32), SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Publish(ctx, tn, otherSite.ID, newSource.ID); err != nil {
		t.Fatal(err)
	}
	afterGC, err := st.GCOrg(ctx, storage.NewFake(), org, store.GCPolicy{KeepLastN: 0, DryRun: true})
	if err != nil || afterGC.RetainedVersions != beforeGC.RetainedVersions+1 {
		t.Fatalf("execution source GC pin lost: before=%+v after=%+v err=%v", beforeGC, afterGC, err)
	}
	must(t, st.RemoveProjectRoomSite(ctx, tn, room.ID, otherSite.ID, store.AuditRecord{}))
	must(t, st.RemoveProjectRoomSite(ctx, tn, room.ID, site.ID, store.AuditRecord{}))
	jobs, err := st.ListExecutionJobs(ctx, tn, room.ID)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("detached jobs leaked %+v %v", jobs, err)
	}

	for _, direction := range []string{"down", "up"} {
		cmd := exec.Command("go", "run", "github.com/pressly/goose/v3/cmd/goose@v3.22.0", "-dir", repoRoot(t)+"/db/migrations/app", "postgres", ownerDSN, direction)
		cmd.Dir = repoRoot(t)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("execution migration %s: %v\n%s", direction, err, output)
		}
	}

}
