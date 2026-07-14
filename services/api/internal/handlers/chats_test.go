package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// chatsRouterFor mirrors the production /v1/chats + site-chat route tree
// locally (avoiding the router→handlers import cycle), authenticated as
// (orgID, userID).
func chatsRouterFor(a *API, orgID, userID string) http.Handler {
	v := fakeVerifier{claims: claims(userID, orgID, "member")}
	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Route("/chats", func(r chi.Router) {
			r.Post("/", a.CreateChatLog)
			r.Get("/", a.ListChatLogs)
			r.Get("/{id}", a.GetChatLog)
			r.Delete("/{id}", a.DeleteChatLog)
			r.Post("/{id}/messages", a.AppendChatMessages)
			r.Get("/{id}/messages", a.ListChatMessages)
			r.Delete("/{id}/messages/{seq}", a.DeleteChatMessage)
			r.Put("/{id}/site", a.SetChatLogSite)
			r.Put("/{id}/panel", a.SetChatLogPanel)
		})
		r.Route("/sites", func(r chi.Router) {
			r.Get("/{id}/chat", a.GetSiteChat)
			r.Post("/{id}/chat", a.AppendSiteChat)
		})
		r.Get("/orgs/chat-logs", a.GetChatSettings)
		r.Patch("/orgs/chat-logs", a.PatchChatSettings)
	})
	return r
}

type chatLogEnvelope struct {
	ChatLog  chatLogResponse `json:"chat_log"`
	Appended int             `json:"appended"`
	Pruned   int64           `json:"pruned"`
	Window   int64           `json:"window"`
	Dropped  int             `json:"dropped"`
}

type chatMessagesEnvelope struct {
	Messages []chatMessageResponse `json:"messages"`
	Pruned   int64                 `json:"pruned"`
	Window   int64                 `json:"window"`
}

// TestChatFlow_CreateAppendListDelete drives the core loop: create → append
// turns + an action annotation → list → delete one message → delete the log.
// The compiled transcript object must track every mutation.
func TestChatFlow_CreateAppendListDelete(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	obj := storage.NewFake()
	a := NewFull(quota.Unlimited{}, fs, obj, nil)
	h := chatsRouterFor(a, "org_1", "user_1")

	rr := do(t, h, http.MethodPost, "/v1/chats", `{"title":"how the dashboard was made","source_tool":"claude_code"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created chatLogEnvelope
	mustJSON(t, rr, &created)
	id := created.ChatLog.ID
	if created.ChatLog.SourceTool != "claude_code" || !created.ChatLog.PanelEnabled {
		t.Fatalf("created log = %+v", created.ChatLog)
	}

	rr = do(t, h, http.MethodPost, "/v1/chats/"+id+"/messages", `{
		"messages": [
			{"role":"user","content":"make the chart blue"},
			{"role":"assistant","content":"done — switched the palette"},
			{"kind":"action","content":"inlined the font to satisfy the CSP","meta":{"action":"file_edit","paths":["index.html"]}}
		]
	}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("append: %d %s", rr.Code, rr.Body.String())
	}
	var appended chatMessagesEnvelope
	mustJSON(t, rr, &appended)
	if len(appended.Messages) != 3 {
		t.Fatalf("appended %d messages, want 3", len(appended.Messages))
	}
	// The action row defaults role=assistant and carries its meta through.
	act := appended.Messages[2]
	if act.Kind != "action" || act.Role != "assistant" || len(act.Meta) == 0 {
		t.Fatalf("action row = %+v", act)
	}

	// The served transcript object was compiled.
	if _, err := obj.GetChatTranscript(context.Background(), "org_1", id); err != nil {
		t.Fatalf("transcript object missing after append: %v", err)
	}

	rr = do(t, h, http.MethodGet, "/v1/chats/"+id+"/messages", "")
	var listed chatMessagesEnvelope
	mustJSON(t, rr, &listed)
	if len(listed.Messages) != 3 || listed.Messages[0].Seq != 1 {
		t.Fatalf("list = %+v", listed.Messages)
	}

	// Delete message 2 (the pasted-secret escape hatch); seq numbers survive.
	rr = do(t, h, http.MethodDelete, "/v1/chats/"+id+"/messages/2", "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete message: %d %s", rr.Code, rr.Body.String())
	}
	rr = do(t, h, http.MethodGet, "/v1/chats/"+id+"/messages", "")
	mustJSON(t, rr, &listed)
	if len(listed.Messages) != 2 || listed.Messages[1].Seq != 3 {
		t.Fatalf("after delete = %+v", listed.Messages)
	}

	rr = do(t, h, http.MethodDelete, "/v1/chats/"+id, "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete log: %d %s", rr.Code, rr.Body.String())
	}
	// Teardown removed the served transcript.
	if _, err := obj.GetChatTranscript(context.Background(), "org_1", id); err == nil {
		t.Fatal("transcript object should be deleted with the log")
	}
	rr = do(t, h, http.MethodGet, "/v1/chats/"+id, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("get deleted: %d", rr.Code)
	}
}

