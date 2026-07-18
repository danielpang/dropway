// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/danielpang/dropway/internal/openrouter"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// transcriptWriter persists a turn's messages asynchronously, in order, on a
// single background goroutine. The turn loop only enqueues (never blocks on the
// database), so a slow or briefly-unavailable database can't stall the model
// stream or fail the conversation. Each write retries with backoff on a context
// detached from the request, so a client disconnect doesn't lose the transcript.
//
// Ordering: one writer per turn, and RunTurn's deferred Close drains the queue
// before the session claim (TryBeginAITurn) is released, so the next turn's
// loadHistory sees the full transcript and per-session seq stays single-writer.
// If the drain deadline expires (database down), the claim is released anyway —
// a stuck session would be worse — and the writer keeps flushing in the
// background; AppendAIMessage's seq-collision retry absorbs the rare overlap
// with the next turn.
type transcriptWriter struct {
	append appendFn
	log    *slog.Logger
	ctx    context.Context // detached from the request; writes outlive a disconnect

	mu      sync.Mutex
	cond    *sync.Cond
	queue   [][2]string // (role, marshaled message body)
	closed  bool
	dropped int

	done chan struct{} // closed when the goroutine has drained and exited
}

const (
	// transcriptWriteAttempts bounds retries per message before it is dropped
	// (logged loudly); transcriptAttemptTimeout bounds each individual attempt.
	transcriptWriteAttempts  = 4
	transcriptAttemptTimeout = 10 * time.Second
	transcriptInitialBackoff = time.Second
	// transcriptDrainGrace is how long Close waits for the backlog before
	// releasing the turn anyway. The queue drains continuously while the model
	// generates, so a healthy close is instant; this only bites when the
	// database is down at end of turn.
	transcriptDrainGrace = 15 * time.Second
)

// appendFn persists one already-marshaled message row (the store call, bound to
// a tenant + session; a plain func so tests can substitute it).
type appendFn func(ctx context.Context, role string, body json.RawMessage) error

// newTranscriptWriter starts the writer goroutine for one turn.
func (r *Runner) newTranscriptWriter(ctx context.Context, t store.Tenant, sessionID string) *transcriptWriter {
	return startTranscriptWriter(ctx,
		func(ctx context.Context, role string, body json.RawMessage) error {
			_, err := r.Store.AppendAIMessage(ctx, t, sessionID, role, body)
			return err
		},
		r.logger().With("session_id", sessionID, "org_id", t.OrgID))
}

// startTranscriptWriter is the constructor proper (tenant-agnostic, testable).
func startTranscriptWriter(ctx context.Context, append appendFn, log *slog.Logger) *transcriptWriter {
	w := &transcriptWriter{
		append: append,
		log:    log,
		ctx:    context.WithoutCancel(ctx),
		done:   make(chan struct{}),
	}
	w.cond = sync.NewCond(&w.mu)
	go w.run()
	return w
}

// Append enqueues one message for persistence. It never blocks on the database
// and never fails the caller; an unmarshalable message (can't happen for the
// shapes we build) is counted as dropped.
func (w *transcriptWriter) Append(m openrouter.Message) {
	body, err := json.Marshal(m)
	if err != nil {
		w.mu.Lock()
		w.dropped++
		w.mu.Unlock()
		w.log.Error("ai: transcript message not marshalable; dropped", "role", m.Role, "err", err)
		return
	}
	w.mu.Lock()
	w.queue = append(w.queue, [2]string{m.Role, string(body)})
	w.mu.Unlock()
	w.cond.Signal()
}

// Close marks the queue complete and waits up to transcriptDrainGrace for the
// backlog to drain, so the transcript is durable before the session claim is
// released. On timeout it returns (the writer keeps flushing in the background)
// after logging the lag.
func (w *transcriptWriter) Close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.cond.Signal()

	select {
	case <-w.done:
	case <-time.After(transcriptDrainGrace):
		w.log.Error("ai: transcript persistence lagging; releasing the session before the backlog drained")
	}
	w.mu.Lock()
	dropped := w.dropped
	w.mu.Unlock()
	if dropped > 0 {
		w.log.Error("ai: transcript incomplete for this turn", "dropped_messages", dropped)
	}
}

// run drains the queue in order until Close is called and the queue is empty.
func (w *transcriptWriter) run() {
	defer close(w.done)
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && !w.closed {
			w.cond.Wait()
		}
		if len(w.queue) == 0 {
			w.mu.Unlock()
			return
		}
		next := w.queue[0]
		w.queue = w.queue[1:]
		w.mu.Unlock()
		w.write(next[0], json.RawMessage(next[1]))
	}
}

// write persists one message, retrying transient failures with backoff. After
// the attempt budget the message is dropped (logged) rather than stalling the
// rest of the queue forever against a dead database.
func (w *transcriptWriter) write(role string, body json.RawMessage) {
	backoff := transcriptInitialBackoff
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(w.ctx, transcriptAttemptTimeout)
		err := w.append(ctx, role, body)
		cancel()
		if err == nil {
			return
		}
		if attempt >= transcriptWriteAttempts {
			w.mu.Lock()
			w.dropped++
			w.mu.Unlock()
			w.log.Error("ai: transcript message dropped after retries", "role", role, "attempts", attempt, "err", err)
			return
		}
		w.log.Warn("ai: transcript write failed; retrying", "role", role, "attempt", attempt, "err", err)
		time.Sleep(backoff)
		backoff *= 2
	}
}
