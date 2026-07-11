// Package openrouter is the LLM vendor seam for the AI website builder: a
// minimal client for the OpenRouter chat-completions API (an OpenAI-compatible
// gateway over many model providers). The builder's agent loop depends only on
// this package's Client/Event surface, so a different gateway can be dropped in
// behind the same shape without touching the loop.
//
// The API key is configuration, not code: the composition root (the API
// service's main) reads OPENROUTER_API_KEY from the environment and injects it
// here. Self-host deployments bring their own key.
//
// Cost semantics: this client always requests usage accounting
// (usage.include), so OpenRouter's final stream chunk reports usage.cost in
// OpenRouter credits, which are 1:1 with USD. Usage is surfaced on the "done"
// Event so the caller can meter per-generation spend.
package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DefaultBaseURL is the public OpenRouter API root used when Client.BaseURL is
// empty. Overridable so tests point at an httptest server and a self-host can
// front a proxy.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// Client calls the OpenRouter HTTP API. The zero value plus an APIKey is
// usable; all other fields are optional. Safe for concurrent use.
type Client struct {
	// BaseURL is the API root (with or without a trailing slash). Empty means
	// DefaultBaseURL.
	BaseURL string
	// APIKey is sent as "Authorization: Bearer <key>". Required for chat
	// completions; the model catalog works without it.
	APIKey string
	// HTTPClient is the transport. Nil means http.DefaultClient, which has no
	// overall Timeout. That is deliberate: a streamed completion can
	// legitimately run for minutes, so a client-wide Timeout would kill long
	// streams mid-generation. Deadlines belong on the per-request ctx. If you
	// supply your own client, leave Timeout at 0 for the same reason.
	HTTPClient *http.Client
	// AppURL, if set, is sent as the HTTP-Referer header (OpenRouter's app
	// attribution convention; the app shows up on openrouter.ai rankings).
	AppURL string
	// AppTitle, if set, is sent as the X-Title header (attribution display name).
	AppTitle string
}

// Message is one turn in an OpenAI-shaped chat. Assistant turns may carry
// ToolCalls; tool turns answer one call and must set ToolCallID (and
// conventionally Name) to identify the call they answer.
type Message struct {
	Role       string     `json:"role"` // system|user|assistant|tool
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // role=tool
	Name       string     `json:"name,omitempty"`
}

// ToolCall is one function invocation requested by the assistant.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall names the function and carries its arguments as the raw JSON
// string the model produced; callers unmarshal it against their own schema.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON string
}

// Tool declares one callable function to the model.
type Tool struct {
	Type     string   `json:"type"` // "function"
	Function ToolSpec `json:"function"`
}

// ToolSpec describes a function: name, prose description, and a JSON Schema
// for its arguments.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // JSON Schema
}

// ChatRequest is one chat-completion call. The client always sets stream=true
// and usage.include=true on the wire, so those are not fields here.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []Tool
	MaxTokens   int      // omitted when 0
	Temperature *float64 // omitted when nil
}

// chatRequestWire is the JSON actually POSTed: ChatRequest plus the two
// always-on knobs (streaming, and usage accounting so the final chunk carries
// token counts and cost).
type chatRequestWire struct {
	Model       string     `json:"model"`
	Messages    []Message  `json:"messages"`
	Tools       []Tool     `json:"tools,omitempty"`
	MaxTokens   int        `json:"max_tokens,omitempty"`
	Temperature *float64   `json:"temperature,omitempty"`
	Stream      bool       `json:"stream"`
	Usage       usageOptIn `json:"usage"`
}

type usageOptIn struct {
	Include bool `json:"include"`
}

// Usage is the accounting attached to a finished generation.
type Usage struct {
	GenerationID     string // top-level "id" of the completion
	Model            string // the "model" echoed in chunks
	PromptTokens     int64
	CompletionTokens int64
	Cost             float64 // usage.cost: OpenRouter credits (1:1 USD)
}

// Event types delivered on the ChatStream channel.
const (
	EventDelta = "delta"
	EventDone  = "done"
	EventError = "error"
	// EventToolCall is reserved for per-call progress emission. Today assembled
	// tool calls are delivered exactly once, on the "done" event's
	// Message.ToolCalls, so a consumer never risks executing a call twice.
	EventToolCall = "tool_call"
)

// Event is one item on the ChatStream channel.
type Event struct {
	Type string // "delta" | "tool_call" | "done" | "error"
	// Type=="delta": a text token delta.
	Delta string
	// Type=="done": the fully assembled assistant message + usage.
	Message Message
	Usage   *Usage
	// Type=="error": Err carries the terminal error.
	Err error
}

// Model is one catalog entry from GET /models.
type Model struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	ContextLength int64  `json:"context_length,omitempty"`
	Pricing       struct {
		Prompt     string `json:"prompt"` // per-token USD as string
		Completion string `json:"completion"`
	} `json:"pricing"`
}

// Models fetches the OpenRouter model catalog. No auth is required, but the
// API key (when set) is sent anyway so account-scoped catalog views work.
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter: models request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, apiError(resp.StatusCode, body)
	}
	var out struct {
		Data []Model `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openrouter: decode models response: %w", err)
	}
	return out.Data, nil
}

// maxErrorBody caps how much of an error response body is read into memory.
const maxErrorBody = 1 << 20

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// newRequest builds a request against the API root with the auth and
// attribution headers applied.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL()+path, body)
	if err != nil {
		return nil, fmt.Errorf("openrouter: build request: %w", err)
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.AppURL != "" {
		req.Header.Set("HTTP-Referer", c.AppURL)
	}
	if c.AppTitle != "" {
		req.Header.Set("X-Title", c.AppTitle)
	}
	return req, nil
}

// apiError shapes a non-2xx response into an error carrying the HTTP status
// and OpenRouter's error message ({"error":{"message":...,"code":...}}). If
// the body is not that shape, a trimmed snippet of it is included instead.
func apiError(status int, body []byte) error {
	var e struct {
		Error struct {
			Message string          `json:"message"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return fmt.Errorf("openrouter: HTTP %d: %s", status, e.Error.Message)
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 256 {
		msg = msg[:256]
	}
	if msg == "" {
		return fmt.Errorf("openrouter: HTTP %d", status)
	}
	return fmt.Errorf("openrouter: HTTP %d: %s", status, msg)
}
