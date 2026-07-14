// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package chatspec

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		msg     Message
		wantErr bool
	}{
		{"chat ok", Message{Kind: KindChat, Role: RoleUser, Content: "hi"}, false},
		{"assistant ok", Message{Kind: KindChat, Role: RoleAssistant, Content: "hello"}, false},
		{"empty chat", Message{Kind: KindChat, Role: RoleUser, Content: "  "}, true},
		{"unknown kind", Message{Kind: "note", Role: RoleUser, Content: "x"}, true},
		{"unknown role", Message{Kind: KindChat, Role: "system", Content: "x"}, true},
		{"meta on chat", Message{Kind: KindChat, Role: RoleUser, Content: "x", Meta: &ActionMeta{Action: ActionToolUse, Tool: "Bash"}}, true},
		{"action no meta", Message{Kind: KindAction, Role: RoleAssistant, Content: "x"}, true},
		{"action tool_use ok", Message{Kind: KindAction, Role: RoleAssistant, Content: "ran tests", Meta: &ActionMeta{Action: ActionToolUse, Tool: "Bash"}}, false},
		{"action tool_use no tool", Message{Kind: KindAction, Role: RoleAssistant, Content: "x", Meta: &ActionMeta{Action: ActionToolUse}}, true},
		{"action file_edit ok", Message{Kind: KindAction, Role: RoleAssistant, Content: "", Meta: &ActionMeta{Action: ActionFileEdit, Paths: []string{"src/app.tsx"}}}, false},
		{"action file_edit no paths", Message{Kind: KindAction, Role: RoleAssistant, Content: "x", Meta: &ActionMeta{Action: ActionFileEdit}}, true},
		{"action bad path", Message{Kind: KindAction, Role: RoleAssistant, Content: "x", Meta: &ActionMeta{Action: ActionFileEdit, Paths: []string{"../etc/passwd"}}}, true},
		{"action unknown action", Message{Kind: KindAction, Role: RoleAssistant, Content: "x", Meta: &ActionMeta{Action: "run"}}, true},
		{"oversize content", Message{Kind: KindChat, Role: RoleUser, Content: strings.Repeat("a", MaxContentBytes+1)}, true},
		{"invalid utf8", Message{Kind: KindChat, Role: RoleUser, Content: string([]byte{0xff, 0xfe})}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.msg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate(%s) err = %v, wantErr %v", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestValidateMeta_TooManyPaths(t *testing.T) {
	paths := make([]string, MaxActionPaths+1)
	for i := range paths {
		paths[i] = "a/b.txt"
	}
	// Duplicate paths are fine; the count bound is what fires here.
	err := Validate(Message{Kind: KindAction, Role: RoleAssistant, Content: "x",
		Meta: &ActionMeta{Action: ActionFileEdit, Paths: paths}})
	if err == nil {
		t.Fatal("expected path-count error")
	}
}

func TestNormalize_ClaudeCodeJSONL(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"make the chart blue"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Sure, updating it."},{"type":"tool_use","name":"Edit","input":{"file_path":"/src/chart.tsx"}},{"type":"text","text":"Done."}]}}`,
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"npm test"}}]}}`,
	}, "\n")

	msgs, dropped, err := Normalize([]byte(raw), "claude_code", true)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	kinds := make([]string, len(msgs))
	for i, m := range msgs {
		kinds[i] = m.Kind + "/" + m.Role
	}
	want := []string{"chat/user", "chat/assistant", "action/assistant", "chat/assistant", "action/assistant"}
	if len(msgs) != len(want) {
		t.Fatalf("got %d messages (%v), want %d", len(msgs), kinds, len(want))
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("msg[%d] = %s, want %s", i, kinds[i], want[i])
		}
	}
	// The Edit tool_use becomes a file_edit with a cleaned relative path.
	if m := msgs[2]; m.Meta == nil || m.Meta.Action != ActionFileEdit || len(m.Meta.Paths) != 1 || m.Meta.Paths[0] != "src/chart.tsx" {
		t.Errorf("edit action meta = %+v", msgs[2].Meta)
	}
	// The Bash tool_use stays a tool_use carrying the tool name.
	if m := msgs[4]; m.Meta == nil || m.Meta.Action != ActionToolUse || m.Meta.Tool != "Bash" {
		t.Errorf("bash action meta = %+v", msgs[4].Meta)
	}
	// Every derived message must pass Validate (action rows may have empty content).
	for i, m := range msgs {
		if err := Validate(m); err != nil {
			t.Errorf("derived msg[%d] fails Validate: %v", i, err)
		}
	}
}

func TestNormalize_JSONLWithoutDeriveActions(t *testing.T) {
	raw := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"},{"type":"tool_use","name":"Edit","input":{"file_path":"a.txt"}}]}}`
	msgs, _, err := Normalize([]byte(raw), "claude_code", false)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Kind != KindChat {
		t.Fatalf("tool_use should be dropped without deriveActions, got %+v", msgs)
	}
}

