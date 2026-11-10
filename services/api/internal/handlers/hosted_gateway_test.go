package handlers

import (
	"strings"
	"testing"
)

func TestGatewayRequiresAuthoritativeUsage(t *testing.T) {
	cases := []struct {
		name, provider, body string
		stream, valid        bool
		tokens               int64
	}{{"anthropic", "anthropic", `{"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":3}}`, false, true, 15}, {"stream", "anthropic", "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":6}}\n\n", true, true, 10}, {"missing", "anthropic", `{"content":[]}`, false, false, 0}, {"router", "openrouter", `{"usage":{"prompt_tokens":8,"completion_tokens":3}}`, false, true, 11}, {"negative", "openrouter", `{"usage":{"prompt_tokens":-8,"completion_tokens":3}}`, false, false, 3}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, ok := gatewayUsage([]byte(c.body), c.provider, c.stream)
			if ok != c.valid || (ok && n != c.tokens) {
				t.Fatalf("usage %d %v", n, ok)
			}
		})
	}
	if !hasRemoteModelInput(map[string]any{"messages": []any{map[string]any{"type": "image", "source": map[string]any{"url": "http://private"}}}}) {
		t.Fatal("remote model content allowed")
	}
	if hasRemoteModelInput(map[string]any{"messages": []any{map[string]any{"content": "https://example.com ordinary text"}}}) {
		t.Fatal("text rejected")
	}
	_ = strings.TrimSpace
}
