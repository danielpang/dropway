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
// database), so a slow database can't stall the model stream.
//
// Failure semantics are fail-stop: the first write that fails permanently (or
// exhausts its transient-retry budget) marks the writer failed, DISCARDS the
// rest of the queue, and cancels the turn via onFail. The persisted transcript
// therefore always stays a clean prefix of the real conversation — a mid-turn
// hole (e.g. a tool result whose parent assistant tool_calls message was lost)
// can never be written, which is what keeps loadHistory valid for future turns.
// The caller surfaces the failure to the user, who simply retries the message.
//
// Ordering across turns: RunTurn Flushes the writer before publishing the draft
// and its deferred Close drains (bounded) before the session claim is released,
// so the next turn's loadHistory sees the full transcript and per-session seq
// allocation stays single-writer.
type transcriptWriter struct {
	append appendFn
	log    *slog.Logger
	ctx    context.Context // detached from the request; writes outlive a disconnect
	onFail func()          // cancels the turn; called at most once, outside the lock
	timing writerTiming

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []queuedMsg
	closed  bool
	writing bool // a write is in flight (the queue alone doesn't show it)
	err     error

	done chan struct{} // closed when the goroutine has drained or failed
}

// queuedMsg is one pending transcript row.
type queuedMsg struct {
	role string
	body json.RawMessage
}

// appendFn persists one already-marshaled message row (the store call, bound to
// a tenant + session; a plain func so tests can substitute it).
type appendFn func(ctx context.Context, role string, body json.RawMessage) error

// writerTiming bounds one message's persistence attempts. The worst case per
// message (attempts x attemptTimeout + backoff sleeps, ~16.5s at the defaults)
// is what Flush and Close can block for when the database is failing, so keep
// it far below the turn deadline.
type writerTiming struct {
	attempts       int
	attemptTimeout time.Duration
	initialBackoff time.Duration
}

var defaultWriterTiming = writerTiming{
	attempts:       3,
	attemptTimeout: 5 * time.Second,
	initialBackoff: 500 * time.Millisecond,
}

// transcriptCloseBackstop bounds Close's wait for the drain. With the timing
// above a failing head message resolves (and discards the rest) in ~17s, so
// this should never fire; if it does, something is deeply wrong and it is
// logged as an error rather than wedging the turn's goroutine forever.
const transcriptCloseBackstop = 30 * time.Second

// newTranscriptWriter starts the writer goroutine for one turn. onFail is
// called (once) when persistence fails, so the turn can stop promptly.
func (r *Runner) newTranscriptWriter(ctx context.Context, t store.Tenant, sessionID string, onFail func()) *transcriptWriter {
	return startTranscriptWriter(ctx,
		func(ctx context.Context, role string, body json.RawMessage) error {
			_, err := r.Store.AppendAIMessage(ctx, t, sessionID, role, body)
			return err
		},
		r.logger().With("session_id", sessionID, "org_id", t.OrgID),
		onFail)
}

// startTranscriptWriter is the constructor proper (tenant-agnostic, testable).
func startTranscriptWriter(ctx context.Context, append appendFn, log *slog.Logger, onFail func()) *transcriptWriter {
	return startTranscriptWriterTimed(ctx, append, log, onFail, defaultWriterTiming)
}

func startTranscriptWriterTimed(ctx context.Context, append appendFn, log *slog.Logger, onFail func(), timing writerTiming) *transcriptWriter {
	w := &transcriptWriter{
		append: append,
		log:    log,
		ctx:    context.WithoutCancel(ctx),
		onFail: onFail,
		timing: timing,
		done:   make(chan struct{}),
	}
	w.cond = sync.NewCond(&w.mu)
	go w.run()
	return w
}

// Append enqueues one message for persistence. It never blocks on the database
// and never fails the caller. After a persistence failure the message is
// discarded: the turn is already stopping, and writing it would punch a hole in
// the transcript prefix.
func (w *transcriptWriter) Append(m openrouter.Message) {
	body, err := json.Marshal(m)
	if err != nil {
		// Unreachable for the message shapes we build (plain structs/strings);
		// guarded anyway so a future field can't panic the turn.
		w.log.Error("ai: transcript message not marshalable; dropped", "role", m.Role, "err", err)
		return
	}
	w.mu.Lock()
	if w.err == nil {
		w.queue = append(w.queue, queuedMsg{role: m.Role, body: body})
	}
	w.mu.Unlock()
	w.cond.Broadcast()
}

// Err returns the first persistence failure, or nil. Once non-nil the writer
// has discarded its backlog and stopped; the turn must not continue.
func (w *transcriptWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Flush blocks until every enqueued message is durably written (or the writer
// has failed) and returns the failure, if any. RunTurn calls this before
// publishing the draft, so a turn never "succeeds" with its transcript still in
// flight and the session claim is never released ahead of the backlog.
func (w *transcriptWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for (len(w.queue) > 0 || w.writing) && w.err == nil {
		w.cond.Wait()
	}
	return w.err
}

// Close marks the queue complete and waits (bounded) for the goroutine to
// finish, so the session claim released after it can't race the backlog.
func (w *transcriptWriter) Close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.cond.Broadcast()
	select {
	case <-w.done:
	case <-time.After(transcriptCloseBackstop):
		w.log.Error("ai: transcript writer failed to drain in time; abandoning it")
	}
}

// run drains the queue in order. It exits when Close has been called and the
// queue is empty, or immediately on the first failed write (after discarding
// the backlog and cancelling the turn).
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
		w.writing = true
		w.mu.Unlock()

		err := w.write(next)

		w.mu.Lock()
		w.writing = false
		if err != nil {
			w.err = err
			w.queue = nil // discard: the persisted transcript stays a clean prefix
		}
		w.mu.Unlock()
		w.cond.Broadcast()
		if err != nil {
			w.log.Error("ai: transcript persistence failed; stopping the turn", "role", next.role, "err", err)
			if w.onFail != nil {
				w.onFail()
			}
			return
		}
	}
}

// write persists one message. Transient failures (connection loss, timeout,
// resource pressure) retry with backoff; a permanently-rejected statement
// (store.IsPermanentWriteError) fails immediately instead of burning the budget
// while the rest of the queue waits behind it.
func (w *transcriptWriter) write(m queuedMsg) error {
	backoff := w.timing.initialBackoff
	var lastErr error
	for attempt := 1; attempt <= w.timing.attempts; attempt++ {
		ctx, cancel := context.WithTimeout(w.ctx, w.timing.attemptTimeout)
		err := w.append(ctx, m.role, m.body)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if store.IsPermanentWriteError(err) {
			return err
		}
		if attempt < w.timing.attempts {
			w.log.Warn("ai: transcript write failed; retrying", "role", m.role, "attempt", attempt, "err", err)
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return lastErr
}