func TestNormalize_ChatGPTMessages(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi there"},{"role":"system","content":"secret"}]}`
	msgs, _, err := Normalize([]byte(raw), "chatgpt", false)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (system dropped): %+v", len(msgs), msgs)
	}
}

func TestNormalize_ChatGPTMapping(t *testing.T) {
	raw := `{"mapping":{
		"b":{"message":{"author":{"role":"assistant"},"create_time":2,"content":{"parts":["answer"]}}},
		"a":{"message":{"author":{"role":"user"},"create_time":1,"content":{"parts":["question"]}}},
		"root":{"message":null}
	}}`
	msgs, _, err := Normalize([]byte(raw), "chatgpt", false)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Role != RoleUser || msgs[1].Role != RoleAssistant {
		t.Fatalf("mapping order wrong: %+v", msgs)
	}
}

func TestNormalize_TextSpeakers(t *testing.T) {
	raw := "User: build me a dashboard\nAssistant: here you go\nwith two lines\nHuman: thanks"
	msgs, _, err := Normalize([]byte(raw), "text", false)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(msgs), msgs)
	}
	if msgs[1].Role != RoleAssistant || !strings.Contains(msgs[1].Content, "two lines") {
		t.Errorf("multi-line assistant block wrong: %+v", msgs[1])
	}
}

func TestNormalize_PlainNotes(t *testing.T) {
	msgs, _, err := Normalize([]byte("just some notes about how I made this"), "auto", false)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Role != RoleUser {
		t.Fatalf("plain notes should be one user message: %+v", msgs)
	}
}

func TestNormalize_AutoDetect(t *testing.T) {
	jsonl := "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"a\"}}\n{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":\"b\"}}"
	msgs, _, err := Normalize([]byte(jsonl), "auto", false)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("auto jsonl: %v %+v", err, msgs)
	}
	gpt := `{"messages":[{"role":"user","content":"a"}]}`
	msgs, _, err = Normalize([]byte(gpt), "auto", false)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("auto chatgpt: %v %+v", err, msgs)
	}
}

func TestNormalize_TruncatesOversize(t *testing.T) {
	big := "User: " + strings.Repeat("x", MaxContentBytes+100)
	msgs, _, err := Normalize([]byte(big), "text", false)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(msgs[0].Content) > MaxContentBytes {
		t.Fatalf("content not truncated: %d bytes", len(msgs[0].Content))
	}
	if !strings.HasSuffix(msgs[0].Content, "[truncated]") {
		t.Error("missing truncation marker")
	}
	if err := Validate(msgs[0]); err != nil {
		t.Errorf("truncated message fails Validate: %v", err)
	}
}

func TestNormalize_KeepsNewestPastImportBound(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxImportMessages+50; i++ {
		b.WriteString(`{"type":"user","message":{"role":"user","content":"m`)
		b.WriteString(strings.Repeat("x", i%3)) // vary a little
		b.WriteString(`"}}` + "\n")
	}
	msgs, dropped, err := Normalize([]byte(b.String()), "claude_code", false)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(msgs) != MaxImportMessages || dropped != 50 {
		t.Fatalf("got %d msgs dropped %d, want %d/%d", len(msgs), dropped, MaxImportMessages, 50)
	}
}

func TestNormalize_EmptyAndGarbage(t *testing.T) {
	if _, _, err := Normalize(nil, "auto", false); err == nil {
		t.Error("empty export should error")
	}
	if _, _, err := Normalize([]byte(`{"mapping":{}}`), "chatgpt", false); err == nil {
		t.Error("no-message export should error")
	}
}
