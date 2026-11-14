// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danielpang/dropway/internal/httpx"
	"github.com/danielpang/dropway/services/api/internal/hosted"
	"github.com/go-chi/chi/v5"
)

// HostedGateway is the only process that sees the decrypted provider key. It
// reserves a conservative token ceiling before upstream I/O, polls revocation,
// and verifies usage before releasing completion/tool-call bytes to the worker.
// SSE is preserved, but buffered (bounded) until accounting is committed.
func (a *API) HostedGateway(w http.ResponseWriter, r *http.Request) {
	if !a.hostedReady(w) {
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = r.Header.Get("x-api-key")
	}
	_, id, err := hosted.ParseToken(token, "gateway")
	if err != nil || id != chi.URLParam(r, "attemptID") {
		writeHostedError(w, hosted.ErrDenied)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 512<<10))
	if err != nil {
		writeHostedError(w, hosted.ErrInvalid)
		return
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(raw, &request); err != nil {
		writeHostedError(w, hosted.ErrInvalid)
		return
	}
	// Only text/tool schemas cross this gateway. Provider-hosted tools, remote
	// media URLs, files, arbitrary base URLs, and routing fallback are unavailable.
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil || hasRemoteModelInput(generic) {
		writeHostedError(w, hosted.ErrInvalid)
		return
	}
	var model string
	_ = json.Unmarshal(request["model"], &model)
	var maxTokens int64
	_ = json.Unmarshal(request["max_tokens"], &maxTokens)
	grant, err := a.Hosted.Store.ReserveGatewayCall(r.Context(), token, int64(len(raw)), maxTokens, a.Hosted.Vault)
	if err != nil {
		writeHostedError(w, err)
		return
	}
	if model != grant.Attempt.Job.Config.ModelID || !gatewayPathAllowed(grant.Attempt.Job.Config.Provider, id, r.URL.Path, r.URL.RawQuery) {
		_ = a.Hosted.Store.SettleGatewayCall(r.Context(), token, 0, true)
		writeHostedError(w, hosted.ErrInvalid)
		return
	}
	provider := grant.Attempt.Job.Config.Provider
	endpoint, filtered, err := gatewayPayload(provider, request, grant.MaxOutputTokens)
	if err != nil {
		_ = a.Hosted.Store.SettleGatewayCall(r.Context(), token, 0, true)
		writeHostedError(w, err)
		return
	}
	payload, _ := json.Marshal(filtered)
	ctx, cancel := context.WithDeadline(r.Context(), grant.Attempt.Deadline)
	defer cancel()
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := a.Hosted.Store.CheckGatewayCall(ctx, token); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	setProviderHeaders(req, provider, grant.Secret)
	response, err := a.Hosted.HTTPClient.Do(req)
	if err != nil {
		_ = a.Hosted.Store.SettleGatewayCall(context.WithoutCancel(r.Context()), token, 0, false)
		httpx.WriteJSON(w, http.StatusBadGateway, httpx.ErrorBody{Error: "provider_unavailable", Message: "model request failed"})
		return
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if response.StatusCode != http.StatusOK || readErr != nil || len(body) > 4<<20 {
		_ = a.Hosted.Store.SettleGatewayCall(context.WithoutCancel(r.Context()), token, 0, false)
		httpx.WriteJSON(w, http.StatusBadGateway, httpx.ErrorBody{Error: "provider_failed", Message: "model request failed"})
		return
	}
	used, valid := gatewayUsage(body, provider, strings.Contains(response.Header.Get("Content-Type"), "text/event-stream"))
	valid = valid && used <= grant.Reserved
	if err := a.Hosted.Store.SettleGatewayCall(r.Context(), token, used, valid); err != nil {
		writeHostedError(w, err)
		return
	}
	if !valid {
		httpx.WriteJSON(w, http.StatusBadGateway, httpx.ErrorBody{Error: "usage_unverified", Message: "provider usage could not be verified"})
		return
	}
	// Even a hostile provider error/content cannot reflect the credential into
	// worker tools, events, transcripts, or candidate artifacts.
	body = bytes.ReplaceAll(body, []byte(grant.Secret), []byte("[redacted]"))
	grant.Secret = ""
	contentType := "application/json"
	if strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		contentType = "text/event-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func gatewayPathAllowed(provider, attemptID, path, query string) bool {
	base := "/internal/room-gateway/" + attemptID
	switch provider {
	case "anthropic":
		return path == base+"/v1/messages" && (query == "" || query == "beta=true")
	case "openai", "openrouter":
		return path == base+"/v1/chat/completions" && query == ""
	default:
		return false
	}
}

// gatewayPayload selects an exact upstream and reconstructs supported fields.
// Caller-controlled routing, persistence, and token bounds never pass through.
func gatewayPayload(provider string, request map[string]json.RawMessage, maxOutput int64) (string, map[string]json.RawMessage, error) {
	endpoint := ""
	switch provider {
	case "anthropic":
		endpoint = "https://api.anthropic.com/v1/messages"
	case "openrouter":
		endpoint = "https://openrouter.ai/api/v1/chat/completions"
	case "openai":
		endpoint = "https://api.openai.com/v1/chat/completions"
	default:
		return "", nil, hosted.ErrInvalid
	}
	filtered := map[string]json.RawMessage{}
	for _, key := range []string{"model", "messages", "tools", "tool_choice", "temperature", "top_p", "stream"} {
		if value, ok := request[key]; ok {
			filtered[key] = value
		}
	}
	if provider == "anthropic" {
		if value, ok := request["system"]; ok {
			filtered["system"] = value
		}
	}
	filtered["max_tokens"], _ = json.Marshal(maxOutput)
	if provider == "openrouter" {
		filtered["provider"] = json.RawMessage(`{"allow_fallbacks":false}`)
	}
	if provider == "openai" {
		filtered["store"] = json.RawMessage(`false`)
	}
	if provider != "anthropic" {
		var stream bool
		_ = json.Unmarshal(filtered["stream"], &stream)
		if stream {
			filtered["stream_options"] = json.RawMessage(`{"include_usage":true}`)
		}
	}
	return endpoint, filtered, nil
}

func hasRemoteModelInput(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			switch key {
			case "image_url", "audio_url", "file_id", "mcp_servers", "container", "context_management", "base_url":
				return true
			}
			if key == "type" {
				if kind, ok := child.(string); ok && (kind == "image" || kind == "document" || kind == "input_audio" || strings.HasPrefix(kind, "web_search") || strings.HasPrefix(kind, "computer")) {
					return true
				}
			}
			if hasRemoteModelInput(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if hasRemoteModelInput(child) {
				return true
			}
		}
	}
	return false
}
func gatewayUsage(body []byte, provider string, stream bool) (int64, bool) {
	var input, output, cache int64
	var haveInput, haveOutput bool
	note := func(raw json.RawMessage) {
		var usage map[string]json.RawMessage
		if json.Unmarshal(raw, &usage) != nil {
			return
		}
		read := func(key string) (int64, bool) {
			var n int64
			raw, ok := usage[key]
			if !ok || json.Unmarshal(raw, &n) != nil || n < 0 {
				return 0, false
			}
			return n, true
		}
		if provider == "anthropic" {
			if n, ok := read("input_tokens"); ok {
				input = n
				haveInput = true
			}
			if n, ok := read("output_tokens"); ok {
				output = n
				haveOutput = true
			}
			var c int64
			for _, key := range []string{"cache_read_input_tokens", "cache_creation_input_tokens"} {
				n, _ := read(key)
				c += n
			}
			if c > cache {
				cache = c
			}
		} else {
			if n, ok := read("prompt_tokens"); ok {
				input = n
				haveInput = true
			}
			if n, ok := read("completion_tokens"); ok {
				output = n
				haveOutput = true
			}
		}
	}
	event := func(raw []byte) {
		var v struct {
			Usage   json.RawMessage `json:"usage"`
			Message struct {
				Usage json.RawMessage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &v) != nil {
			return
		}
		note(v.Usage)
		note(v.Message.Usage)
	}
	if stream {
		for _, line := range bytes.Split(body, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data:")) {
				event(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:"))))
			}
		}
	} else {
		event(body)
	}
	total := input + output + cache
	if total < input || total < output || total < cache {
		return 0, false
	}
	return total, haveInput && haveOutput
}
