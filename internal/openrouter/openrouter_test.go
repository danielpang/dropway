package openrouter

// Tests exercise the client against httptest servers that script OpenRouter's
// SSE wire format: comment/keepalive lines, split tool-call fragments, the
// usage-bearing final chunk, [DONE], and the error body shape.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseHandler asserts the request shape (path, headers, always-on stream/usage
// flags) and then streams the given SSE lines, flushing after each.
func sseHandler(t *testing.T, lines []string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q", got)
		}
		var wire struct {
			Stream bool `json:"stream"`
			Usage  struct {
				Include bool `json:"include"`
			} `json:"usage"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if !wire.Stream || !wire.Usage.Include {
			t.Errorf("wire request: stream=%v usage.include=%v, want both true", wire.Stream, wire.Usage.Include)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, l := range lines {
			_, _ = w.Write([]byte(l + "\n\n"))
			flusher.Flush()
		}
	}
}

// collect drains the channel with a watchdog so a stuck stream fails the test
// instead of hanging it.
func collect(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var evs []Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return evs
			}
			evs = append(evs, ev)
		case <-timeout:
			t.Fatalf("timed out draining stream; got %d events so far", len(evs))
		}
	}
}

func TestChatStreamHappyPath(t *testing.T) {
	srv := httptest.NewServer(sseHandler(t, []string{
		`: OPENROUTER PROCESSING`,
		`data: {"id":"gen-abc","model":"test/model-1","choices":[{"delta":{"content":"Hel"},"finish_reason":null}]}`,
		`data: {"id":"gen-abc","model":"test/model-1","choices":[{"delta":{"content":"lo"},"finish_reason":null}]}`,
		`data: {"id":"gen-abc","model":"test/model-1","choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
		`data: {"id":"gen-abc","model":"test/model-1","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"cost":0.00042}}`,
		`data: [DONE]`,
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, APIKey: "test-key"}
	ch, err := c.ChatStream(context.Background(), ChatRequest{
		Model:    "test/model-1",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	evs := collect(t, ch)

	wantDeltas := []string{"Hel", "lo", " world"}
	var deltas []string
	for _, ev := range evs[:len(evs)-1] {
		if ev.Type != EventDelta {
			t.Fatalf("event %+v before done, want only deltas", ev)
		}
		deltas = append(deltas, ev.Delta)
	}
	if len(deltas) != len(wantDeltas) {
		t.Fatalf("deltas = %q, want %q", deltas, wantDeltas)
	}
	for i := range wantDeltas {
		if deltas[i] != wantDeltas[i] {
			t.Errorf("delta[%d] = %q, want %q", i, deltas[i], wantDeltas[i])
		}
	}

	done := evs[len(evs)-1]
	if done.Type != EventDone {
		t.Fatalf("last event = %+v, want done", done)
	}
	if done.Message.Role != "assistant" || done.Message.Content != "Hello world" {
		t.Errorf("done message = %+v", done.Message)
	}
	if len(done.Message.ToolCalls) != 0 {
		t.Errorf("unexpected tool calls: %+v", done.Message.ToolCalls)
	}
	if done.Usage == nil {
		t.Fatal("done event has no usage")
	}
	if done.Usage.GenerationID != "gen-abc" || done.Usage.Model != "test/model-1" {
		t.Errorf("usage id/model = %q/%q", done.Usage.GenerationID, done.Usage.Model)
	}
	if done.Usage.PromptTokens != 10 || done.Usage.CompletionTokens != 3 || done.Usage.Cost != 0.00042 {
		t.Errorf("usage = %+v", done.Usage)
	}
}

func TestChatStreamAssemblesToolCalls(t *testing.T) {
	srv := httptest.NewServer(sseHandler(t, []string{
		// Tool call 0: arguments split across 3 chunks; id/name only on the first.
		`data: {"id":"gen-t","model":"m","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"write_file","arguments":"{\"path\""}}]},"finish_reason":null}]}`,
		`data: {"id":"gen-t","model":"m","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"index.html\","}}]},"finish_reason":null}]}`,
		`data: {"id":"gen-t","model":"m","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"content\":\"<h1>hi</h1>\"}"}}]},"finish_reason":null}]}`,
		// Tool call 1 in its own chunks.
		`data: {"id":"gen-t","model":"m","choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"deploy","arguments":"{\"site\""}}]},"finish_reason":null}]}`,
		`data: {"id":"gen-t","model":"m","choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":":\"demo\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"gen-t","model":"m","choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":50,"completion_tokens":20,"cost":0.001}}`,
		`data: [DONE]`,
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, APIKey: "test-key"}
	ch, err := c.ChatStream(context.Background(), ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	evs := collect(t, ch)
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	done := evs[len(evs)-1]
	if done.Type != EventDone {
		t.Fatalf("last event = %+v, want done", done)
	}
	calls := done.Message.ToolCalls
	if len(calls) != 2 {
		t.Fatalf("tool calls = %+v, want 2", calls)
	}
	if calls[0].ID != "call_1" || calls[0].Type != "function" || calls[0].Function.Name != "write_file" {
		t.Errorf("call 0 = %+v", calls[0])
	}
	if want := `{"path":"index.html","content":"<h1>hi</h1>"}`; calls[0].Function.Arguments != want {
		t.Errorf("call 0 arguments = %q, want %q", calls[0].Function.Arguments, want)
	}
	if calls[1].ID != "call_2" || calls[1].Function.Name != "deploy" {
		t.Errorf("call 1 = %+v", calls[1])
	}
	if want := `{"site":"demo"}`; calls[1].Function.Arguments != want {
		t.Errorf("call 1 arguments = %q, want %q", calls[1].Function.Arguments, want)
	}
	if done.Usage == nil || done.Usage.Cost != 0.001 {
		t.Errorf("usage = %+v", done.Usage)
	}
}

func TestChatStreamHTTPError(t *testing.T) {
	cases := []struct {
		status  int
		body    string
		wantSub []string
	}{
		{http.StatusPaymentRequired, `{"error":{"message":"Insufficient credits","code":402}}`, []string{"402", "Insufficient credits"}},
		{http.StatusTooManyRequests, `{"error":{"message":"Rate limit exceeded","code":429}}`, []string{"429", "Rate limit exceeded"}},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c := &Client{BaseURL: srv.URL, APIKey: "test-key"}
		ch, err := c.ChatStream(context.Background(), ChatRequest{Model: "m"})
		if err == nil {
			t.Fatalf("status %d: want error, got channel %v", tc.status, ch)
		}
		for _, sub := range tc.wantSub {
			if !strings.Contains(err.Error(), sub) {
				t.Errorf("status %d: error %q missing %q", tc.status, err, sub)
			}
		}
		srv.Close()
	}
}

func TestChatStreamContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"gen-c","model":"m","choices":[{"delta":{"content":"first"},"finish_reason":null}]}` + "\n\n"))
		w.(http.Flusher).Flush()
		// Hold the stream open until the client cancels.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{BaseURL: srv.URL, APIKey: "test-key"}
	ch, err := c.ChatStream(ctx, ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	// Consume the first delta so we know the stream is live, then cancel.
	select {
	case ev := <-ch:
		if ev.Type != EventDelta || ev.Delta != "first" {
			t.Fatalf("first event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first delta")
	}
	cancel()

	// The channel must close promptly (an error event may precede the close);
	// the close is the reader goroutine's done signal, so no leak.
	timeout := time.After(5 * time.Second)
	sawError := false
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if !sawError {
					t.Log("channel closed without an error event (send raced ctx.Done); acceptable")
				}
				return
			}
			if ev.Type == EventError {
				sawError = true
			} else if ev.Type == EventDone {
				t.Fatalf("got done after cancel: %+v", ev)
			}
		case <-timeout:
			t.Fatal("channel did not close after cancel")
		}
	}
}

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"a/one","name":"A One","description":"first","context_length":128000,"pricing":{"prompt":"0.000003","completion":"0.000015"}},
			{"id":"b/two","name":"B Two","pricing":{"prompt":"0","completion":"0"}}
		]}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	m := models[0]
	if m.ID != "a/one" || m.Name != "A One" || m.Description != "first" || m.ContextLength != 128000 {
		t.Errorf("model 0 = %+v", m)
	}
	if m.Pricing.Prompt != "0.000003" || m.Pricing.Completion != "0.000015" {
		t.Errorf("model 0 pricing = %+v", m.Pricing)
	}
	if models[1].ID != "b/two" || models[1].ContextLength != 0 {
		t.Errorf("model 1 = %+v", models[1])
	}
}