// TestChatImport_TranscriptNormalized creates a log seeded from a raw Claude
// Code JSONL export with derive_actions: tool_use events become action rows.
func TestChatImport_TranscriptNormalized(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil)
	h := chatsRouterFor(a, "org_1", "user_1")

	rr := do(t, h, http.MethodPost, "/v1/chats", `{
		"title": "imported",
		"source_tool": "claude_code",
		"format": "claude_code",
		"derive_actions": true,
		"transcript": "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"build it\"}}\n{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"on it\"},{\"type\":\"tool_use\",\"name\":\"Edit\",\"input\":{\"file_path\":\"app.js\"}}]}}"
	}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	var created chatLogEnvelope
	mustJSON(t, rr, &created)
	if created.Appended != 3 {
		t.Fatalf("appended = %d, want 3 (user turn, assistant turn, derived action)", created.Appended)
	}

	rr = do(t, h, http.MethodGet, "/v1/chats/"+created.ChatLog.ID+"/messages", "")
	var listed chatMessagesEnvelope
	mustJSON(t, rr, &listed)
	if listed.Messages[2].Kind != "action" {
		t.Fatalf("derived action missing: %+v", listed.Messages)
	}
}

// TestChatAppend_FreeWindowPrunes emulates the free tier: appends never 402,
// the log keeps its newest `window` rows, and the response discloses pruning.
func TestChatAppend_FreeWindowPrunes(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	fs.ch().window = 10
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil)
	h := chatsRouterFor(a, "org_1", "user_1")

	rr := do(t, h, http.MethodPost, "/v1/chats", `{"title":"long"}`)
	var created chatLogEnvelope
	mustJSON(t, rr, &created)
	id := created.ChatLog.ID

	body := `{"messages":[`
	for i := 0; i < 12; i++ {
		if i > 0 {
			body += ","
		}
		body += `{"role":"user","content":"m"}`
	}
	body += `]}`
	rr = do(t, h, http.MethodPost, "/v1/chats/"+id+"/messages", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("windowed append must not fail: %d %s", rr.Code, rr.Body.String())
	}
	var res chatMessagesEnvelope
	mustJSON(t, rr, &res)
	if res.Pruned != 2 || res.Window != 10 {
		t.Fatalf("pruned/window = %d/%d, want 2/10", res.Pruned, res.Window)
	}
	rr = do(t, h, http.MethodGet, "/v1/chats/"+id+"/messages", "")
	var listed chatMessagesEnvelope
	mustJSON(t, rr, &listed)
	if len(listed.Messages) != 10 || listed.Messages[0].Seq != 3 || listed.Messages[9].Seq != 12 {
		t.Fatalf("survivors = %d rows, first seq %d — want the newest 10 (3..12)",
			len(listed.Messages), listed.Messages[0].Seq)
	}
}

// TestChatAppend_HardCap402 emulates the pro tier: the batch fits entirely or
// 402s with the standard quota body before any row lands.
func TestChatAppend_HardCap402(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	fs.ch().msgCap = 5
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil)
	h := chatsRouterFor(a, "org_1", "user_1")

	rr := do(t, h, http.MethodPost, "/v1/chats", `{"title":"capped"}`)
	var created chatLogEnvelope
	mustJSON(t, rr, &created)
	id := created.ChatLog.ID

	rr = do(t, h, http.MethodPost, "/v1/chats/"+id+"/messages",
		`{"messages":[{"role":"user","content":"1"},{"role":"user","content":"2"},{"role":"user","content":"3"},{"role":"user","content":"4"}]}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("under cap: %d %s", rr.Code, rr.Body.String())
	}
	rr = do(t, h, http.MethodPost, "/v1/chats/"+id+"/messages",
		`{"messages":[{"role":"user","content":"5"},{"role":"user","content":"6"}]}`)
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("over cap = %d, want 402", rr.Code)
	}
	var quotaBody struct {
		Limit    string `json:"limit"`
		NextTier string `json:"next_tier"`
	}
	mustJSON(t, rr, &quotaBody)
	if quotaBody.Limit != string(quota.ResourceChatMessagePerLog) || quotaBody.NextTier != "business" {
		t.Fatalf("402 body = %+v", quotaBody)
	}
	// No partial import: the failed batch left the log at 4 rows.
	rr = do(t, h, http.MethodGet, "/v1/chats/"+id+"/messages", "")
	var listed chatMessagesEnvelope
	mustJSON(t, rr, &listed)
	if len(listed.Messages) != 4 {
		t.Fatalf("rows after failed batch = %d, want 4", len(listed.Messages))
	}
}

