package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielpang/dropway/cli/internal/api"
)

// fakeChatClient records the calls it received and returns canned responses,
// simulating the chat-log server flows.
type fakeChatClient struct {
	sites []api.Site
	logs  []api.ChatLog

	created    *api.CreateChatLogRequest
	createResp *api.CreateChatLogResponse
	createErr  error

	messages []api.ChatMessage

	appendedID   string // chat id of the last log-scoped append
	appendedSite string // site id of the last site-scoped append
	appendedReq  *api.ChatImport
	appendResp   *api.ChatAppendResponse
	appendErr    error

	setSiteID     string  // chat id of the last SetChatLogSite call
	setSiteTarget *string // its site_id argument
	setSiteErr    error

	panelID      string
	panelEnabled bool

	deletedLog  string
	deletedSeqs []int32
}

func (f *fakeChatClient) CreateChatLog(_ context.Context, req api.CreateChatLogRequest) (*api.CreateChatLogResponse, error) {
	f.created = &req
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.createResp != nil {
		return f.createResp, nil
	}
	return &api.CreateChatLogResponse{ChatLog: api.ChatLog{ID: "chat_1", Title: req.Title}, Appended: 1}, nil
}

func (f *fakeChatClient) ListChatLogs(_ context.Context) (*api.ChatLogsResponse, error) {
	return &api.ChatLogsResponse{ChatLogs: f.logs}, nil
}

func (f *fakeChatClient) GetChatLog(_ context.Context, id string) (*api.ChatLog, error) {
	for i := range f.logs {
		if f.logs[i].ID == id {
			return &f.logs[i], nil
		}
	}
	return nil, errors.New("fake: no chat log " + id)
}

func (f *fakeChatClient) ListChatMessages(_ context.Context, id string, afterSeq, limit int) (*api.ChatMessagesResponse, error) {
	return &api.ChatMessagesResponse{Messages: f.messages}, nil
}

func (f *fakeChatClient) AppendChatMessages(_ context.Context, id string, req api.ChatImport) (*api.ChatAppendResponse, error) {
	f.appendedID, f.appendedReq = id, &req
	return f.appendResult(req)
}

func (f *fakeChatClient) AppendSiteChat(_ context.Context, siteID string, req api.ChatImport) (*api.ChatAppendResponse, error) {
	f.appendedSite, f.appendedReq = siteID, &req
	return f.appendResult(req)
}

func (f *fakeChatClient) appendResult(req api.ChatImport) (*api.ChatAppendResponse, error) {
	if f.appendErr != nil {
		return nil, f.appendErr
	}
	if f.appendResp != nil {
		return f.appendResp, nil
	}
	out := make([]api.ChatMessage, len(req.Messages))
	for i, m := range req.Messages {
		out[i] = api.ChatMessage{Seq: int32(i + 1), Role: m.Role, Kind: m.Kind, Content: m.Content}
	}
	return &api.ChatAppendResponse{Messages: out}, nil
}

func (f *fakeChatClient) SetChatLogSite(_ context.Context, id string, siteID *string) (*api.ChatLog, error) {
	f.setSiteID, f.setSiteTarget = id, siteID
	if f.setSiteErr != nil {
		return nil, f.setSiteErr
	}
	return &api.ChatLog{ID: id, SiteID: siteID}, nil
}

func (f *fakeChatClient) SetChatLogPanel(_ context.Context, id string, enabled bool) (*api.ChatLog, error) {
	f.panelID, f.panelEnabled = id, enabled
	return &api.ChatLog{ID: id, PanelEnabled: enabled}, nil
}

func (f *fakeChatClient) DeleteChatLog(_ context.Context, id string) error {
	f.deletedLog = id
	return nil
}

func (f *fakeChatClient) DeleteChatMessage(_ context.Context, id string, seq int32) error {
	f.deletedLog = id
	f.deletedSeqs = append(f.deletedSeqs, seq)
	return nil
}

func (f *fakeChatClient) ListSites(_ context.Context) (*api.SitesResponse, error) {
	return &api.SitesResponse{Sites: f.sites}, nil
}

func chatFactoryOf(c api.ChatClient) func(string, string) api.ChatClient {
	return func(string, string) api.ChatClient { return c }
}

