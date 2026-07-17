// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package chatspec is the shared validation vocabulary for shared chat logs:
// what makes a message appendable (bounded content, known kind/role, sane
// action metadata) and the tolerant import normalizer that turns a pasted
// export — Claude Code JSONL, a ChatGPT JSON export, or plain text — into
// the canonical message list the API stores one row per message.
//
// The API enforces these rules on every append (single or batch import).
// Clients (dashboard, CLI, MCP) pre-check cheaply for better errors, but the
// API is the boundary.
package chatspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// MaxContentBytes bounds one message's content. It is validation on every
	// tier, not a plan lever: it keeps any single append bounded so a whole
	// free/pro log stays under window/cap × 64 KiB.
	MaxContentBytes = 64 << 10 // 64 KiB
	// MaxActionPaths bounds how many file paths one action annotation may
	// reference.
	MaxActionPaths = 20
	// MaxToolNameLen bounds an action annotation's tool name.
	MaxToolNameLen = 128
	// MaxImportMessages bounds one batch import. Longer exports keep their
	// NEWEST messages (the tier window/cap prunes further); the importer
	// reports what was dropped.
	MaxImportMessages = 1000
	// MaxTitleLen bounds a chat log's title.
	MaxTitleLen = 200
)

// Message kinds: a conversation turn, or an LLM-authored annotation about
// work performed (a tool run or file edit) whose content is the model's
// commentary and whose Meta carries the structured facts.
const (
	KindChat   = "chat"
	KindAction = "action"
)

// Roles a stored message may carry. Imports flatten everything else
// (system/tool noise) rather than storing it.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Action types an annotation may describe.
const (
	ActionToolUse  = "tool_use"
	ActionFileEdit = "file_edit"
)

// Source tools a log can record (display metadata, not an enum the API
// rejects on — new tools appear faster than releases).
const (
	SourceClaudeCode = "claude_code"
	SourceChatGPT    = "chatgpt"
	SourceCursor     = "cursor"
	SourceOther      = "other"
)

// ActionMeta is the structured half of a kind="action" message. Stored as
// jsonb; rendered by the viewer as icon + target chips.
type ActionMeta struct {
	// Action is ActionToolUse or ActionFileEdit.
	Action string `json:"action"`
	// Tool names the tool invoked (required for tool_use).
	Tool string `json:"tool,omitempty"`
	// Paths lists the files touched (clean relative paths, ≤ MaxActionPaths).
	Paths []string `json:"paths,omitempty"`
}

// Message is one canonical chat-log entry.
type Message struct {
	Kind    string      `json:"kind"`
	Role    string      `json:"role"`
	Content string      `json:"content"`
	Meta    *ActionMeta `json:"meta,omitempty"`
}

// Validate asserts m is storable. The returned error is client-safe
// (surfaced as a 400).
func Validate(m Message) error {
	switch m.Kind {
	case KindChat:
		if m.Meta != nil {
			return errors.New("chatspec: meta is only valid on kind \"action\"")
		}
	case KindAction:
		if m.Meta == nil {
			return errors.New("chatspec: kind \"action\" requires meta")
		}
		if err := validateMeta(*m.Meta); err != nil {
			return err
		}
	default:
		return fmt.Errorf("chatspec: unknown kind %q", m.Kind)
	}
	switch m.Role {
	case RoleUser, RoleAssistant:
	default:
		return fmt.Errorf("chatspec: unknown role %q", m.Role)
	}
	if strings.TrimSpace(m.Content) == "" && m.Kind == KindChat {
		return errors.New("chatspec: empty message")
	}
	if len(m.Content) > MaxContentBytes {
		return fmt.Errorf("chatspec: content %d bytes exceeds the %d-byte limit", len(m.Content), MaxContentBytes)
	}
	if !utf8.ValidString(m.Content) {
		return errors.New("chatspec: content is not valid UTF-8")
	}
	return nil
}

func validateMeta(meta ActionMeta) error {
	switch meta.Action {
	case ActionToolUse:
		if strings.TrimSpace(meta.Tool) == "" {
			return errors.New("chatspec: tool_use action requires a tool name")
		}
	case ActionFileEdit:
		if len(meta.Paths) == 0 {
			return errors.New("chatspec: file_edit action requires at least one path")
		}
	default:
		return fmt.Errorf("chatspec: unknown action %q", meta.Action)
	}
	if len(meta.Tool) > MaxToolNameLen {
		return fmt.Errorf("chatspec: tool name exceeds %d characters", MaxToolNameLen)
	}
	if len(meta.Paths) > MaxActionPaths {
		return fmt.Errorf("chatspec: %d paths exceeds the %d-path limit", len(meta.Paths), MaxActionPaths)
	}
	for _, p := range meta.Paths {
		if !CleanPath(p) {
			return fmt.Errorf("chatspec: invalid path %q (must be a clean relative path)", p)
		}
	}
	return nil
}

