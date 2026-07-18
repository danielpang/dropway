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

	"github.com/danielpang/dropway/internal/openrouter"
)

// TestTranscriptWriterOrdersAndDrains verifies messages are persisted in enqueue
// order and Close waits for the backlog.
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
		}, slog.Default())

	w.Append(openrouter.Message{Role: "user", Content: "a"})
	w.Append(openrouter.Message{Role: "assistant", Content: "b"})
	w.Append(openrouter.Message{Role: "tool", Content: "c"})
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
		}, slog.Default())
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
// retried rather than dropping the message or failing the turn.
func TestTranscriptWriterRetriesThenSucceeds(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	w := startTranscriptWriter(context.Background(),
		func(_ context.Context, _ string, _ json.RawMessage) error {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 1 {
				return errors.New("transient")
			}
			return nil
		}, slog.Default())

	w.Append(openrouter.Message{Role: "assistant", Content: "hello"})
	w.Close()

	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("append called %d times, want 2 (one failure + one retry)", calls)
	}
	if w.dropped != 0 {
		t.Fatalf("dropped = %d, want 0", w.dropped)
	}
}
