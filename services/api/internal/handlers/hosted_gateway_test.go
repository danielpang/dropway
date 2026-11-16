package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
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

func TestOpenAIGatewayPinsUpstreamAndAccounting(t *testing.T) {
	request := map[string]json.RawMessage{
		"model": json.RawMessage(`"gpt-4.1"`), "messages": json.RawMessage(`[]`), "stream": json.RawMessage(`true`),
		"max_tokens": json.RawMessage(`999999`), "max_completion_tokens": json.RawMessage(`999999`),
		"store": json.RawMessage(`true`), "provider": json.RawMessage(`{"allow_fallbacks":true}`),
		"stream_options": json.RawMessage(`{"include_usage":false}`), "system": json.RawMessage(`"unsupported"`),
	}
	endpoint, payload, err := gatewayPayload("openai", request, 512)
	if err != nil || endpoint != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("upstream %s %v", endpoint, err)
	}
	for key, want := range map[string]string{"max_tokens": "512", "store": "false", "stream_options": `{"include_usage":true}`} {
		if string(payload[key]) != want {
			t.Fatalf("%s: %s", key, payload[key])
		}
	}
	for _, key := range []string{"provider", "max_completion_tokens", "system"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("unsafe field %s retained", key)
		}
	}
	if _, _, err := gatewayPayload("unknown", request, 512); err == nil {
		t.Fatal("unknown provider accepted")
	}
	req, _ := http.NewRequest(http.MethodPost, endpoint, nil)
	setProviderHeaders(req, "openai", "sk-proj-private")
	if req.Header.Get("Authorization") != "Bearer sk-proj-private" || req.Header.Get("x-api-key") != "" {
		t.Fatal("invalid provider authentication")
	}
	body := []byte("data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":null}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":4,\"total_tokens\":16}}\n\ndata: [DONE]\n\n")
	if used, ok := gatewayUsage(body, "openai", true); !ok || used != 16 {
		t.Fatalf("usage %d %v", used, ok)
	}
	if _, ok := gatewayUsage([]byte(`{"choices":[],"usage":null}`), "openai", false); ok {
		t.Fatal("missing usage accepted")
	}
}

func TestOpenAIGatewayRejectsWrongPaths(t *testing.T) {
	good := "/internal/room-gateway/attempt/v1/chat/completions"
	if !gatewayPathAllowed("openai", "attempt", good, "") {
		t.Fatal("valid path denied")
	}
	for _, path := range []string{"/v1/chat/completions", "/internal/room-gateway/other/v1/chat/completions", "/internal/room-gateway/attempt/v1/messages", "/internal/room-gateway/attempt/v1/responses"} {
		if gatewayPathAllowed("openai", "attempt", path, "") {
			t.Fatalf("path accepted: %s", path)
		}
	}
	if gatewayPathAllowed("openai", "attempt", good, "beta=true") {
		t.Fatal("unexpected query accepted")
	}
	if !gatewayPathAllowed("anthropic", "attempt", "/internal/room-gateway/attempt/v1/messages", "beta=true") {
		t.Fatal("Anthropic SDK beta query denied")
	}
}

func TestProviderFailuresAreNotUsageFailures(t *testing.T) {
	for status, want := range map[int]string{404: "provider_model_unavailable", 401: "provider_authentication", 403: "provider_authentication", 429: "provider_rate_limited", 400: "provider_rejected", 422: "provider_rejected", 500: "provider_unavailable", 200: "provider_response_invalid"} {
		if got := providerFailureCode(status); got != want {
			t.Fatalf("status %d: %s", status, got)
		}
	}
}

func TestGatewayAcceptsOnlyBoundedInlineImagePreviews(t *testing.T) {
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 32, 24)), nil); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(data.Bytes())
	anthropic := map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": encoded}}
	openai := map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64," + encoded}}
	for _, content := range []map[string]any{anthropic, openai} {
		if hasRemoteModelInput(content) {
			t.Fatal("bounded inline preview rejected")
		}
		size, count := gatewayImageBytes(map[string]any{"messages": []any{content}})
		if size != len(encoded) || count != 1 {
			t.Fatalf("wrong visual accounting %d/%d", size, count)
		}
	}
	cases := []map[string]any{
		{"type": "image_url", "image_url": map[string]any{"url": "http://169.254.169.254/secret"}},
		{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": "not an image"}},
		{"type": "image", "source": anthropic["source"], "file_id": "secret"},
		{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64," + encoded, "base_url": "http://private"}},
	}
	data.Reset()
	_ = jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 513, 10)), nil)
	cases = append(cases, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(data.Bytes())}})
	for _, content := range cases {
		if !hasRemoteModelInput(content) {
			t.Fatal("unsafe image accepted")
		}
	}
}