// CleanPath reports whether p is a safe repo-relative POSIX path: non-empty,
// relative, forward slashes only, no empty/"."/".." segments, no NUL. Same
// rules as skill file paths — annotation paths are rendered as text chips,
// never resolved, but a clean vocabulary keeps the UI honest.
func CleanPath(p string) bool {
	if p == "" || len(p) > 512 || strings.ContainsAny(p, "\x00\\") {
		return false
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "//") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// Normalize parses a raw conversation export into canonical messages.
//
// format is one of "auto", "claude_code" (JSONL), "chatgpt" (JSON export),
// or "text". deriveActions condenses tool activity found in an export into
// kind="action" rows instead of dropping it. Content longer than
// MaxContentBytes is truncated (an import should degrade, not fail); exports
// longer than MaxImportMessages keep their newest entries. The returned
// dropped count reports messages discarded by that bound so the importer can
// disclose it.
func Normalize(raw []byte, format string, deriveActions bool) (msgs []Message, dropped int, err error) {
	if len(raw) == 0 {
		return nil, 0, errors.New("chatspec: empty export")
	}
	if format == "" || format == "auto" {
		format = detectFormat(raw)
	}
	switch format {
	case "claude_code", "jsonl":
		msgs, err = normalizeJSONL(raw, deriveActions)
	case "chatgpt", "json":
		msgs, err = normalizeChatGPT(raw)
	case "text", "markdown":
		msgs, err = normalizeText(string(raw)), nil
	default:
		return nil, 0, fmt.Errorf("chatspec: unknown format %q", format)
	}
	if err != nil {
		return nil, 0, err
	}
	if len(msgs) == 0 {
		return nil, 0, errors.New("chatspec: export contained no messages")
	}
	for i := range msgs {
		msgs[i].Content = truncate(msgs[i].Content)
	}
	if len(msgs) > MaxImportMessages {
		dropped = len(msgs) - MaxImportMessages
		msgs = msgs[dropped:]
	}
	return msgs, dropped, nil
}

// detectFormat guesses the export shape: a JSON document with ChatGPT
// markers, JSONL (every non-blank line its own JSON object), else text.
func detectFormat(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		// One JSON document → ChatGPT-style export; many → JSONL.
		if json.Valid([]byte(trimmed)) {
			return "chatgpt"
		}
	}
	lines := nonBlankLines(trimmed)
	if len(lines) > 0 {
		allJSON := true
		for _, l := range lines {
			if !json.Valid([]byte(l)) {
				allJSON = false
				break
			}
		}
		if allJSON {
			return "claude_code"
		}
	}
	return "text"
}

func nonBlankLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

// jsonlEvent is the tolerant subset of a Claude Code / agent JSONL line we
// read. Content is either a plain string or a list of typed parts.
type jsonlEvent struct {
	Type    string `json:"type"`
	Message *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentPart struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func normalizeJSONL(raw []byte, deriveActions bool) ([]Message, error) {
	var msgs []Message
	for _, line := range nonBlankLines(string(raw)) {
		var ev jsonlEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // tolerate meta lines we don't understand
		}
		role, content := ev.Role, ev.Content
		if ev.Message != nil {
			role, content = ev.Message.Role, ev.Message.Content
		}
		role = canonicalRole(role, ev.Type)
		if role == "" || len(content) == 0 {
			continue
		}
		// content: plain string…
		var text string
		if err := json.Unmarshal(content, &text); err == nil {
			if strings.TrimSpace(text) != "" {
				msgs = append(msgs, Message{Kind: KindChat, Role: role, Content: text})
			}
			continue
		}
		// …or a list of typed parts.
		var parts []contentPart
		if err := json.Unmarshal(content, &parts); err != nil {
			continue
		}
		var buf strings.Builder
		for _, p := range parts {
			switch p.Type {
			case "text":
				if strings.TrimSpace(p.Text) != "" {
					if buf.Len() > 0 {
						buf.WriteString("\n\n")
					}
					buf.WriteString(p.Text)
				}
			case "tool_use":
				if !deriveActions || role != RoleAssistant {
					continue
				}
				if buf.Len() > 0 { // flush preceding prose so order is kept
					msgs = append(msgs, Message{Kind: KindChat, Role: role, Content: buf.String()})
					buf.Reset()
				}
				msgs = append(msgs, deriveAction(p))
			}
			// tool_result and unknown parts are noise: flattened, never stored.
		}
		if buf.Len() > 0 {
			msgs = append(msgs, Message{Kind: KindChat, Role: role, Content: buf.String()})
		}
	}
	return msgs, nil
}

