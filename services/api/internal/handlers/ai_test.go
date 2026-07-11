package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/openrouter"
	"github.com/danielpang/dropway/internal/quota"
	aipkg "github.com/danielpang/dropway/services/api/internal/ai"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// withURLParam attaches a chi URL param to a request so a handler that reads
// chi.URLParam resolves it without a full router mount.
func withURLParam(req *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// fakeRunner is a scripted AITurnRunner: it emits a couple of token events and a
// draft_ready without touching OpenRouter or a sandbox.
type fakeRunner struct {
	err error
}

func (f *fakeRunner) RunTurn(ctx context.Context, _ store.Tenant, _ store.AISession, _ string, _ time.Duration, contentURL aipkg.ContentURL) error {
	emit := aipkg.EmitFromContext(ctx) //nolint
	emit(aipkg.Event{Type: "token", Text: "Building"})
	if f.err != nil {
		return f.err
	}
	emit(aipkg.Event{Type: "draft_ready", VersionID: "ver_1", PreviewURL: contentURL("abc--org--site.dropwaycontent.com")})
	return nil
}

// fakeCatalog is a scripted AIModelCatalog.
type fakeCatalog struct{}

func (fakeCatalog) Models(context.Context) ([]openrouter.Model, error) {
	return []openrouter.Model{{ID: "anthropic/claude-sonnet-4.5", Name: "Claude Sonnet"}}, nil
}

func newAITestAPI(fs *fakeStore) *API {
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.AI = &fakeRunner{}
	a.AIModels = fakeCatalog{}
	a.AIDefaultModel = "anthropic/claude-sonnet-4.5"
	a.AIMaxConcurrent = 2
	return a
}

func TestCreateAISession_NewSite_201(t *testing.T) {
	fs := newFakeStore()
	a := newAITestAPI(fs)
	h := authed(a.CreateAISession, claims("user_1", "org_1", "member"))

	req := jsonReq(http.MethodPost, "/v1/ai/sessions", `{"slug":"my-site"}`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var body aiSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SiteID != "site_my-site" || body.Model != "anthropic/claude-sonnet-4.5" {
		t.Errorf("session = %+v", body)
	}
}

func TestCreateAISession_Disabled_403(t *testing.T) {
	fs := newFakeStore()
	fs.ai().settings.Enabled = false
	a := newAITestAPI(fs)
	h := authed(a.CreateAISession, claims("user_1", "org_1", "member"))

	req := jsonReq(http.MethodPost, "/v1/ai/sessions", `{"slug":"s"}`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateAISession_ConcurrencyLimit_429(t *testing.T) {
	fs := newFakeStore()
	// Pre-seed a site so the create path reaches StartAISession.
	_, _ = fs.CreateSite(context.Background(), store.Tenant{OrgID: "org_1", UserID: "user_1"}, "s", "public")
	fs.ai().concurrErr = true
	a := newAITestAPI(fs)
	h := authed(a.CreateAISession, claims("user_1", "org_1", "member"))

	req := jsonReq(http.MethodPost, "/v1/ai/sessions", `{"site_id":"site_s"}`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", rr.Code, rr.Body.String())
	}
}

func TestCreateAISession_NoRunner_503(t *testing.T) {
	fs := newFakeStore()
	a := NewFull(quota.Unlimited{}, fs, nil, nil) // no AI runner wired
	h := authed(a.CreateAISession, claims("user_1", "org_1", "member"))

	req := jsonReq(http.MethodPost, "/v1/ai/sessions", `{"slug":"s"}`)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestPostAIMessage_StreamsSSE(t *testing.T) {
	fs := newFakeStore()
	a := newAITestAPI(fs)
	// Seed the site, then a session.
	fs.sites["site_x"] = store.Site{ID: "site_x", OrgID: "org_1", Slug: "x"}
	sess, err := fs.StartAISession(context.Background(), store.Tenant{OrgID: "org_1", UserID: "user_1"}, "site_x", "m", nil, 2)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}

	h := authed(a.PostAIMessage, claims("user_1", "org_1", "member"))
	req := jsonReq(http.MethodPost, "/v1/ai/sessions/"+sess.ID+"/messages", `{"text":"make it blue"}`)
	req = withURLParam(req, "id", sess.ID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: token") {
		t.Errorf("missing token event: %s", body)
	}
	if !strings.Contains(body, "event: draft_ready") {
		t.Errorf("missing draft_ready event: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Errorf("missing done event: %s", body)
	}
}

func TestPostAIMessage_ConcurrentTurnRejected(t *testing.T) {
	fs := newFakeStore()
	a := newAITestAPI(fs)
	fs.sites["site_x"] = store.Site{ID: "site_x", OrgID: "org_1", Slug: "x"}
	sess, err := fs.StartAISession(context.Background(), store.Tenant{OrgID: "org_1", UserID: "user_1"}, "site_x", "m", nil, 2)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	// Simulate a turn already running.
	_ = fs.SetAISessionStatus(context.Background(), store.Tenant{OrgID: "org_1", UserID: "user_1"}, sess.ID, "running")

	h := authed(a.PostAIMessage, claims("user_1", "org_1", "member"))
	req := jsonReq(http.MethodPost, "/v1/ai/sessions/"+sess.ID+"/messages", `{"text":"again"}`)
	req = withURLParam(req, "id", sess.ID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rr.Code, rr.Body.String())
	}
}

func TestListAIModels(t *testing.T) {
	fs := newFakeStore()
	a := newAITestAPI(fs)
	h := authed(a.ListAIModels, claims("user_1", "org_1", "member"))
	req := httptest.NewRequest(http.MethodGet, "/v1/ai/models", nil)
	req.Header.Set("Authorization", "Bearer x")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "claude-sonnet") {
		t.Errorf("body = %s", rr.Body.String())
	}
}
