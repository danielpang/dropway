package openrouter

// SSE stream handling for chat completions: line parsing, chunk decoding, and
// assembly of text deltas + incrementally streamed tool calls into the final
// assistant message.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ChatStream POSTs a streaming chat completion and returns a channel of
// events: one "delta" per text fragment as it arrives, then exactly one
// terminal event ("done" with the assembled assistant message + usage, or
// "error"), after which the channel is closed.
//
// A non-2xx HTTP response is returned as an error from ChatStream itself (the
// channel is never created). Mid-stream failures (ctx cancellation, network
// errors, malformed chunks) arrive as the terminal "error" event.
//
// The reader goroutine exits when the stream ends or ctx is cancelled, so a
// caller that stops consuming must cancel ctx (or drain the channel) to
// release it.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest) (<-chan Event, error) {
	body, err := json.Marshal(chatRequestWire{
		Model:       req.Model,
		Messages:    req.Messages,
		Tools:       req.Tools,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      true,
		Usage:       usageOptIn{Include: true},
	})
	if err != nil {
		return nil, fmt.Errorf("openrouter: encode chat request: %w", err)
	}

	httpReq, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openrouter: chat request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, apiError(resp.StatusCode, errBody)
	}

	events := make(chan Event)
	go readStream(ctx, resp.Body, events)
	return events, nil
}

// Wire shapes of one SSE chunk.

type streamChunk struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []streamChoice `json:"choices"`
	Usage   *streamUsage   `json:"usage"`
}

type streamChoice struct {
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Content   string          `json:"content"`
	ToolCalls []toolCallDelta `json:"tool_calls"`
}

// toolCallDelta is one tool-call fragment: id/type/name arrive on the first
// fragment for an index, arguments concatenate across fragments.
type toolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type streamUsage struct {
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	Cost             float64 `json:"cost"`
}

// readStream owns resp.Body and the events channel: it parses SSE lines,
// emits deltas, and always closes the channel exactly once after the single
// terminal event.
func readStream(ctx context.Context, body io.ReadCloser, events chan<- Event) {
	defer close(events)
	defer body.Close()

	// send delivers ev unless the caller has gone away (ctx cancelled), which
	// is the one case where blocking on the channel could leak this goroutine.
	send := func(ev Event) bool {
		select {
		case events <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	fail := func(err error) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		send(Event{Type: EventError, Err: fmt.Errorf("openrouter: stream: %w", err)})
	}
	finish := func(asm *assembler) {
		msg, usage := asm.finish()
		send(Event{Type: EventDone, Message: msg, Usage: usage})
	}

	asm := &assembler{}
	// ReadString (not a Scanner) so an arbitrarily long data line cannot
	// overflow a fixed token buffer.
	r := bufio.NewReader(body)
	for {
		line, readErr := r.ReadString('\n')
		if line != "" {
			trimmed := strings.TrimRight(line, "\r\n")
			switch {
			case trimmed == "":
				// Blank event separator.
			case strings.HasPrefix(trimmed, ":"):
				// SSE comment/keepalive (": OPENROUTER PROCESSING").
			case strings.HasPrefix(trimmed, "data:"):
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if payload == "[DONE]" {
					finish(asm)
					return
				}
				var chunk streamChunk
				if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
					fail(fmt.Errorf("malformed chunk: %w", err))
					return
				}
				if delta, ok := asm.apply(chunk); ok {
					if !send(Event{Type: EventDelta, Delta: delta}) {
						return
					}
				}
			default:
				// Other SSE fields (event:, id:, retry:) are irrelevant here.
			}
		}
		if readErr != nil {
			// A clean EOF without [DONE] still terminates the generation.
			if errors.Is(readErr, io.EOF) && ctx.Err() == nil {
				finish(asm)
				return
			}
			fail(readErr)
			return
		}
	}
}

// assembler accumulates one streamed completion: text deltas into Content,
// tool-call fragments by index, and the id/model/usage accounting.
type assembler struct {
	content strings.Builder
	calls   []ToolCall // indexed by the wire tool-call index
	usage   *Usage
}

// apply folds one chunk into the assembly and returns the text delta to emit
// (ok=false when the chunk carried no text).
func (a *assembler) apply(chunk streamChunk) (delta string, ok bool) {
	if a.usage == nil {
		a.usage = &Usage{}
	}
	if chunk.ID != "" {
		a.usage.GenerationID = chunk.ID
	}
	if chunk.Model != "" {
		a.usage.Model = chunk.Model
	}
	if chunk.Usage != nil {
		a.usage.PromptTokens = chunk.Usage.PromptTokens
		a.usage.CompletionTokens = chunk.Usage.CompletionTokens
		a.usage.Cost = chunk.Usage.Cost
	}
	if len(chunk.Choices) == 0 {
		return "", false
	}
	// Requests are always n=1, so only the first choice carries content.
	choice := chunk.Choices[0]
	for _, tc := range choice.Delta.ToolCalls {
		a.applyToolDelta(tc)
	}
	if choice.Delta.Content == "" {
		return "", false
	}
	a.content.WriteString(choice.Delta.Content)
	return choice.Delta.Content, true
}

func (a *assembler) applyToolDelta(d toolCallDelta) {
	if d.Index < 0 {
		return
	}
	for len(a.calls) <= d.Index {
		a.calls = append(a.calls, ToolCall{Type: "function"})
	}
	call := &a.calls[d.Index]
	if d.ID != "" {
		call.ID = d.ID
	}
	if d.Type != "" {
		call.Type = d.Type
	}
	if d.Function.Name != "" {
		call.Function.Name = d.Function.Name
	}
	call.Function.Arguments += d.Function.Arguments
}

// finish returns the assembled assistant message (tool calls ordered by their
// wire index) and the accumulated usage (nil only if no chunk ever arrived).
func (a *assembler) finish() (Message, *Usage) {
	msg := Message{Role: "assistant", Content: a.content.String()}
	if len(a.calls) > 0 {
		msg.ToolCalls = a.calls
	}
	return msg, a.usage
}