func runChat(t *testing.T, client api.ChatClient, args ...string) (string, error) {
	t.Helper()
	t.Setenv("DROPWAY_TOKEN", "test-token") // auth.Token short-circuits to this
	cmd := newChatCmd(chatFactoryOf(client))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// tempExport writes a transcript file and returns its path.
func tempExport(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---------------------------------------------------------------------------
// chat share
// ---------------------------------------------------------------------------

// TestChatShare_HappyPathWithTrimDisclosure drives share with a --site slug:
// the transcript is sent, the slug resolves to the site's id, and server-side
// trimming (pruned + dropped) is disclosed as "kept the last N of M".
func TestChatShare_HappyPathWithTrimDisclosure(t *testing.T) {
	fc := &fakeChatClient{
		sites: []api.Site{{ID: "site-uuid-1", Slug: "my-site"}},
		createResp: &api.CreateChatLogResponse{
			ChatLog:  api.ChatLog{ID: "chat_42", Title: "Debugging"},
			Appended: 100, Pruned: 20, Window: 80, Dropped: 5,
		},
	}
	file := tempExport(t, "User: hi\nAssistant: hello")

	out, err := runChat(t, fc, "share", file,
		"--site", "my-site", "--title", "Debugging", "--source", "claude_code", "--derive-actions")
	if err != nil {
		t.Fatalf("chat share: %v\n%s", err, out)
	}

	if fc.created == nil {
		t.Fatal("CreateChatLog was not called")
	}
	if fc.created.Title != "Debugging" || fc.created.SourceTool != "claude_code" {
		t.Errorf("created metadata = %+v", fc.created)
	}
	// --site resolved the slug to the site's id.
	if fc.created.SiteID == nil || *fc.created.SiteID != "site-uuid-1" {
		t.Errorf("created site id = %v, want site-uuid-1", fc.created.SiteID)
	}
	if !strings.Contains(fc.created.Transcript, "User: hi") {
		t.Errorf("transcript not sent: %q", fc.created.Transcript)
	}
	if !fc.created.DeriveActions {
		t.Error("--derive-actions not carried into the request")
	}
	if !strings.Contains(out, "chat_42") {
		t.Errorf("output should print the chat id:\n%s", out)
	}
	// Trim disclosure: kept = appended - pruned = 80, total = appended + dropped = 105.
	if !strings.Contains(out, "kept the last 80 of 105") || !strings.Contains(out, "upgrade to keep full history") {
		t.Errorf("output should disclose trimming:\n%s", out)
	}
}

// TestChatShare_NoTrimNoDisclosure proves the disclosure line only appears
// when something was actually trimmed.
func TestChatShare_NoTrimNoDisclosure(t *testing.T) {
	fc := &fakeChatClient{
		createResp: &api.CreateChatLogResponse{ChatLog: api.ChatLog{ID: "chat_1"}, Appended: 3},
	}
	out, err := runChat(t, fc, "share", tempExport(t, "hello"))
	if err != nil {
		t.Fatalf("chat share: %v", err)
	}
	if strings.Contains(out, "kept the last") {
		t.Errorf("no trim happened, output must not disclose one:\n%s", out)
	}
}

// TestChatShare_UnknownSiteErrors proves an unresolvable --site slug fails
// before the log is created.
func TestChatShare_UnknownSiteErrors(t *testing.T) {
	fc := &fakeChatClient{sites: []api.Site{{ID: "s1", Slug: "other-site"}}}
	_, err := runChat(t, fc, "share", tempExport(t, "hi"), "--site", "nope")
	if err == nil || !strings.Contains(err.Error(), "no site with slug") {
		t.Fatalf("err = %v, want an unknown-site error", err)
	}
	if fc.created != nil {
		t.Error("CreateChatLog must not run with an unresolved site")
	}
}

// TestChatShare_QuotaFriendlyMessage proves a 402 quota error surfaces the
// upgrade URL instead of a raw status line.
func TestChatShare_QuotaFriendlyMessage(t *testing.T) {
	fc := &fakeChatClient{createErr: &api.QuotaError{
		Limit: "chat_messages_per_log", Current: 100, Max: 100,
		PlanTier: "pro", NextTier: "business", UpgradeURL: "https://dropway.dev/upgrade",
	}}
	_, err := runChat(t, fc, "share", tempExport(t, "hi"))
	if err == nil {
		t.Fatal("share should fail on a quota error")
	}
	if !strings.Contains(err.Error(), "quota exceeded") || !strings.Contains(err.Error(), "upgrade at https://dropway.dev/upgrade") {
		t.Errorf("err = %v, want a friendly upgrade message", err)
	}
}

// TestChatShare_RejectsBadEnums proves --source/--format typos fail fast,
// before any file read or network call.
func TestChatShare_RejectsBadEnums(t *testing.T) {
	fc := &fakeChatClient{}
	file := tempExport(t, "hi")
	if _, err := runChat(t, fc, "share", file, "--source", "copilot"); err == nil || !strings.Contains(err.Error(), "--source") {
		t.Errorf("bad --source err = %v", err)
	}
	if _, err := runChat(t, fc, "share", file, "--format", "yaml"); err == nil || !strings.Contains(err.Error(), "--format") {
		t.Errorf("bad --format err = %v", err)
	}
	if fc.created != nil {
		t.Error("CreateChatLog must not run with invalid flags")
	}
}

// ---------------------------------------------------------------------------
// chat list
// ---------------------------------------------------------------------------

func TestChatList_Table(t *testing.T) {
	site := "site-uuid-1"
	fc := &fakeChatClient{
		sites: []api.Site{{ID: site, Slug: "my-site"}},
		logs: []api.ChatLog{
			{ID: "chat_1", Title: "Debugging session", SourceTool: "claude_code", MessageCount: 42, SiteID: &site, PanelEnabled: true},
			{ID: "chat_2", Title: "", SourceTool: "", MessageCount: 3},
		},
	}
	out, err := runChat(t, fc, "list")
	if err != nil {
		t.Fatalf("chat list: %v", err)
	}
	for _, want := range []string{"ID", "TITLE", "SOURCE", "MESSAGES", "SITE", "PANEL"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q column header:\n%s", want, out)
		}
	}
	// The attached site renders by slug, the enabled panel as "on".
	if !strings.Contains(out, "my-site") || !strings.Contains(out, "on") {
		t.Errorf("attached row should show the site slug and panel state:\n%s", out)
	}
	// The unattached log shows "-" for its site and "off" for its panel.
	if !strings.Contains(out, "-") || !strings.Contains(out, "off") {
		t.Errorf("unattached row should show - and off:\n%s", out)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("message count missing:\n%s", out)
	}
}

func TestChatList_Empty(t *testing.T) {
	out, err := runChat(t, &fakeChatClient{}, "list")
	if err != nil {
		t.Fatalf("chat list: %v", err)
	}
	if !strings.Contains(out, "No shared chats yet") {
		t.Errorf("empty list should hint at `chat share`:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// chat show
// ---------------------------------------------------------------------------

func TestChatShow_RendersChatAndActionRows(t *testing.T) {
	editMeta, _ := json.Marshal(api.ChatActionMeta{Action: "file_edit", Paths: []string{"src/app.js", "src/util.js"}})
	toolMeta, _ := json.Marshal(api.ChatActionMeta{Action: "tool_use", Tool: "Bash"})
	fc := &fakeChatClient{messages: []api.ChatMessage{
		{Seq: 1, Role: "user", Kind: "chat", Content: "fix the bug"},
		{Seq: 2, Role: "assistant", Kind: "action", Content: "patched the handler", Meta: editMeta},
		{Seq: 3, Role: "assistant", Kind: "action", Content: "ran the tests", Meta: toolMeta},
	}}
	out, err := runChat(t, fc, "show", "chat_1")
	if err != nil {
		t.Fatalf("chat show: %v", err)
	}
	if !strings.Contains(out, "[1] user: fix the bug") {
		t.Errorf("chat row wrong:\n%s", out)
	}
	if !strings.Contains(out, "[2] ✎ src/app.js, src/util.js — patched the handler") {
		t.Errorf("file_edit row wrong:\n%s", out)
	}
	if !strings.Contains(out, "[3] ⚙ Bash — ran the tests") {
		t.Errorf("tool_use row wrong:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// chat append
// ---------------------------------------------------------------------------

// TestChatAppend_WithSite proves --site resolves the slug and appends via the
// site-scoped endpoint with a canonical chat message.
func TestChatAppend_WithSite(t *testing.T) {
	fc := &fakeChatClient{sites: []api.Site{{ID: "site-uuid-1", Slug: "my-site"}}}
	out, err := runChat(t, fc, "append", "--site", "my-site", "--message", "deployed v2", "--role", "assistant")
	if err != nil {
		t.Fatalf("chat append: %v\n%s", err, out)
	}
	if fc.appendedSite != "site-uuid-1" {
		t.Errorf("site append target = %q, want site-uuid-1", fc.appendedSite)
	}
	if fc.appendedID != "" {
		t.Error("log-scoped append must not run when --site is used")
	}
	msgs := fc.appendedReq.Messages
	if len(msgs) != 1 || msgs[0].Kind != "chat" || msgs[0].Role != "assistant" || msgs[0].Content != "deployed v2" {
		t.Errorf("appended payload = %+v", msgs)
	}
	if !strings.Contains(out, "✓ Appended 1 message(s)") {
		t.Errorf("output should confirm the append:\n%s", out)
	}
}

// TestChatAppend_ByIDWithAction proves the chat-id path builds an action
// annotation with meta.
func TestChatAppend_ByIDWithAction(t *testing.T) {
	fc := &fakeChatClient{}
	_, err := runChat(t, fc, "append", "chat_1",
		"--action", "file_edit", "--path", "src/a.js", "--path", "src/b.js", "--comment", "refactored")
	if err != nil {
		t.Fatalf("chat append: %v", err)
	}
	if fc.appendedID != "chat_1" {
		t.Errorf("append target = %q", fc.appendedID)
	}
	msgs := fc.appendedReq.Messages
	if len(msgs) != 1 || msgs[0].Kind != "action" || msgs[0].Role != "assistant" || msgs[0].Content != "refactored" {
		t.Fatalf("appended payload = %+v", msgs)
	}
	meta := msgs[0].Meta
	if meta == nil || meta.Action != "file_edit" || len(meta.Paths) != 2 || meta.Paths[0] != "src/a.js" {
		t.Errorf("action meta = %+v", meta)
	}
}

// TestChatAppend_RequiresExactlyOneTarget proves the chat id and --site are
// mutually exclusive and one is required.
func TestChatAppend_RequiresExactlyOneTarget(t *testing.T) {
	fc := &fakeChatClient{sites: []api.Site{{ID: "s1", Slug: "my-site"}}}
	if _, err := runChat(t, fc, "append", "--message", "hi"); err == nil {
		t.Error("append with no target should error")
	}
	if _, err := runChat(t, fc, "append", "chat_1", "--site", "my-site", "--message", "hi"); err == nil {
		t.Error("append with both a chat id and --site should error")
	}
}

// TestBuildAppendPayload unit-tests the flag → payload rules.
func TestBuildAppendPayload(t *testing.T) {
	// Exactly one of --file/--message/--action.
	if _, err := buildAppendPayload("", "", "user", "", nil, "", ""); err == nil {
		t.Error("no input should error")
	}
	if _, err := buildAppendPayload("", "hi", "user", "tool_use", nil, "Bash", ""); err == nil {
		t.Error("two inputs should error")
	}
	// Role vocabulary is enforced.
	if _, err := buildAppendPayload("", "hi", "system", "", nil, "", ""); err == nil {
		t.Error("bad role should error")
	}
	// tool_use requires --tool; file_edit requires --path.
	if _, err := buildAppendPayload("", "", "user", "tool_use", nil, "", ""); err == nil {
		t.Error("tool_use without --tool should error")
	}
	if _, err := buildAppendPayload("", "", "user", "file_edit", nil, "", ""); err == nil {
		t.Error("file_edit without --path should error")
	}
	if _, err := buildAppendPayload("", "", "user", "sabotage", []string{"a"}, "", ""); err == nil {
		t.Error("unknown action should error")
	}
	// A file import carries the transcript.
	path := filepath.Join(t.TempDir(), "t.txt")
	if err := os.WriteFile(path, []byte("User: hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := buildAppendPayload(path, "", "user", "", nil, "", "")
	if err != nil || p.Transcript != "User: hi" {
		t.Errorf("file payload = %+v, %v", p, err)
	}
}

// ---------------------------------------------------------------------------
// chat attach / detach / panel / delete
// ---------------------------------------------------------------------------

// TestChatAttach_ResolvesSlug proves attach maps the slug to the site id and
// PUTs it as the log's binding.
func TestChatAttach_ResolvesSlug(t *testing.T) {
	fc := &fakeChatClient{sites: []api.Site{{ID: "site-uuid-1", Slug: "my-site"}}}
	out, err := runChat(t, fc, "attach", "chat_1", "--site", "my-site")
	if err != nil {
		t.Fatalf("chat attach: %v", err)
	}
	if fc.setSiteID != "chat_1" || fc.setSiteTarget == nil || *fc.setSiteTarget != "site-uuid-1" {
		t.Errorf("SetChatLogSite(%q, %v)", fc.setSiteID, fc.setSiteTarget)
	}
	if !strings.Contains(out, "✓ Attached chat chat_1 to site my-site") {
		t.Errorf("output:\n%s", out)
	}
}

// TestChatAttach_ConflictSurfaces proves a server-side conflict (the site
// already has a chat log) reaches the user wrapped with the command context.
func TestChatAttach_ConflictSurfaces(t *testing.T) {
	fc := &fakeChatClient{
		sites:      []api.Site{{ID: "site-uuid-1", Slug: "my-site"}},
		setSiteErr: errors.New(`PUT /v1/chats/chat_1/site: server returned 409: {"error":"site already has a chat log"}`),
	}
	_, err := runChat(t, fc, "attach", "chat_1", "--site", "my-site")
	if err == nil {
		t.Fatal("attach should surface the conflict")
	}
	if !strings.Contains(err.Error(), "chat attach:") || !strings.Contains(err.Error(), "site already has a chat log") {
		t.Errorf("err = %v, want the wrapped 409 detail", err)
	}
}

func TestChatDetach_ClearsSite(t *testing.T) {
	fc := &fakeChatClient{}
	out, err := runChat(t, fc, "detach", "chat_1")
	if err != nil {
		t.Fatalf("chat detach: %v", err)
	}
	if fc.setSiteID != "chat_1" || fc.setSiteTarget != nil {
		t.Errorf("SetChatLogSite(%q, %v), want a nil site", fc.setSiteID, fc.setSiteTarget)
	}
	if !strings.Contains(out, "✓ Detached chat chat_1") {
		t.Errorf("output:\n%s", out)
	}
}

func TestChatPanel_Toggles(t *testing.T) {
	fc := &fakeChatClient{}
	out, err := runChat(t, fc, "panel", "chat_1", "--enabled=true")
	if err != nil {
		t.Fatalf("chat panel: %v", err)
	}
	if fc.panelID != "chat_1" || !fc.panelEnabled {
		t.Errorf("SetChatLogPanel(%q, %v)", fc.panelID, fc.panelEnabled)
	}
	if !strings.Contains(out, "panel on") {
		t.Errorf("output:\n%s", out)
	}

	if _, err := runChat(t, fc, "panel", "chat_1", "--enabled=false"); err != nil {
		t.Fatalf("chat panel off: %v", err)
	}
	if fc.panelEnabled {
		t.Error("panel should be disabled")
	}
	// --enabled is required — an implicit default would be a silent surprise.
	if _, err := runChat(t, fc, "panel", "chat_1"); err == nil {
		t.Error("panel without --enabled should error")
	}
}

func TestChatDeleteAndDeleteMessage(t *testing.T) {
	fc := &fakeChatClient{}
	out, err := runChat(t, fc, "delete", "chat_1")
	if err != nil {
		t.Fatalf("chat delete: %v", err)
	}
	if fc.deletedLog != "chat_1" || !strings.Contains(out, "✓ Deleted chat chat_1") {
		t.Errorf("delete: %q\n%s", fc.deletedLog, out)
	}

	out, err = runChat(t, fc, "delete-message", "chat_1", "7")
	if err != nil {
		t.Fatalf("chat delete-message: %v", err)
	}
	if len(fc.deletedSeqs) != 1 || fc.deletedSeqs[0] != 7 || !strings.Contains(out, "message 7") {
		t.Errorf("delete-message: %v\n%s", fc.deletedSeqs, out)
	}

	if _, err := runChat(t, fc, "delete-message", "chat_1", "seven"); err == nil {
		t.Error("a non-numeric seq should error")
	}
}
