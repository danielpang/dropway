// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ai

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/danielpang/dropway/internal/openrouter"
)

// fastTiming keeps retry sleeps out of the unit-test run.
var fastTiming = writerTiming{
	attempts:       3,
	attemptTimeout: time.Second,
	initialBackoff: time.Millisecond,
}

// TestTranscriptWriterOrdersAndDrains verifies messages are persisted in enqueue
// order, Flush reports success, and Close waits for the backlog.
func TestTranscriptWriterOrdersAndDrains(t *testing.T) {
	var mu sync.Mutex
	var got []string
	w := startTranscriptWriter(context.Background(),
		func(_ context.Context, role string, body json.RawMessage) error {
			mu.Lock()
			defer mu.Unlock()
			var m openrouter.Message
			if err := json.Unmarshal(body, &m); err != nil {
				t.Errorf("unmarshal persisted body: %v", err)
			}
			got = append(got, role+":"+m.Content)
			return nil
		}, slog.Default(), nil)

	w.Append(openrouter.Message{Role: "user", Content: "a"})
	w.Append(openrouter.Message{Role: "assistant", Content: "b"})
	w.Append(openrouter.Message{Role: "tool", Content: "c"})
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush() = %v, want nil", err)
	}
	w.Close()

	mu.Lock()
	defer mu.Unlock()
	want := []string{"user:a", "assistant:b", "tool:c"}
	if len(got) != len(want) {
		t.Fatalf("persisted %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("persisted %v, want %v", got, want)
		}
	}
}

// TestTranscriptWriterAppendDoesNotBlockOnSlowDB verifies the turn-side Append
// returns immediately even when every write hangs.
func TestTranscriptWriterAppendDoesNotBlockOnSlowDB(t *testing.T) {
	release := make(chan struct{})
	w := startTranscriptWriter(context.Background(),
		func(ctx context.Context, _ string, _ json.RawMessage) error {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		}, slog.Default(), nil)
	defer close(release)

	done := make(chan struct{})
	go func() {
		for range 100 {
			w.Append(openrouter.Message{Role: "user", Content: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Append blocked on a hung database write")
	}
}

// TestTranscriptWriterRetriesThenSucceeds verifies a transient failure is
// retried rather than failing the writer.
func TestTranscriptWriterRetriesThenSucceeds(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	w := startTranscriptWriterTimed(context.Background(),
		func(_ context.Context, _ string, _ json.RawMessage) error {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 1 {
				return errors.New("transient connection blip")
			}
			return nil
		}, slog.Default(), nil, fastTiming)

	w.Append(openrouter.Message{Role: "assistant", Content: "hello"})
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush() = %v, want nil", err)
	}
	w.Close()

	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("append called %d times, want 2 (one failure + one retry)", calls)
	}
}

// TestTranscriptWriterPermanentErrorFailsFast verifies a permanently-rejected
// statement fails on the FIRST attempt (no retry burn), discards the backlog,
// cancels the turn via onFail, and discards later Appends.
func TestTranscriptWriterPermanentErrorFailsFast(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	failed := make(chan struct{})
	w := startTranscriptWriterTimed(context.Background(),
		func(_ context.Context, _ string, _ json.RawMessage) error {
			mu.Lock()
			defer mu.Unlock()
			calls++
			return &pgconn.PgError{Code: "22001", Message: "value too long"}
		}, slog.Default(), func() { close(failed) }, fastTiming)

	w.Append(openrouter.Message{Role: "assistant", Content: "too big"})
	w.Append(openrouter.Message{Role: "tool", Content: "queued behind it"})

	select {
	case <-failed:
	case <-time.After(2 * time.Second):
		t.Fatal("onFail was not called for a permanent write error")
	}
	if err := w.Flush(); err == nil {
		t.Fatal("Flush() = nil, want the permanent write error")
	}
	if w.Err() == nil {
		t.Fatal("Err() = nil after a permanent failure")
	}

	// The turn is stopping: later messages must be discarded, not written.
	w.Append(openrouter.Message{Role: "assistant", Content: "after failure"})
	w.Close()
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("append called %d times, want exactly 1 (fail fast, discard the rest)", calls)
	}
}

// TestTranscriptWriterTransientExhaustionStopsQueue verifies that when the
// retry budget is exhausted the writer fails as a whole (prefix semantics)
// instead of dropping the message and persisting later ones around the hole.
func TestTranscriptWriterTransientExhaustionStopsQueue(t *testing.T) {
	var mu sync.Mutex
	var roles []string
	w := startTranscriptWriterTimed(context.Background(),
		func(_ context.Context, role string, _ json.RawMessage) error {
			mu.Lock()
			defer mu.Unlock()
			roles = append(roles, role)
			return errors.New("db down")
		}, slog.Default(), nil, fastTiming)

	w.Append(openrouter.Message{Role: "assistant", Content: "with tool_calls"})
	w.Append(openrouter.Message{Role: "tool", Content: "its result"})
	if err := w.Flush(); err == nil {
		t.Fatal("Flush() = nil, want the exhausted-retries error")
	}
	w.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(roles) != fastTiming.attempts {
		t.Fatalf("append saw %d calls (%v), want %d attempts on the first message only",
			len(roles), roles, fastTiming.attempts)
	}
	for _, r := range roles {
		if r != "assistant" {
			t.Fatalf("a later message (%q) was attempted after the head failed; queue must stop", r)
		}
	}
}

// TestSanitizeHistory verifies the load-time safety net: incomplete
// assistant/tool blocks and orphan tool messages are dropped so the provider
// never sees an invalid conversation.
func TestSanitizeHistory(t *testing.T) {
	asst := func(content string, callIDs ...string) openrouter.Message {
		m := openrouter.Message{Role: "assistant", Content: content}
		for _, id := range callIDs {
			m.ToolCalls = append(m.ToolCalls, openrouter.ToolCall{ID: id})
		}
		return m
	}
	tool := func(callID string) openrouter.Message {
		return openrouter.Message{Role: "tool", ToolCallID: callID, Content: "out"}
	}
	user := openrouter.Message{Role: "user", Content: "hi"}

	cases := []struct {
		name string
		in   []openrouter.Message
		want []string // resulting roles, in order
	}{
		{"plain conversation untouched",
			[]openrouter.Message{user, asst("reply")},
			[]string{"user", "assistant"}},
		{"complete tool block kept",
			[]openrouter.Message{user, asst("", "c1", "c2"), tool("c1"), tool("c2"), asst("done")},
			[]string{"user", "assistant", "tool", "tool", "assistant"}},
		{"orphan tool result dropped",
			[]openrouter.Message{user, tool("ghost"), asst("done")},
			[]string{"user", "assistant"}},
		{"assistant with missing tool result dropped with its partial results",
			[]openrouter.Message{user, asst("", "c1", "c2"), tool("c1"), asst("done")},
			[]string{"user", "assistant"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeHistory(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d messages, want %d (%v)", len(got), len(tc.want), tc.want)
			}
			for i, m := range got {
				if m.Role != tc.want[i] {
					t.Fatalf("message %d role = %q, want %q", i, m.Role, tc.want[i])
				}
			}
		})
	}
}