// TestChatAttachDetachMove exercises the optional site binding: attach, the
// one-log-per-site conflict, move, and detach.
func TestChatAttachDetachMove(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil)
	h := chatsRouterFor(a, "org_1", "user_1")

	// Two sites via the base fake.
	if _, err := fs.CreateSite(context.Background(), tenantFor("org_1", "user_1"), "alpha", "public"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.CreateSite(context.Background(), tenantFor("org_1", "user_1"), "beta", "public"); err != nil {
		t.Fatal(err)
	}

	rr := do(t, h, http.MethodPost, "/v1/chats", `{"title":"a","site_id":"site_alpha"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create attached: %d %s", rr.Code, rr.Body.String())
	}
	var first chatLogEnvelope
	mustJSON(t, rr, &first)

	// Second log on the same site → 409.
	rr = do(t, h, http.MethodPost, "/v1/chats", `{"title":"b","site_id":"site_alpha"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("second attach = %d, want 409", rr.Code)
	}

	// Move to beta, then detach.
	rr = do(t, h, http.MethodPut, "/v1/chats/"+first.ChatLog.ID+"/site", `{"site_id":"site_beta"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("move: %d %s", rr.Code, rr.Body.String())
	}
	var moved chatLogEnvelope
	mustJSON(t, rr, &moved)
	if moved.ChatLog.SiteID == nil || *moved.ChatLog.SiteID != "site_beta" {
		t.Fatalf("moved = %+v", moved.ChatLog)
	}
	rr = do(t, h, http.MethodPut, "/v1/chats/"+first.ChatLog.ID+"/site", `{"site_id":null}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("detach: %d %s", rr.Code, rr.Body.String())
	}
	var detached chatLogEnvelope
	mustJSON(t, rr, &detached)
	if detached.ChatLog.SiteID != nil {
		t.Fatalf("detached = %+v", detached.ChatLog)
	}
}

// TestSiteChatConvenience drives the agent flow: POST /v1/sites/{id}/chat
// creates the log on first append; GET returns log + messages; appends while
// attached stamp the site's current version.
func TestSiteChatConvenience(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil)
	h := chatsRouterFor(a, "org_1", "user_1")

	site, err := fs.CreateSite(context.Background(), tenantFor("org_1", "user_1"), "gamma", "public")
	if err != nil {
		t.Fatal(err)
	}
	vid := "ver_live"
	site.CurrentVersionID = &vid
	fs.sites[site.ID] = site

	rr := do(t, h, http.MethodPost, "/v1/sites/"+site.ID+"/chat",
		`{"messages":[{"role":"user","content":"ship it"}]}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("site append: %d %s", rr.Code, rr.Body.String())
	}
	rr = do(t, h, http.MethodGet, "/v1/sites/"+site.ID+"/chat", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("site chat get: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		ChatLog  chatLogResponse       `json:"chat_log"`
		Messages []chatMessageResponse `json:"messages"`
	}
	mustJSON(t, rr, &got)
	if got.ChatLog.SiteID == nil || *got.ChatLog.SiteID != site.ID {
		t.Fatalf("log not attached: %+v", got.ChatLog)
	}
	if len(got.Messages) != 1 || got.Messages[0].VersionID == nil || *got.Messages[0].VersionID != vid {
		t.Fatalf("message version stamp = %+v", got.Messages)
	}
}

// TestChatKillSwitch: disabled org → every chat endpoint 403s; the settings
// PATCH is admin-only.
func TestChatKillSwitch(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	fs.p2().members["admin_1"] = "admin"
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil)
	member := chatsRouterFor(a, "org_1", "user_1")
	admin := chatsRouterFor(a, "org_1", "admin_1")

	// A member may not flip the switch.
	rr := do(t, member, http.MethodPatch, "/v1/orgs/chat-logs", `{"enabled":false}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("member patch = %d, want 403", rr.Code)
	}
	rr = do(t, admin, http.MethodPatch, "/v1/orgs/chat-logs", `{"enabled":false}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin patch: %d %s", rr.Code, rr.Body.String())
	}
	rr = do(t, member, http.MethodPost, "/v1/chats", `{"title":"x"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("create while disabled = %d, want 403", rr.Code)
	}
	rr = do(t, member, http.MethodGet, "/v1/chats", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("list while disabled = %d, want 403", rr.Code)
	}
}

// TestChatOwnerOrAdminGate: a non-owner member can't mutate someone else's
// log; an org admin can.
func TestChatOwnerOrAdminGate(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["owner_1"] = "member"
	fs.p2().members["other_1"] = "member"
	fs.p2().members["admin_1"] = "admin"
	a := NewFull(quota.Unlimited{}, fs, storage.NewFake(), nil)
	owner := chatsRouterFor(a, "org_1", "owner_1")
	other := chatsRouterFor(a, "org_1", "other_1")
	admin := chatsRouterFor(a, "org_1", "admin_1")

	rr := do(t, owner, http.MethodPost, "/v1/chats", `{"title":"mine"}`)
	var created chatLogEnvelope
	mustJSON(t, rr, &created)
	id := created.ChatLog.ID

	rr = do(t, other, http.MethodDelete, "/v1/chats/"+id, "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("other member delete = %d, want 403", rr.Code)
	}
	rr = do(t, other, http.MethodPost, "/v1/chats/"+id+"/messages",
		`{"messages":[{"role":"user","content":"hi"}]}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("other member append = %d, want 403", rr.Code)
	}
	rr = do(t, admin, http.MethodDelete, "/v1/chats/"+id, "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("admin delete = %d, want 204", rr.Code)
	}
}

// tenantFor builds a store.Tenant for direct fake calls in tests.
func tenantFor(orgID, userID string) store.Tenant {
	return store.Tenant{OrgID: orgID, UserID: userID}
}
