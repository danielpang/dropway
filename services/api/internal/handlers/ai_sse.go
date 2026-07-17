// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/danielpang/dropway/internal/quota"
	aipkg "github.com/danielpang/dropway/services/api/internal/ai"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// writeSSE serializes a builder event as an SSE frame and flushes it.
func writeSSE(w http.ResponseWriter, f http.Flusher, ev aipkg.Event) {
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, body)
	f.Flush()
}

// writeSSERaw writes an arbitrary JSON payload as an SSE frame with an optional
// event id (the message seq, so a reconnect resumes via Last-Event-ID).
func writeSSERaw(w http.ResponseWriter, f http.Flusher, id int32, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if id > 0 {
		fmt.Fprintf(w, "id: %d\n", id)
	}
	fmt.Fprintf(w, "data: %s\n\n", body)
	f.Flush()
}

// contextWithTimeout is a thin wrapper so the handler file needn't import
// context directly for the one deadline it sets.
func contextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// clearWriteDeadline removes the server's global WriteTimeout for THIS response
// only. That timeout is an absolute deadline for the whole response body, and a
// builder turn streams SSE for minutes — without clearing it the server closes
// the connection ~30s in ("connection closed before message completed" at the
// proxy) and the request context cancels mid-turn. Normal JSON routes keep the
// global timeout; slowloris protection stays with ReadHeaderTimeout. Every
// ResponseWriter wrapper in the middleware chain must expose Unwrap for the
// ResponseController to reach the connection; a failure here means the global
// timeout still applies, so it is logged loudly rather than swallowed.
func clearWriteDeadline(w http.ResponseWriter, r *http.Request) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		logger(r).Error("ai: clear write deadline failed; stream will die at the server WriteTimeout", "err", err)
	}
}

func parseInt32(s string) (int32, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	return int32(n), err
}

// aiErrorMessage maps a turn error to a client-facing SSE error message. The two
// spend limits surface DIFFERENT text: the monthly cap is raisable in settings,
// the per-turn backstop is not (it's a fixed runaway guard). Concurrency and
// not-found map to clear strings; anything else is generic (internals are logged,
// not leaked).
func aiErrorMessage(err error) string {
	if ex, ok := quota.AsExceeded(err); ok {
		if ex.Limit == aipkg.AITurnSpendLimit {
			return "this request reached the per-turn spending limit and stopped; start a new message to continue"
		}
		return "monthly AI spend cap reached; raise it in settings to continue"
	}
	switch {
	case errors.Is(err, store.ErrAIConcurrencyLimit):
		return "too many active AI sessions"
	case errors.Is(err, store.ErrNotFound):
		return "session or site not found"
	default:
		return "the AI builder hit an error and stopped"
	}
}