// deriveAction condenses a tool_use content part into an action annotation.
// Edit-shaped tools with a file path become file_edit; everything else is
// tool_use. Derived rows carry no commentary — the good comments come from
// a live agent appending its own.
func deriveAction(p contentPart) Message {
	meta := &ActionMeta{Action: ActionToolUse, Tool: p.Name}
	if len(meta.Tool) > MaxToolNameLen {
		meta.Tool = meta.Tool[:MaxToolNameLen]
	}
	var input struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	}
	_ = json.Unmarshal(p.Input, &input)
	path := input.FilePath
	if path == "" {
		path = input.Path
	}
	if path != "" {
		path = strings.TrimPrefix(path, "/")
		if CleanPath(path) {
			meta.Paths = []string{path}
			switch p.Name {
			case "Edit", "Write", "MultiEdit", "NotebookEdit", "str_replace_editor", "create_file":
				meta.Action = ActionFileEdit
			}
		}
	}
	if meta.Action == ActionFileEdit {
		meta.Tool = "" // the paths tell the story; tool name is noise here
	}
	return Message{Kind: KindAction, Role: RoleAssistant, Meta: meta}
}

// chatgptExport covers the two shapes ChatGPT exports arrive in: a simple
// {"messages": […]} document, or the full conversation export with a
// "mapping" of nodes ordered by create_time.
type chatgptExport struct {
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Mapping map[string]struct {
		Message *struct {
			Author struct {
				Role string `json:"role"`
			} `json:"author"`
			CreateTime float64 `json:"create_time"`
			Content    struct {
				Parts []json.RawMessage `json:"parts"`
			} `json:"content"`
		} `json:"message"`
	} `json:"mapping"`
}

func normalizeChatGPT(raw []byte) ([]Message, error) {
	var doc chatgptExport
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("chatspec: not a recognizable JSON export: %w", err)
	}
	var msgs []Message
	for _, m := range doc.Messages {
		role := canonicalRole(m.Role, "")
		if role == "" {
			continue
		}
		var text string
		if err := json.Unmarshal(m.Content, &text); err != nil {
			continue
		}
		if strings.TrimSpace(text) != "" {
			msgs = append(msgs, Message{Kind: KindChat, Role: role, Content: text})
		}
	}
	if len(msgs) > 0 {
		return msgs, nil
	}
	type timed struct {
		at   float64
		role string
		text string
	}
	var nodes []timed
	for _, n := range doc.Mapping {
		if n.Message == nil {
			continue
		}
		role := canonicalRole(n.Message.Author.Role, "")
		if role == "" {
			continue
		}
		var buf strings.Builder
		for _, part := range n.Message.Content.Parts {
			var s string
			if err := json.Unmarshal(part, &s); err != nil {
				continue // multimodal parts are objects; skip them
			}
			if strings.TrimSpace(s) == "" {
				continue
			}
			if buf.Len() > 0 {
				buf.WriteString("\n\n")
			}
			buf.WriteString(s)
		}
		if buf.Len() > 0 {
			nodes = append(nodes, timed{at: n.Message.CreateTime, role: role, text: buf.String()})
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].at < nodes[j].at })
	for _, n := range nodes {
		msgs = append(msgs, Message{Kind: KindChat, Role: n.role, Content: n.text})
	}
	return msgs, nil
}

// normalizeText splits a plain-text/markdown conversation on speaker markers
// ("User:", "Assistant:", "Human:", "AI:", "Claude:", case-insensitive, at
// line start). Text with no markers becomes a single user message — manual
// notes are still a story worth attaching.
func normalizeText(s string) []Message {
	var msgs []Message
	role := RoleUser
	var buf strings.Builder
	flush := func() {
		if strings.TrimSpace(buf.String()) != "" {
			msgs = append(msgs, Message{Kind: KindChat, Role: role, Content: strings.TrimSpace(buf.String())})
		}
		buf.Reset()
	}
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if r, rest, ok := speakerMarker(line); ok {
			flush()
			role = r
			buf.WriteString(rest)
			continue
		}
		if buf.Len() > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(line)
	}
	flush()
	return msgs
}

func speakerMarker(line string) (role, rest string, ok bool) {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	for prefix, r := range map[string]string{
		"user:": RoleUser, "human:": RoleUser, "me:": RoleUser,
		"assistant:": RoleAssistant, "ai:": RoleAssistant, "claude:": RoleAssistant, "chatgpt:": RoleAssistant,
	} {
		if strings.HasPrefix(lower, prefix) {
			return r, strings.TrimSpace(trimmed[len(prefix):]), true
		}
	}
	return "", "", false
}

// canonicalRole maps export roles onto the two stored roles; system/tool
// noise maps to "" (dropped).
func canonicalRole(role, eventType string) string {
	switch strings.ToLower(role) {
	case RoleUser, "human":
		return RoleUser
	case RoleAssistant, "model", "ai":
		return RoleAssistant
	}
	switch eventType {
	case "user":
		return RoleUser
	case "assistant":
		return RoleAssistant
	}
	return ""
}

// truncate bounds content at MaxContentBytes on a rune boundary, marking the
// cut. Imports degrade rather than fail; direct appends go through Validate,
// which rejects oversize instead.
func truncate(s string) string {
	if len(s) <= MaxContentBytes {
		return s
	}
	cut := MaxContentBytes - len("\n\n[truncated]")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n\n[truncated]"
}
